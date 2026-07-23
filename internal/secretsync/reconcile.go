package secretsync

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"sort"
	"time"

	"github.com/steveokay/janus-secrets/internal/audit"
	"github.com/steveokay/janus-secrets/internal/resolve"
	"github.com/steveokay/janus-secrets/internal/store"
)

const (
	backoffBase = 1 * time.Minute
	backoffCap  = 1 * time.Hour
)

// backoff: base*2^(n-1) capped at backoffCap; n is 1-based (first failure→base).
func backoff(failureCount int) time.Duration {
	d := backoffBase
	for i := 1; i < failureCount; i++ {
		d *= 2
		if d >= backoffCap {
			return backoffCap
		}
	}
	if d > backoffCap {
		return backoffCap
	}
	return d
}

// projectAuthorizer implements resolve.Authorizer for system-driven sync: a
// reference is followed only if its target config is in the SAME project as the
// sync target. This prevents a project admin from exfiltrating another project's
// secrets by syncing a config that references across projects.
type projectAuthorizer struct{ projectID string }

func (a projectAuthorizer) CanReadSecrets(_ context.Context, t resolve.RawConfig) error {
	if t.ProjectID != a.projectID {
		return resolve.ErrForbiddenReference
	}
	return nil
}

func (s *Service) providerFor(name string) (Provider, error) {
	switch name {
	case ProviderGitHub:
		return githubProvider{hc: s.hc, baseURL: s.githubBaseURL}, nil
	case ProviderK8s:
		return k8sProvider{}, nil
	case ProviderGitLab:
		return gitlabProvider{hc: s.hc}, nil
	case ProviderAWSSSM:
		return awsssmProvider{}, nil
	default:
		return nil, ErrInvalidType
	}
}

// resolveDesired returns the config's resolved key/value map (references
// expanded), authorized to the target's own project.
func (s *Service) resolveDesired(ctx context.Context, t *store.SyncTarget) (map[string]string, error) {
	r := resolve.New(s.secrets, projectAuthorizer{projectID: t.ProjectID})
	raw, _, err := r.Resolve(ctx, t.ConfigID)
	if err != nil {
		return nil, err
	}
	out := make(map[string]string, len(raw))
	for k, v := range raw {
		out[k] = string(v)
	}
	return out, nil
}

// fingerprint canonically serializes desired (sorted, length-prefixed) and
// returns the keyed HMAC via the keyring (nil while sealed).
func (s *Service) fingerprint(desired map[string]string) []byte {
	keys := make([]string, 0, len(desired))
	for k := range desired {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var buf []byte
	for _, k := range keys {
		buf = appendField(buf, k)
		buf = appendField(buf, desired[k])
	}
	return s.kr.SyncFingerprint(buf)
}

func appendField(b []byte, f string) []byte {
	b = binary.BigEndian.AppendUint64(b, uint64(len(f)))
	return append(b, f...)
}

// reconcile syncs one target. force skips change-detection (manual sync-now).
// startedAt marks the attempt's wall-clock start, recorded on the run row.
func (s *Service) reconcile(ctx context.Context, t *store.SyncTarget, force bool, startedAt time.Time) error {
	proj, err := s.projects.Get(ctx, t.ProjectID)
	if err != nil {
		return mapStoreErr(err)
	}
	creds, err := s.openCreds(proj, t) // ErrSealed while sealed
	if err != nil {
		return err
	}
	desired, err := s.resolveDesired(ctx, t)
	if err != nil {
		return err
	}
	fp := s.fingerprint(desired)
	if fp == nil {
		return ErrSealed
	}
	if !force && t.SyncedFingerprint != nil && bytes.Equal(fp, t.SyncedFingerprint) {
		return nil // unchanged — skip, no external calls
	}

	prov, err := s.providerFor(t.Provider)
	if err != nil {
		return err
	}
	var addr Addr
	if err := json.Unmarshal(t.Addr, &addr); err != nil {
		return ErrInvalidConfig
	}
	res, err := prov.Apply(ctx, creds, addr, desired, t.ManagedKeys, t.Prune)
	if err != nil {
		return err
	}
	next := s.now().Add(time.Duration(t.IntervalSeconds) * time.Second)
	// Record the config's current version at the moment of a successful push, so
	// synced_config_version reflects what was actually mirrored (not a frozen 0).
	cv, err := s.secrets.LatestVersion(ctx, t.ConfigID)
	if err != nil {
		return err
	}
	if err := s.repo.MarkSynced(ctx, t.ID, res.Applied, fp, cv, next, startedAt, t.FailureCount); err != nil {
		return mapStoreErr(err)
	}
	if len(res.Skipped) > 0 {
		s.logger.Warn("sync skipped keys", "target", t.ID, "skipped_count", len(res.Skipped))
	}
	return nil
}

// attempt reconciles and records audit + failure bookkeeping. Single entry point
// for scheduler and manual sync-now. Sealed is not a failure.
func (s *Service) attempt(ctx context.Context, t *store.SyncTarget, force bool) error {
	startedAt := s.now()
	err := s.reconcile(ctx, t, force, startedAt)
	if err != nil {
		if errors.Is(err, ErrSealed) {
			s.logger.Debug("sync skipped: server sealed", "target", t.ID)
			return err
		}
		next := s.now().Add(backoff(t.FailureCount + 1))
		if merr := s.repo.MarkFailure(ctx, t.ID, sanitize(err), next, failureThreshold, startedAt, t.FailureCount+1); merr != nil {
			s.logger.Warn("sync mark-failure failed", "target", t.ID, "err", merr)
		}
		s.recordSync(ctx, t, "failure", sanitize(err))
		return err
	}
	s.recordSync(ctx, t, "success", "")
	return nil
}

// sanitize maps an error to a fixed, value-free category for last_error/audit.
func sanitize(err error) string {
	switch {
	case errors.Is(err, ErrSealed):
		return "sealed"
	case errors.Is(err, ErrApplyFailed):
		return "apply failed"
	case errors.Is(err, ErrInvalidConfig):
		return "invalid config"
	case errors.Is(err, resolve.ErrForbiddenReference):
		return "forbidden reference"
	default:
		return "sync error"
	}
}

func (s *Service) recordSync(ctx context.Context, t *store.SyncTarget, result, detail string) {
	if s.audit == nil {
		return
	}
	if err := s.audit.Record(ctx, audit.Event{
		Actor:    audit.Actor{Kind: "system", Name: "sync:" + t.ID},
		Action:   "sync.reconcile",
		Resource: "configs/" + t.ConfigID + " -> " + t.Provider,
		Detail:   detail,
		Result:   result,
	}); err != nil {
		s.logger.Warn("sync audit write failed", "target", t.ID, "err", err)
	}
}
