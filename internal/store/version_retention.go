package store

import "context"

// VersionRetention is a config's optional retention override (migration
// 000043). Both fields are optional; nil means "no opinion from this config,
// inherit the instance-wide floor". Value-free: ids and integers only.
//
// An override can only ever RETAIN MORE than the instance floor — the effective
// floor is the strictest of {instance floor, this override, the prune request}
// — so a per-config row can never weaken an operator's instance-wide guarantee.
type VersionRetention struct {
	MinVersions *int
	MinDays     *int
}

// VersionRetentionRepo stores per-config secret-version retention overrides.
type VersionRetentionRepo struct{ s *Store }

// NewVersionRetentionRepo returns a retention-override repository.
func NewVersionRetentionRepo(s *Store) *VersionRetentionRepo { return &VersionRetentionRepo{s: s} }

// Get returns a config's retention override. A config with no override yields a
// zero VersionRetention (both fields nil) and a nil error — absence is not an
// error, it means "inherit the instance floor".
func (r *VersionRetentionRepo) Get(ctx context.Context, configID string) (VersionRetention, error) {
	var out VersionRetention
	rows, err := r.s.pool.Query(ctx,
		`SELECT min_versions, min_days FROM config_version_retention WHERE config_id = $1::uuid`,
		configID)
	if err != nil {
		return VersionRetention{}, mapError(err)
	}
	defer rows.Close()
	if rows.Next() {
		if err := rows.Scan(&out.MinVersions, &out.MinDays); err != nil {
			return VersionRetention{}, mapError(err)
		}
	}
	return out, mapError(rows.Err())
}

// Set upserts a config's retention override. At least one of minVersions /
// minDays must be non-nil and each non-nil value must be >= 1 (the DB CHECKs
// enforce both too). createdBy may be "" (a service-token actor); stored NULL.
func (r *VersionRetentionRepo) Set(ctx context.Context, configID string, ret VersionRetention, createdBy string) error {
	var by any
	if createdBy != "" {
		by = createdBy
	}
	_, err := r.s.pool.Exec(ctx,
		`INSERT INTO config_version_retention (config_id, min_versions, min_days, created_by)
		 VALUES ($1::uuid, $2, $3, $4)
		 ON CONFLICT (config_id) DO UPDATE
		    SET min_versions = EXCLUDED.min_versions,
		        min_days     = EXCLUDED.min_days,
		        created_by   = EXCLUDED.created_by,
		        updated_at   = now()`,
		configID, ret.MinVersions, ret.MinDays, by)
	return mapError(err)
}

// Clear removes a config's retention override (falling back to the instance
// floor). Clearing an absent override is a no-op.
func (r *VersionRetentionRepo) Clear(ctx context.Context, configID string) error {
	_, err := r.s.pool.Exec(ctx,
		`DELETE FROM config_version_retention WHERE config_id = $1::uuid`, configID)
	return mapError(err)
}
