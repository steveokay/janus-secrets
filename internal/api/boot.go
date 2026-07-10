package api

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/steveokay/janus-secrets/internal/audit"
	"github.com/steveokay/janus-secrets/internal/auth"
	"github.com/steveokay/janus-secrets/internal/authz"
	"github.com/steveokay/janus-secrets/internal/crypto"
	"github.com/steveokay/janus-secrets/internal/rotation"
	"github.com/steveokay/janus-secrets/internal/secrets"
	"github.com/steveokay/janus-secrets/internal/secretsync"
	"github.com/steveokay/janus-secrets/internal/store"
	"github.com/steveokay/janus-secrets/internal/transit"
	"github.com/steveokay/janus-secrets/internal/web"
)

// BootConfig is everything `janus server` derives from the environment.
type BootConfig struct {
	DatabaseURL string
	ListenAddr  string
	// SealType is the operator-configured seal type. Optional once
	// initialized (the stored type is authoritative); a conflicting value is
	// a fatal misconfiguration.
	SealType string
	// NewKMSClient lazily builds the KMS client, called only when the
	// effective seal type is awskms. cmd/janus supplies the real AWS
	// implementation; tests supply fakes.
	NewKMSClient func(context.Context) (crypto.KMSClient, error)
	Logger       *slog.Logger
	// SessionIdleTimeout is the session-cookie inactivity window (web UI and
	// CLI login sessions). Zero disables
	// idle enforcement (the 30m production default is applied by cmd/janus,
	// so tests that build BootConfig directly get no idle timeout).
	SessionIdleTimeout time.Duration
	// Version is the janus build version (cmd/janus's stamped version var),
	// recorded in backup headers.
	Version string
	// RotationTick is the rotation scheduler's tick interval. Zero disables the
	// scheduler (tests build BootConfig directly and get no scheduler); cmd/janus
	// applies the production default.
	RotationTick time.Duration
	// SyncTick is the sync scheduler's tick interval. Zero disables the scheduler
	// (tests build BootConfig directly and get no scheduler); cmd/janus applies
	// the production default.
	SyncTick time.Duration
}

