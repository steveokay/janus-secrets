package api

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/steveokay/janus-secrets/internal/crypto"
	"github.com/steveokay/janus-secrets/internal/secrets"
	"github.com/steveokay/janus-secrets/internal/store"
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
	srv := New(Config{ListenAddr: bc.ListenAddr, SealType: sealType}, kr, unsealer, seals, svc, logger)

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
