package store

import "context"

// MaxAgeSentinel is the reserved key under which a config's DEFAULT max-age is
// stored. The empty string is never a valid secret key (validateKey rejects
// it), so it can never collide with a real per-key override.
const MaxAgeSentinel = ""

// MaxAgeEntry is one advisory max-age policy row: a per-key override (Key is a
// real secret key) or the config default (Key == MaxAgeSentinel).
type MaxAgeEntry struct {
	Key           string
	MaxAgeSeconds int64
}

// MaxAgeRepo stores advisory per-config / per-key max-age policies. Value-free:
// only key names and durations are stored, never secret material.
type MaxAgeRepo struct{ s *Store }

// NewMaxAgeRepo returns a max-age repository.
func NewMaxAgeRepo(s *Store) *MaxAgeRepo { return &MaxAgeRepo{s: s} }

// Set upserts a max-age policy for (configID, key). key may be MaxAgeSentinel
// for the config default. seconds must be > 0 (the DB CHECK enforces this too).
// createdBy may be "" (a service-token actor); stored as NULL.
func (r *MaxAgeRepo) Set(ctx context.Context, configID, key string, seconds int64, createdBy string) error {
	var by any
	if createdBy != "" {
		by = createdBy
	}
	_, err := r.s.pool.Exec(ctx,
		`INSERT INTO config_secret_max_age (config_id, key, max_age_seconds, created_by)
		 VALUES ($1::uuid, $2, $3, $4)
		 ON CONFLICT (config_id, key)
		 DO UPDATE SET max_age_seconds = EXCLUDED.max_age_seconds, created_by = EXCLUDED.created_by`,
		configID, key, seconds, by)
	return mapError(err)
}

// Clear removes a max-age policy for (configID, key). Clearing an absent policy
// is a no-op.
func (r *MaxAgeRepo) Clear(ctx context.Context, configID, key string) error {
	_, err := r.s.pool.Exec(ctx,
		`DELETE FROM config_secret_max_age WHERE config_id=$1::uuid AND key=$2`, configID, key)
	return mapError(err)
}

// List returns a config's max-age entries (per-key overrides and, if set, the
// config default under MaxAgeSentinel), sorted by key.
func (r *MaxAgeRepo) List(ctx context.Context, configID string) ([]MaxAgeEntry, error) {
	rows, err := r.s.pool.Query(ctx,
		`SELECT key, max_age_seconds FROM config_secret_max_age
		  WHERE config_id=$1::uuid ORDER BY key ASC`, configID)
	if err != nil {
		return nil, mapError(err)
	}
	defer rows.Close()
	out := []MaxAgeEntry{}
	for rows.Next() {
		var e MaxAgeEntry
		if err := rows.Scan(&e.Key, &e.MaxAgeSeconds); err != nil {
			return nil, mapError(err)
		}
		out = append(out, e)
	}
	return out, mapError(rows.Err())
}
