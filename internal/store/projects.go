package store

import "context"

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

// GetBySlug returns a non-deleted project by slug.
func (r *ProjectRepo) GetBySlug(ctx context.Context, slug string) (*Project, error) {
	row := r.s.pool.QueryRow(ctx,
		`SELECT `+projectCols+` FROM projects WHERE slug = $1 AND deleted_at IS NULL`, slug)
	return scanProject(row)
}

// List returns all non-deleted projects, newest first.
func (r *ProjectRepo) List(ctx context.Context) ([]*Project, error) {
	rows, err := r.s.pool.Query(ctx,
		`SELECT `+projectCols+` FROM projects WHERE deleted_at IS NULL ORDER BY created_at DESC`)
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
