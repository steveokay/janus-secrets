package store

import (
	"context"
	"time"
)

// EnvironmentRepo persists environments.
type EnvironmentRepo struct{ s *Store }

// NewEnvironmentRepo returns an environment repository.
func NewEnvironmentRepo(s *Store) *EnvironmentRepo { return &EnvironmentRepo{s: s} }

const envCols = `id::text, project_id::text, slug, name, created_at, updated_at, deleted_at`

func scanEnv(row interface{ Scan(...any) error }) (*Environment, error) {
	var e Environment
	if err := row.Scan(&e.ID, &e.ProjectID, &e.Slug, &e.Name,
		&e.CreatedAt, &e.UpdatedAt, &e.DeletedAt); err != nil {
		return nil, mapError(err)
	}
	return &e, nil
}

// Create inserts an environment under a project.
func (r *EnvironmentRepo) Create(ctx context.Context, projectID, slug, name string) (*Environment, error) {
	row := r.s.pool.QueryRow(ctx,
		`INSERT INTO environments (project_id, slug, name)
		 VALUES ($1::uuid, $2, $3)
		 RETURNING `+envCols,
		projectID, slug, name)
	return scanEnv(row)
}

// Get returns a non-deleted environment by id.
func (r *EnvironmentRepo) Get(ctx context.Context, id string) (*Environment, error) {
	row := r.s.pool.QueryRow(ctx,
		`SELECT `+envCols+` FROM environments WHERE id = $1::uuid AND deleted_at IS NULL`, id)
	return scanEnv(row)
}

// GetIncludingDeleted returns an environment by id even if soft-deleted. Used to
// authorize restore, where the live-only Get would 404 the row being restored.
func (r *EnvironmentRepo) GetIncludingDeleted(ctx context.Context, id string) (*Environment, error) {
	row := r.s.pool.QueryRow(ctx,
		`SELECT `+envCols+` FROM environments WHERE id = $1::uuid`, id)
	return scanEnv(row)
}

// ListDeleted returns soft-deleted environments, most-recently-deleted first.
func (r *EnvironmentRepo) ListDeleted(ctx context.Context) ([]*Environment, error) {
	rows, err := r.s.pool.Query(ctx,
		`SELECT `+envCols+` FROM environments WHERE deleted_at IS NOT NULL ORDER BY deleted_at DESC`)
	if err != nil {
		return nil, mapError(err)
	}
	defer rows.Close()
	var out []*Environment
	for rows.Next() {
		e, err := scanEnv(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, mapError(rows.Err())
}

// GetBySlug returns a non-deleted environment by (project, slug).
func (r *EnvironmentRepo) GetBySlug(ctx context.Context, projectID, slug string) (*Environment, error) {
	row := r.s.pool.QueryRow(ctx,
		`SELECT `+envCols+` FROM environments
		 WHERE project_id = $1::uuid AND slug = $2 AND deleted_at IS NULL`, projectID, slug)
	return scanEnv(row)
}

// ListByProjectPage returns non-deleted environments for a project in
// (created_at DESC, id DESC) order. limit<=0 unbounded; after==nil first page.
func (r *EnvironmentRepo) ListByProjectPage(ctx context.Context, projectID string, limit int, after *Cursor) ([]*Environment, error) {
	q := `SELECT ` + envCols + ` FROM environments WHERE project_id = $1::uuid AND deleted_at IS NULL`
	args := []any{projectID}
	if ks, ksArgs := keyset(after, len(args)+1); ks != "" {
		q += " AND " + ks
		args = append(args, ksArgs...)
	}
	q += " ORDER BY created_at DESC, id DESC"
	if ls, lArgs := limitSQL(limit, len(args)+1); ls != "" {
		q += ls
		args = append(args, lArgs...)
	}
	rows, err := r.s.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, mapError(err)
	}
	defer rows.Close()
	var out []*Environment
	for rows.Next() {
		e, err := scanEnv(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, mapError(rows.Err())
}

// ListByProject returns all non-deleted environments in a project (unbounded;
// kept for existing internal callers).
func (r *EnvironmentRepo) ListByProject(ctx context.Context, projectID string) ([]*Environment, error) {
	return r.ListByProjectPage(ctx, projectID, 0, nil)
}

// SoftDelete marks an environment deleted.
func (r *EnvironmentRepo) SoftDelete(ctx context.Context, id string) error {
	return r.s.execAffectingOne(ctx,
		`UPDATE environments SET deleted_at = now(), updated_at = now()
		 WHERE id = $1::uuid AND deleted_at IS NULL`, id)
}

// Undelete restores a soft-deleted environment.
func (r *EnvironmentRepo) Undelete(ctx context.Context, id string) error {
	return r.s.execAffectingOne(ctx,
		`UPDATE environments SET deleted_at = NULL, updated_at = now()
		 WHERE id = $1::uuid AND deleted_at IS NOT NULL`, id)
}

// Destroy hard-deletes an environment row. Returns ErrNotFound if absent, or
// ErrParentNotFound if a child config still references it.
func (r *EnvironmentRepo) Destroy(ctx context.Context, id string) error {
	return r.s.execAffectingOne(ctx, `DELETE FROM environments WHERE id = $1::uuid`, id)
}

// LastActivity returns, for each given LIVE environment id that has at least one
// config version, the timestamp of its most recent version. Ids with no activity
// — including soft-deleted environments, even if they still have versions — are
// absent. Empty input returns an empty map without querying.
func (r *EnvironmentRepo) LastActivity(ctx context.Context, ids []string) (map[string]time.Time, error) {
	out := make(map[string]time.Time, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	rows, err := r.s.pool.Query(ctx, `
		SELECT e.id::text, max(cv.created_at)
		FROM environments e
		JOIN configs c ON c.environment_id = e.id AND c.deleted_at IS NULL
		JOIN config_versions cv ON cv.config_id = c.id
		WHERE e.deleted_at IS NULL AND e.id::text = ANY($1)
		GROUP BY e.id`, ids)
	if err != nil {
		return nil, mapError(err)
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		var ts time.Time
		if err := rows.Scan(&id, &ts); err != nil {
			return nil, mapError(err)
		}
		out[id] = ts
	}
	return out, mapError(rows.Err())
}
