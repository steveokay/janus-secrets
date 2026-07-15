package store

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
)

// RoleBindingRepo persists RBAC role bindings.
type RoleBindingRepo struct{ s *Store }

// NewRoleBindingRepo returns a role-binding repository.
func NewRoleBindingRepo(s *Store) *RoleBindingRepo { return &RoleBindingRepo{s: s} }

const roleBindingCols = `id::text, subject_user_id::text, scope_level,
	project_id::text, environment_id::text, role, created_by::text, created_at`

func scanRoleBinding(row interface{ Scan(...any) error }) (*RoleBinding, error) {
	var b RoleBinding
	if err := row.Scan(&b.ID, &b.SubjectUserID, &b.ScopeLevel,
		&b.ProjectID, &b.EnvironmentID, &b.Role, &b.CreatedBy, &b.CreatedAt); err != nil {
		return nil, mapError(err)
	}
	return &b, nil
}

// Create upserts a binding on its exact scope: if the subject already has a
// binding at that scope the role is updated in place, otherwise a row is
// inserted. Wrapped in a tx so the read-then-write is atomic; the unique index
// is the final backstop against a concurrent double-insert.
func (r *RoleBindingRepo) Create(ctx context.Context, in RoleBindingInput) (*RoleBinding, error) {
	var out *RoleBinding
	err := r.s.withTx(ctx, func(tx pgx.Tx) error {
		var id string
		err := tx.QueryRow(ctx,
			`SELECT id::text FROM role_bindings
			 WHERE subject_user_id = $1::uuid AND scope_level = $2
			   AND project_id     IS NOT DISTINCT FROM $3::uuid
			   AND environment_id IS NOT DISTINCT FROM $4::uuid`,
			in.SubjectUserID, in.ScopeLevel, in.ProjectID, in.EnvironmentID).Scan(&id)
		switch {
		case err == nil:
			row := tx.QueryRow(ctx,
				`UPDATE role_bindings SET role = $2, created_by = $3::uuid
				 WHERE id = $1::uuid RETURNING `+roleBindingCols,
				id, in.Role, in.CreatedBy)
			out, err = scanRoleBinding(row)
			return err
		case errors.Is(err, pgx.ErrNoRows):
			row := tx.QueryRow(ctx,
				`INSERT INTO role_bindings
				   (subject_user_id, scope_level, project_id, environment_id, role, created_by)
				 VALUES ($1::uuid, $2, $3::uuid, $4::uuid, $5, $6::uuid)
				 RETURNING `+roleBindingCols,
				in.SubjectUserID, in.ScopeLevel, in.ProjectID, in.EnvironmentID, in.Role, in.CreatedBy)
			out, err = scanRoleBinding(row)
			return err
		default:
			return mapError(err)
		}
	})
	return out, err
}

// ListForUser returns every binding held by a user (for Can() resolution).
func (r *RoleBindingRepo) ListForUser(ctx context.Context, userID string) ([]*RoleBinding, error) {
	rows, err := r.s.pool.Query(ctx,
		`SELECT `+roleBindingCols+` FROM role_bindings WHERE subject_user_id = $1::uuid`, userID)
	if err != nil {
		return nil, mapError(err)
	}
	defer rows.Close()
	var out []*RoleBinding
	for rows.Next() {
		b, err := scanRoleBinding(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, mapError(rows.Err())
}

// ListForScope returns the bindings at a scope (members list). scopeID is the
// project_id or environment_id; it is ignored for the instance level. It is the
// unbounded delegate of ListForScopePage.
func (r *RoleBindingRepo) ListForScope(ctx context.Context, level, scopeID string) ([]*RoleBinding, error) {
	return r.ListForScopePage(ctx, level, scopeID, 0, nil)
}

// ListForScopePage returns the bindings at a scope in (created_at DESC, id DESC)
// order, with keyset continuation from after (nil = first page) and a LIMIT when
// limit>0 (limit<=0 = unbounded, the legacy ListForScope path). Unknown levels
// return ErrNotFound, matching ListForScope's original default behavior.
func (r *RoleBindingRepo) ListForScopePage(ctx context.Context, level, scopeID string, limit int, after *Cursor) ([]*RoleBinding, error) {
	var q string
	var args []any
	switch level {
	case "instance":
		q = `SELECT ` + roleBindingCols + ` FROM role_bindings WHERE scope_level = 'instance'`
	case "project":
		q = `SELECT ` + roleBindingCols + ` FROM role_bindings WHERE scope_level = 'project' AND project_id = $1::uuid`
		args = append(args, scopeID)
	case "environment":
		q = `SELECT ` + roleBindingCols + ` FROM role_bindings WHERE scope_level = 'environment' AND environment_id = $1::uuid`
		args = append(args, scopeID)
	default:
		return nil, ErrNotFound
	}
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
	var out []*RoleBinding
	for rows.Next() {
		b, err := scanRoleBinding(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, mapError(rows.Err())
}

// DeleteForSubjectScope revokes a subject's binding at an exact scope.
// ErrNotFound if none exists.
func (r *RoleBindingRepo) DeleteForSubjectScope(ctx context.Context, subjectUserID, level string, projectID, environmentID *string) error {
	return r.s.execAffectingOne(ctx,
		`DELETE FROM role_bindings
		 WHERE subject_user_id = $1::uuid AND scope_level = $2
		   AND project_id     IS NOT DISTINCT FROM $3::uuid
		   AND environment_id IS NOT DISTINCT FROM $4::uuid`,
		subjectUserID, level, projectID, environmentID)
}

// CountInstanceOwners counts instance-level owner bindings (never-lock-out guard).
func (r *RoleBindingRepo) CountInstanceOwners(ctx context.Context) (int, error) {
	var n int
	err := r.s.pool.QueryRow(ctx,
		`SELECT count(*) FROM role_bindings WHERE scope_level = 'instance' AND role = 'owner'`).Scan(&n)
	return n, mapError(err)
}
