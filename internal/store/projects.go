package store

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
)

// ProjectRepo persists projects.
type ProjectRepo struct{ s *Store }

// NewProjectRepo returns a project repository.
func NewProjectRepo(s *Store) *ProjectRepo { return &ProjectRepo{s: s} }

const projectCols = `id::text, slug, name, wrapped_kek, kek_version, created_at, updated_at, deleted_at`

func scanProject(row interface{ Scan(...any) error }) (*Project, error) {
	var p Project
	if err := row.Scan(&p.ID, &p.Slug, &p.Name, &p.WrappedKEK, &p.KEKVersion,
		&p.CreatedAt, &p.UpdatedAt, &p.DeletedAt); err != nil {
		return nil, mapError(err)
	}
	return &p, nil
}

// Create inserts a project with the caller-supplied id and returns the stored
// row. The id is generated via Store.NewID so callers can bind the project
// KEK's AAD to it before wrapping.
func (r *ProjectRepo) Create(ctx context.Context, id, slug, name string, wrappedKEK []byte, kekVersion int) (*Project, error) {
	row := r.s.pool.QueryRow(ctx,
		`INSERT INTO projects (id, slug, name, wrapped_kek, kek_version)
		 VALUES ($1::uuid, $2, $3, $4, $5)
		 RETURNING `+projectCols,
		id, slug, name, wrappedKEK, kekVersion)
	return scanProject(row)
}

// Get returns a non-deleted project by id.
func (r *ProjectRepo) Get(ctx context.Context, id string) (*Project, error) {
	row := r.s.pool.QueryRow(ctx,
		`SELECT `+projectCols+` FROM projects WHERE id = $1::uuid AND deleted_at IS NULL`, id)
	return scanProject(row)
}

// GetIncludingDeleted returns a project by id regardless of soft-delete state.
// Used to resolve parent-name labels for the Trash view, where a project may
// itself be soft-deleted. ErrNotFound only if the row does not exist at all.
func (r *ProjectRepo) GetIncludingDeleted(ctx context.Context, id string) (*Project, error) {
	row := r.s.pool.QueryRow(ctx,
		`SELECT `+projectCols+` FROM projects WHERE id = $1::uuid`, id)
	return scanProject(row)
}

