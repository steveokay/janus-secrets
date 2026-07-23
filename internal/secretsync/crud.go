package secretsync

import (
	"context"
	"encoding/json"
	"time"

	"github.com/steveokay/janus-secrets/internal/store"
)

// TargetInput is the create payload (plaintext creds; encrypted here).
type TargetInput struct {
	ConfigID        string
	Provider        string
	Prune           bool
	IntervalSeconds int64
	Addr            Addr
	Creds           Creds
}

// TargetView is the masked, safe-to-return projection (NO creds/fingerprint).
// Addr is included — it holds only non-secret destination coordinates.
type TargetView struct {
	ID              string
	ProjectID       string
	ConfigID        string
	Provider        string
	Prune           bool
	IntervalSeconds int64
	Addr            Addr
	Status          string
	FailureCount    int
	LastError       *string
	NextSyncAt      time.Time
	LastSyncedAt    *time.Time
	ManagedKeys     []string
	CreatedAt       time.Time
}

// view projects a store.SyncTarget onto the masked, safe-to-return TargetView.
// It NEVER copies creds columns or the synced fingerprint. If Addr fails to
// unmarshal, it is left zero rather than failing the read.
func view(t *store.SyncTarget) TargetView {
	var addr Addr
	_ = json.Unmarshal(t.Addr, &addr)
	return TargetView{
		ID: t.ID, ProjectID: t.ProjectID, ConfigID: t.ConfigID, Provider: t.Provider,
		Prune: t.Prune, IntervalSeconds: t.IntervalSeconds, Addr: addr, Status: t.Status,
		FailureCount: t.FailureCount, LastError: t.LastError, NextSyncAt: t.NextSyncAt,
		LastSyncedAt: t.LastSyncedAt, ManagedKeys: t.ManagedKeys, CreatedAt: t.CreatedAt,
	}
}

// validateInput checks a provider/addr/creds combination for structural
// completeness before anything is encrypted or persisted.
func validateInput(provider string, addr Addr, creds Creds) error {
	switch provider {
	case ProviderGitHub:
		if addr.Owner == "" || addr.Repo == "" || creds.PAT == "" {
			return ErrInvalidConfig
		}
	case ProviderK8s:
		// CACert is required: the k8s provider verifies TLS against it, and an
		// empty CA makes verification impossible — fail fast here.
		if creds.APIURL == "" || creds.Token == "" || creds.CACert == "" ||
			addr.Namespace == "" || addr.SecretName == "" {
			return ErrInvalidConfig
		}
	case ProviderGitLab:
		// gitlab_url is optional (defaults to gitlab.com); project + token required.
		if creds.Token == "" || addr.Project == "" {
			return ErrInvalidConfig
		}
	case ProviderAWSSSM:
		if creds.AccessKeyID == "" || creds.SecretAccessKey == "" ||
			addr.Region == "" || addr.PathPrefix == "" {
			return ErrInvalidConfig
		}
	default:
		return ErrInvalidType
	}
	return nil
}

// projectForConfig resolves the owning project of a config (for KEK + scope).
func (s *Service) projectForConfig(ctx context.Context, configID string) (*store.Project, error) {
	cfg, err := store.NewConfigRepo(s.st).Get(ctx, configID)
	if err != nil {
		return nil, mapStoreErr(err)
	}
	env, err := store.NewEnvironmentRepo(s.st).Get(ctx, cfg.EnvironmentID)
	if err != nil {
		return nil, mapStoreErr(err)
	}
	proj, err := s.projects.Get(ctx, env.ProjectID)
	if err != nil {
		return nil, mapStoreErr(err)
	}
	return proj, nil
}

