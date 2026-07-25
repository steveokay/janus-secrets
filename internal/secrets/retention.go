package secrets

import (
	"context"
	"errors"
	"fmt"

	"github.com/steveokay/janus-secrets/internal/store"
)

// RetentionFloor is the instance-wide minimum-retention floor for secret
// value-version history (JANUS_SECRET_RETAIN_MIN_VERSIONS /
// JANUS_SECRET_RETAIN_MIN_DAYS). Both default to 0 = OFF, mirroring the audit
// retention floor: with no floor configured an owner may prune a config's
// history down to its latest version, which is the historical behavior of an
// instance that could not prune at all.
//
// A floor is a CEILING on how aggressive a prune request may be. It can only
// ever cause MORE to be retained, never less.
type RetentionFloor struct {
	// MinVersions retains at least the newest N config versions of every config.
	MinVersions int
	// MinDays retains every config version created within the last N days.
	MinDays int
}

// RetentionPolicy is the resolved retention picture for one config: the
// instance-wide floor, the config's own override (nil fields = no opinion), and
// the effective floor a prune of this config must respect.
type RetentionPolicy struct {
	Floor             RetentionFloor
	OverrideVersions  *int
	OverrideDays      *int
	EffectiveVersions int
	EffectiveDays     int
}

// PruneRequest is an operator's requested retention target for one config.
// At least one of KeepVersions / KeepDays must be positive; the effective
// values are then raised (never lowered) by the instance floor and the config's
// override.
type PruneRequest struct {
	KeepVersions int
	KeepDays     int
	DryRun       bool
}

// SetRetentionFloor installs the instance-wide minimum-retention floor. Server
// config, applied at boot; negative values are treated as 0 (off).
func (s *Service) SetRetentionFloor(f RetentionFloor) {
	if f.MinVersions < 0 {
		f.MinVersions = 0
	}
	if f.MinDays < 0 {
		f.MinDays = 0
	}
	s.retentionFloor = f
}

// RetentionFloor returns the instance-wide minimum-retention floor.
func (s *Service) RetentionFloor() RetentionFloor { return s.retentionFloor }

// GetVersionRetention resolves a config's retention policy: the instance floor,
// the config's override, and the effective floor. Value-free (integers only).
func (s *Service) GetVersionRetention(ctx context.Context, configID string) (RetentionPolicy, error) {
	if _, err := s.configs.Get(ctx, configID); err != nil {
		return RetentionPolicy{}, mapStoreErr(err)
	}
	ov, err := s.retention.Get(ctx, configID)
	if err != nil {
		return RetentionPolicy{}, mapStoreErr(err)
	}
	return s.resolvePolicy(ov), nil
}

// resolvePolicy folds the instance floor and a config override into the
// effective floor. The effective floor is the STRICTEST (largest) of the two, so
// a per-config override can only ever retain more than the instance-wide
// guarantee — never less.
func (s *Service) resolvePolicy(ov store.VersionRetention) RetentionPolicy {
	p := RetentionPolicy{
		Floor:             s.retentionFloor,
		OverrideVersions:  ov.MinVersions,
		OverrideDays:      ov.MinDays,
		EffectiveVersions: s.retentionFloor.MinVersions,
		EffectiveDays:     s.retentionFloor.MinDays,
	}
	if ov.MinVersions != nil && *ov.MinVersions > p.EffectiveVersions {
		p.EffectiveVersions = *ov.MinVersions
	}
	if ov.MinDays != nil && *ov.MinDays > p.EffectiveDays {
		p.EffectiveDays = *ov.MinDays
	}
	return p
}

// SetVersionRetention upserts a config's retention override. At least one of
// minVersions / minDays must be non-nil, and each non-nil value must be >= 1.
//
// The override can only STRENGTHEN retention: it is combined with the instance
// floor by taking the larger of each, so setting a small value on a config
// cannot defeat an operator's instance-wide floor.
func (s *Service) SetVersionRetention(ctx context.Context, configID string, minVersions, minDays *int, actor string) error {
	if minVersions == nil && minDays == nil {
		return fmt.Errorf("%w: set min_versions and/or min_days", ErrValidation)
	}
	if minVersions != nil && *minVersions < 1 {
		return fmt.Errorf("%w: min_versions must be >= 1 or null", ErrValidation)
	}
	if minDays != nil && *minDays < 1 {
		return fmt.Errorf("%w: min_days must be >= 1 or null", ErrValidation)
	}
	if _, err := s.configs.Get(ctx, configID); err != nil {
		return mapStoreErr(err)
	}
	return mapStoreErr(s.retention.Set(ctx, configID,
		store.VersionRetention{MinVersions: minVersions, MinDays: minDays}, actor))
}

// ClearVersionRetention removes a config's retention override, falling back to
// the instance floor. Clearing an absent override is a no-op.
func (s *Service) ClearVersionRetention(ctx context.Context, configID string) error {
	if _, err := s.configs.Get(ctx, configID); err != nil {
		return mapStoreErr(err)
	}
	return mapStoreErr(s.retention.Clear(ctx, configID))
}

// PruneVersions destroys old config VERSIONS of a config and garbage-collects
// the secret_values rows nothing references any more.
//
// Pruning is at the CONFIG-VERSION granularity — the unit of diff and rollback
// — never at the value-version granularity: config_version_entries cascades on
// secret_values delete, so removing a value row would silently strip keys out of
// old config versions. See store.SecretRepo.PruneConfigVersions for the full
// argument and for the in-transaction re-assertion of the invariant
//
//	every RETAINED config version is fully restorable.
//
// The effective floor applied is the strictest of {req, instance floor, config
// override}; the latest config version is never pruned. The returned result is
// value-free: version numbers and counts only.
func (s *Service) PruneVersions(ctx context.Context, configID string, req PruneRequest) (store.PruneResult, error) {
	if req.KeepVersions < 0 || req.KeepDays < 0 {
		return store.PruneResult{}, fmt.Errorf("%w: keep_versions and keep_days must be non-negative", ErrValidation)
	}
	if req.KeepVersions == 0 && req.KeepDays == 0 {
		return store.PruneResult{}, fmt.Errorf("%w: specify keep_versions and/or keep_days", ErrValidation)
	}
	if _, err := s.configs.Get(ctx, configID); err != nil {
		return store.PruneResult{}, mapStoreErr(err)
	}
	ov, err := s.retention.Get(ctx, configID)
	if err != nil {
		return store.PruneResult{}, mapStoreErr(err)
	}
	pol := s.resolvePolicy(ov)

	plan := store.PrunePlan{
		KeepVersions: maxInt(req.KeepVersions, pol.EffectiveVersions, 1),
		KeepDays:     maxInt(req.KeepDays, pol.EffectiveDays),
		DryRun:       req.DryRun,
	}
	res, err := s.secrets.PruneConfigVersions(ctx, configID, plan)
	if err != nil {
		// ErrPruneBlocked must reach the API layer intact (it maps to a distinct
		// 409 message that names the blocking request), so it is passed through
		// rather than folded into ErrConflict by mapStoreErr.
		if errors.Is(err, store.ErrPruneBlocked) {
			return store.PruneResult{}, err
		}
		return store.PruneResult{}, mapStoreErr(err)
	}
	return res, nil
}

// maxInt returns the largest of vs (0 for an empty call).
func maxInt(vs ...int) int {
	out := 0
	for _, v := range vs {
		if v > out {
			out = v
		}
	}
	return out
}