// ListDeleted returns soft-deleted projects, most-recently-deleted first.
func (r *ProjectRepo) ListDeleted(ctx context.Context) ([]*Project, error) {
	rows, err := r.s.pool.Query(ctx,
		`SELECT `+projectCols+` FROM projects WHERE deleted_at IS NOT NULL ORDER BY deleted_at DESC`)
	if err != nil {
		return nil, mapError(err)
	}
	defer rows.Close()
	var out []*Project
	for rows.Next() {
		p, err := scanProject(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, mapError(rows.Err())
}

// GetBySlug returns a non-deleted project by slug.
func (r *ProjectRepo) GetBySlug(ctx context.Context, slug string) (*Project, error) {
	row := r.s.pool.QueryRow(ctx,
		`SELECT `+projectCols+` FROM projects WHERE slug = $1 AND deleted_at IS NULL`, slug)
	return scanProject(row)
}

// ListPage returns non-deleted projects in (created_at DESC, id DESC) order.
// limit<=0 is unbounded; after==nil is the first page.
func (r *ProjectRepo) ListPage(ctx context.Context, limit int, after *Cursor) ([]*Project, error) {
	q := `SELECT ` + projectCols + ` FROM projects WHERE deleted_at IS NULL`
	var args []any
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
	var out []*Project
	for rows.Next() {
		p, err := scanProject(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, mapError(rows.Err())
}

// List returns all non-deleted projects, newest first (unbounded; kept for
// existing internal callers).
func (r *ProjectRepo) List(ctx context.Context) ([]*Project, error) {
	return r.ListPage(ctx, 0, nil)
}

// UpdateName sets a project's display name. Slug is immutable. Returns
// ErrNotFound if the project does not exist or is soft-deleted.
func (r *ProjectRepo) UpdateName(ctx context.Context, id, name string) error {
	return r.s.execAffectingOne(ctx,
		`UPDATE projects SET name = $2, updated_at = now()
		 WHERE id = $1::uuid AND deleted_at IS NULL`, id, name)
}

// SoftDelete marks a project deleted. Returns ErrNotFound if it was already
// deleted or does not exist.
func (r *ProjectRepo) SoftDelete(ctx context.Context, id string) error {
	return r.s.execAffectingOne(ctx,
		`UPDATE projects SET deleted_at = now(), updated_at = now()
		 WHERE id = $1::uuid AND deleted_at IS NULL`, id)
}

// Undelete restores a soft-deleted project.
func (r *ProjectRepo) Undelete(ctx context.Context, id string) error {
	return r.s.execAffectingOne(ctx,
		`UPDATE projects SET deleted_at = NULL, updated_at = now()
		 WHERE id = $1::uuid AND deleted_at IS NOT NULL`, id)
}

// Destroy hard-deletes a project row regardless of its soft-delete state; it is
// the explicit, irreversible counterpart to SoftDelete. Any "must be
// soft-deleted first" policy is enforced above the store. Returns ErrNotFound
// if the row does not exist, or ErrParentNotFound if a child row still
// references it (NO ACTION foreign keys).
func (r *ProjectRepo) Destroy(ctx context.Context, id string) error {
	return r.s.execAffectingOne(ctx, `DELETE FROM projects WHERE id = $1::uuid`, id)
}

// LastActivity returns, for each given project id that has at least one config
// version, the timestamp of its most recent version (max config_versions.created_at
// across the project's live environments and configs). Ids with no activity are
// absent from the map. Empty input returns an empty map without querying.
func (r *ProjectRepo) LastActivity(ctx context.Context, ids []string) (map[string]time.Time, error) {
	out := make(map[string]time.Time, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	rows, err := r.s.pool.Query(ctx, `
		SELECT p.id::text, max(cv.created_at)
		FROM projects p
		JOIN environments e ON e.project_id = p.id AND e.deleted_at IS NULL
		JOIN configs c ON c.environment_id = e.id AND c.deleted_at IS NULL
		JOIN config_versions cv ON cv.config_id = c.id
		WHERE p.id::text = ANY($1)
		GROUP BY p.id`, ids)
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

// RotateKEK atomically installs a new KEK version for a live project. It locks
// the project row, preserves the current (version, wrapped_kek) into
// project_kek_versions, then calls wrapNew(oldVersion) to obtain the newly
// wrapped KEK (the caller does the keyring wrap; no DB access in the closure)
// and updates the project to version+1. Returns the new version, or ErrNotFound
// if the project does not exist or is soft-deleted.
func (r *ProjectRepo) RotateKEK(ctx context.Context, id string, wrapNew func(oldVersion int) (newWrapped []byte, err error)) (int, error) {
	var newVersion int
	err := r.s.withTx(ctx, func(tx pgx.Tx) error {
		var oldVersion int
		var oldWrapped []byte
		row := tx.QueryRow(ctx,
			`SELECT kek_version, wrapped_kek FROM projects
			  WHERE id=$1::uuid AND deleted_at IS NULL FOR UPDATE`, id)
		if err := row.Scan(&oldVersion, &oldWrapped); err != nil {
			return mapError(err) // pgx.ErrNoRows -> ErrNotFound
		}
		if _, err := tx.Exec(ctx,
			`INSERT INTO project_kek_versions (project_id, version, wrapped_kek)
			 VALUES ($1::uuid, $2, $3)`, id, oldVersion, oldWrapped); err != nil {
			return mapError(err)
		}
		newWrapped, err := wrapNew(oldVersion)
		if err != nil {
			return err
		}
		newVersion = oldVersion + 1
		tag, err := tx.Exec(ctx,
			`UPDATE projects SET wrapped_kek=$2, kek_version=$3, updated_at=now() WHERE id=$1::uuid`,
			id, newWrapped, newVersion)
		if err != nil {
			return mapError(err)
		}
		if tag.RowsAffected() != 1 {
			return ErrNotFound
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	return newVersion, nil
}