// Create validates, encrypts the creds blob, and inserts a sync target.
func (s *Service) Create(ctx context.Context, in TargetInput, createdBy string) (TargetView, error) {
	if in.IntervalSeconds <= 0 {
		return TargetView{}, ErrInvalidConfig
	}
	if err := validateInput(in.Provider, in.Addr, in.Creds); err != nil {
		return TargetView{}, err
	}
	proj, err := s.projectForConfig(ctx, in.ConfigID)
	if err != nil {
		return TargetView{}, err
	}
	id, err := s.st.NewID(ctx)
	if err != nil {
		return TargetView{}, err
	}
	ct, nonce, wrapped, kekVer, err := s.sealCreds(proj, id, in.Creds)
	if err != nil {
		return TargetView{}, err
	}
	addrBytes, err := json.Marshal(in.Addr)
	if err != nil {
		return TargetView{}, ErrInvalidConfig
	}
	t := &store.SyncTarget{
		ID: id, ProjectID: proj.ID, ConfigID: in.ConfigID, Provider: in.Provider,
		Prune: in.Prune, IntervalSeconds: in.IntervalSeconds,
		NextSyncAt:         s.now().Add(time.Duration(in.IntervalSeconds) * time.Second),
		CredsCT:            ct,
		CredsNonce:         nonce,
		CredsWrappedDEK:    wrapped,
		CredsDEKKEKVersion: kekVer,
		Addr:               addrBytes,
		CreatedBy:          createdBy,
	}
	saved, err := s.repo.Create(ctx, t)
	if err != nil {
		return TargetView{}, mapStoreErr(err)
	}
	return view(saved), nil
}

func (s *Service) Get(ctx context.Context, id string) (TargetView, error) {
	t, err := s.repo.Get(ctx, id)
	if err != nil {
		return TargetView{}, mapStoreErr(err)
	}
	return view(t), nil
}

// ListRuns returns recorded run history for a target, newest-first, keyset-paginated.
func (s *Service) ListRuns(ctx context.Context, targetID string, cursor int64, limit int) ([]store.SyncRun, error) {
	return s.repo.ListRuns(ctx, targetID, cursor, limit)
}

func (s *Service) ListByProject(ctx context.Context, projectID string) ([]TargetView, error) {
	ts, err := s.repo.ListByProject(ctx, projectID)
	if err != nil {
		return nil, mapStoreErr(err)
	}
	out := make([]TargetView, 0, len(ts))
	for _, t := range ts {
		out = append(out, view(t))
	}
	return out, nil
}

// Update changes interval/prune/status and/or the creds blob/destination
// address. nil args leave the corresponding stored value unchanged. Callers
// may set status only to active/paused.
func (s *Service) Update(ctx context.Context, id string, intervalSeconds *int64, prune *bool, status *string, creds *Creds, addr *Addr) (TargetView, error) {
	if status != nil && *status != "active" && *status != "paused" {
		return TargetView{}, ErrInvalidConfig // callers cannot set 'failed' directly
	}
	t, err := s.repo.Get(ctx, id)
	if err != nil {
		return TargetView{}, mapStoreErr(err)
	}
	var ct, nonce, wrapped []byte
	var kekVer *int
	if creds != nil {
		proj, err := s.projects.Get(ctx, t.ProjectID)
		if err != nil {
			return TargetView{}, mapStoreErr(err)
		}
		c, n, w, v, err := s.sealCreds(proj, id, *creds)
		if err != nil {
			return TargetView{}, err
		}
		ct, nonce, wrapped, kekVer = c, n, w, &v
	}
	var addrBytes []byte
	if addr != nil {
		b, err := json.Marshal(*addr)
		if err != nil {
			return TargetView{}, ErrInvalidConfig
		}
		addrBytes = b
	}
	if err := s.repo.Update(ctx, id, intervalSeconds, prune, status, ct, nonce, wrapped, kekVer, addrBytes); err != nil {
		return TargetView{}, mapStoreErr(err)
	}
	return s.Get(ctx, id)
}

func (s *Service) Delete(ctx context.Context, id string) error {
	return mapStoreErr(s.repo.Delete(ctx, id))
}

// SyncNow runs an immediate sync. It first marks the target due (so a crash
// mid-apply is recovered by the scheduler) and clears a 'failed' status so a
// manual trigger always attempts.
func (s *Service) SyncNow(ctx context.Context, id string) error {
	if _, err := s.repo.Get(ctx, id); err != nil {
		return mapStoreErr(err)
	}
	if s.kr.Sealed() {
		return ErrSealed
	}
	if err := s.repo.PrepareSyncNow(ctx, id, s.now()); err != nil {
		return mapStoreErr(err)
	}
	t, err := s.repo.Get(ctx, id)
	if err != nil {
		return mapStoreErr(err)
	}
	return s.attempt(ctx, t, true)
}
