package store

import "context"

// ConfigRepo persists configs.
type ConfigRepo struct{ s *Store }

// NewConfigRepo returns a config repository.
func NewConfigRepo(s *Store) *ConfigRepo { return &ConfigRepo{s: s} }

const configCols = `id::text, environment_id::text, name, inherits_from::text, created_at, updated_at, deleted_at`

func scanConfig(row interface{ Scan(...any) error }) (*Config, error) {
	var c Config
	if err := row.Scan(&c.ID, &c.EnvironmentID, &c.Name, &c.InheritsFrom,
		&c.CreatedAt, &c.UpdatedAt, &c.DeletedAt); err != nil {
		return nil, mapError(err)
	}
	return &c, nil
}

// Create inserts a config under an environment. inheritsFrom may be nil.
func (r *ConfigRepo) Create(ctx context.Context, environmentID, name string, inheritsFrom *string) (*Config, error) {
	row := r.s.pool.QueryRow(ctx,
		`INSERT INTO configs (environment_id, name, inherits_from)
		 VALUES ($1::uuid, $2, $3::uuid)
		 RETURNING `+configCols,
		environmentID, name, inheritsFrom)
	return scanConfig(row)
}

// Get returns a non-deleted config by id.
func (r *ConfigRepo) Get(ctx context.Context, id string) (*Config, error) {
	row := r.s.pool.QueryRow(ctx,
		`SELECT `+configCols+` FROM configs WHERE id = $1::uuid AND deleted_at IS NULL`, id)
	return scanConfig(row)
}

// GetByName returns a non-deleted config by (environment, name).
func (r *ConfigRepo) GetByName(ctx context.Context, environmentID, name string) (*Config, error) {
	row := r.s.pool.QueryRow(ctx,
		`SELECT `+configCols+` FROM configs
		 WHERE environment_id = $1::uuid AND name = $2 AND deleted_at IS NULL`, environmentID, name)
	return scanConfig(row)
}

// ListByEnvironment returns all non-deleted configs in an environment.
func (r *ConfigRepo) ListByEnvironment(ctx context.Context, environmentID string) ([]*Config, error) {
	rows, err := r.s.pool.Query(ctx,
		`SELECT `+configCols+` FROM configs
		 WHERE environment_id = $1::uuid AND deleted_at IS NULL ORDER BY created_at DESC`, environmentID)
	if err != nil {
		return nil, mapError(err)
	}
	defer rows.Close()
	var out []*Config
	for rows.Next() {
		c, err := scanConfig(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, mapError(rows.Err())
}

// SoftDelete marks a config deleted.
func (r *ConfigRepo) SoftDelete(ctx context.Context, id string) error {
	return r.s.execAffectingOne(ctx,
		`UPDATE configs SET deleted_at = now(), updated_at = now()
		 WHERE id = $1::uuid AND deleted_at IS NULL`, id)
}

// Undelete restores a soft-deleted config.
func (r *ConfigRepo) Undelete(ctx context.Context, id string) error {
	return r.s.execAffectingOne(ctx,
		`UPDATE configs SET deleted_at = NULL, updated_at = now()
		 WHERE id = $1::uuid AND deleted_at IS NOT NULL`, id)
}

// Destroy hard-deletes a config row. Returns ErrNotFound if absent, or
// ErrParentNotFound if config versions still reference it.
func (r *ConfigRepo) Destroy(ctx context.Context, id string) error {
	return r.s.execAffectingOne(ctx, `DELETE FROM configs WHERE id = $1::uuid`, id)
}