// Boot opens the store, auto-migrates, resolves the seal configuration,
// builds the unsealer and (for an initialized KMS seal) auto-unseals, and
// returns the wired Server. The caller owns closing the returned Store.
func Boot(ctx context.Context, bc BootConfig) (*Server, *store.Store, error) {
	logger := bc.Logger
	if logger == nil {
		logger = slog.Default()
	}

	st, err := store.Open(ctx, bc.DatabaseURL)
	if err != nil {
		return nil, nil, err
	}
	if err := st.Migrate(ctx); err != nil {
		st.Close()
		return nil, nil, fmt.Errorf("migrate: %w", err)
	}

	seals := store.NewSealConfigStore(st)
	stored, err := seals.Get(ctx)
	initialized := true
	if errors.Is(err, crypto.ErrNoSealConfig) {
		initialized = false
	} else if err != nil {
		st.Close()
		return nil, nil, err
	}

	// Resolve the effective seal type: stored wins; env must agree if set.
	var sealType string
	if initialized {
		sealType = stored.Type
		if bc.SealType != "" && bc.SealType != sealType {
			st.Close()
			return nil, nil, fmt.Errorf(
				"seal type mismatch: JANUS_SEAL_TYPE=%q but stored seal is %q", bc.SealType, sealType)
		}
	} else {
		sealType = bc.SealType
		if sealType == "" {
			st.Close()
			return nil, nil, errors.New("JANUS_SEAL_TYPE is required before the seal is initialized")
		}
	}
	if sealType != crypto.SealTypeShamir && sealType != crypto.SealTypeAWSKMS {
		st.Close()
		return nil, nil, fmt.Errorf("unknown seal type %q", sealType)
	}

	kr := crypto.NewKeyring()
	var unsealer crypto.Unsealer
	switch sealType {
	case crypto.SealTypeShamir:
		unsealer = crypto.NewShamirUnsealer(seals, 0, 0)
	case crypto.SealTypeAWSKMS:
		if bc.NewKMSClient == nil {
			st.Close()
			return nil, nil, errors.New("kms seal requires a KMS client")
		}
		client, err := bc.NewKMSClient(ctx)
		if err != nil {
			st.Close()
			return nil, nil, err
		}
		unsealer = crypto.NewKMSUnsealer(seals, client)
	}

	svc := secrets.NewService(st, kr)
	transitSvc := transit.New(kr, st)
	authSvc := auth.NewService(st, kr)
	authSvc.SetSessionIdleTimeout(bc.SessionIdleTimeout)
	authorizer := authz.New(store.NewRoleBindingRepo(st))
	auditRec := audit.New(store.NewAuditRepo(st))
	rotationSvc := rotation.New(kr, st, svc, auditRec, logger)
	syncSvc := secretsync.New(kr, st, svc, auditRec, logger)
	// Sweep sessions orphaned by expiry while the server was down.
	if err := authSvc.SweepExpiredSessions(ctx); err != nil {
		logger.Warn("expired-session sweep failed", "err", err)
	}
	// Sweep OIDC login-state orphaned by expiry while the server was down.
	if err := authSvc.SweepExpiredOIDCRequests(ctx); err != nil {
		logger.Warn("expired-oidc-request sweep failed", "err", err)
	}
	// Never-lock-out: guarantee at least one instance owner exists.
	if err := reconcileInstanceOwner(ctx, st, authorizer, logger); err != nil {
		logger.Warn("instance-owner reconciliation failed", "err", err)
	}
	srv := New(Config{ListenAddr: bc.ListenAddr, SealType: sealType, Version: bc.Version}, kr, unsealer, seals, svc, transitSvc, rotationSvc, syncSvc, authSvc, authorizer, st, auditRec, logger)
	srv.MountUI(web.Handler())

	// Start the rotation scheduler tied to the boot ctx (runServer's shutdown
	// context), so it stops cleanly on SIGTERM. Zero tick (tests) disables it.
	if bc.RotationTick > 0 {
		go rotationSvc.RunScheduler(ctx, bc.RotationTick)
	}
	// Start the sync scheduler on the same boot ctx. Zero tick (tests) disables it.
	if bc.SyncTick > 0 {
		go syncSvc.RunScheduler(ctx, bc.SyncTick)
	}

	// KMS auto-unseal: best-effort at boot; failure keeps serving sealed and
	// POST /v1/sys/unseal retries.
	if initialized && sealType == crypto.SealTypeAWSKMS {
		if err := srv.unsealNow(ctx); err != nil {
			logger.Warn("kms auto-unseal failed; server remains sealed (retry via POST /v1/sys/unseal)",
				"err", err)
		}
	}
	return srv, st, nil
}

// reconcileInstanceOwner grants the oldest user instance owner when users exist
// but no instance owner does — self-heals an M5→M6 upgrade and guarantees the
// server can never be left with nobody able to administer it.
func reconcileInstanceOwner(ctx context.Context, st *store.Store, e *authz.Engine, logger *slog.Logger) error {
	n, err := e.CountInstanceOwners(ctx)
	if err != nil {
		return err
	}
	if n > 0 {
		return nil
	}
	users := store.NewUserRepo(st)
	cnt, err := users.Count(ctx)
	if err != nil {
		return err
	}
	if cnt == 0 {
		return nil // uninitialized; the init ceremony grants the owner
	}
	oldest, err := users.Oldest(ctx)
	if err != nil {
		return err
	}
	logger.Warn("no instance owner found; granting the oldest user instance owner", "user", oldest.Email)
	return e.Grant(ctx, store.RoleBindingInput{SubjectUserID: oldest.ID, ScopeLevel: "instance", Role: "owner"})
}
