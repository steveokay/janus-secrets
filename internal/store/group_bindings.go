package store

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
)

// GroupBindingRepo persists role bindings whose subject is a group.
//
// Deliberately separate from RoleBindingRepo: the direct-binding SQL is the
// hottest security path in the system and is left untouched, and each table
// keeps its own pagination cursor.
type GroupBindingRepo struct{ s *Store }

// NewGroupBindingRepo returns a group-binding repository.
func NewGroupBindingRepo(s *Store) *GroupBindingRepo { return &GroupBindingRepo{s: s} }

const groupBindingCols = `id::text, group_id::text, scope_level,
	project_id::text, environment_id::text, role, created_by::text, created_at`

func scanGroupBinding(row interface{ Scan(...any) error }) (*GroupRoleBinding, error) {
	var b GroupRoleBinding
	if err := row.Scan(&b.ID, &b.GroupID, &b.ScopeLevel,
		&b.ProjectID, &b.EnvironmentID, &b.Role, &b.CreatedBy, &b.CreatedAt); err != nil {
		return nil, mapError(err)
	}
	return &b, nil
}

// Create upserts a group's binding at its exact scope, mirroring
// RoleBindingRepo.Create: an existing binding at that scope has its role
// updated in place, otherwise a row is inserted. Wrapped in a tx so the
// read-then-write is atomic; the unique index is the final backstop.
//
// A role of "owner" is rejected by the table's CHECK constraint — group
// bindings top out at admin by design.
func (r *GroupBindingRepo) Create(ctx context.Context, in GroupRoleBindingInput) (*GroupRoleBinding, error) {
	var out *GroupRoleBinding
	err := r.s.withTx(ctx, func(tx pgx.Tx) error {
		var id string
		err := tx.QueryRow(ctx,
			`SELECT id::text FROM group_role_bindings
			 WHERE group_id = $1::uuid AND scope_level = $2
			   AND project_id     IS NOT DISTINCT FROM $3::uuid
			   AND environment_id IS NOT DISTINCT FROM $4::uuid`,
			in.GroupID, in.ScopeLevel, in.ProjectID, in.EnvironmentID).Scan(&id)
		switch {
		case err == nil:
			row := tx.QueryRow(ctx,
				`UPDATE group_role_bindings SET role = $2, created_by = $3::uuid
				 WHERE id = $1::uuid RETURNING `+groupBindingCols,
				id, in.Role, in.CreatedBy)
			out, err = scanGroupBinding(row)
			return err
		case errors.Is(err, pgx.ErrNoRows):
			row := tx.QueryRow(ctx,
				`INSERT INTO group_role_bindings
				   (group_id, scope_level, project_id, environment_id, role, created_by)
				 VALUES ($1::uuid, $2, $3::uuid, $4::uuid, $5, $6::uuid)
				 RETURNING `+groupBindingCols,
				in.GroupID, in.ScopeLevel, in.ProjectID, in.EnvironmentID, in.Role, in.CreatedBy)
			out, err = scanGroupBinding(row)
			return err
		default:
			return mapError(err)
		}
	})
	return out, err
}

// ListForUser returns the bindings a user holds THROUGH GROUP MEMBERSHIP,
// materialised as ordinary RoleBindings so the authz engine sees one longer
// slice and needs no new concepts. Each carries ViaGroupID so the API can
// explain the grant; SubjectUserID is the querying user, and ID belongs to
// group_role_bindings (never feed it to a role_bindings mutation).
//
// This is the authz.GroupBindingStore implementation — it runs on every
// authorization decision, served by group_members_user_idx.
func (r *GroupBindingRepo) ListForUser(ctx context.Context, userID string) ([]*RoleBinding, error) {
	rows, err := r.s.pool.Query(ctx,
		`SELECT b.id::text, b.scope_level, b.project_id::text, b.environment_id::text,
		        b.role, b.created_by::text, b.created_at, b.group_id::text
		   FROM group_role_bindings b
		   JOIN group_members m ON m.group_id = b.group_id
		  WHERE m.user_id = $1::uuid`, userID)
	if err != nil {
		return nil, mapError(err)
	}
	defer rows.Close()
	var out []*RoleBinding
	for rows.Next() {
		b := RoleBinding{SubjectUserID: userID}
		if err := rows.Scan(&b.ID, &b.ScopeLevel, &b.ProjectID, &b.EnvironmentID,
			&b.Role, &b.CreatedBy, &b.CreatedAt, &b.ViaGroupID); err != nil {
			return nil, mapError(err)
		}
		out = append(out, &b)
	}
	return out, mapError(rows.Err())
}

// ListForScopePage returns a page of group bindings at a scope, each joined to
// its group's name and kind for display. Ordering and keyset continuation match
// RoleBindingRepo.ListForScopePage. Unknown levels return ErrNotFound.
func (r *GroupBindingRepo) ListForScopePage(ctx context.Context, level, scopeID string, limit int, after *Cursor) ([]*GroupRoleBinding, error) {
	base := `SELECT b.id::text, b.group_id::text, b.scope_level,
	                b.project_id::text, b.environment_id::text, b.role,
	                b.created_by::text, b.created_at, g.name, g.kind
	           FROM group_role_bindings b JOIN groups g ON g.id = b.group_id
	          WHERE `
	var q string
	var args []any
	switch level {
	case "instance":
		q = base + `b.scope_level = 'instance'`
	case "project":
		q = base + `b.scope_level = 'project' AND b.project_id = $1::uuid`
		args = append(args, scopeID)
	case "environment":
		q = base + `b.scope_level = 'environment' AND b.environment_id = $1::uuid`
		args = append(args, scopeID)
	default:
		return nil, ErrNotFound
	}
	// keyset() is unqualified; these columns are ambiguous across the join.
	if after != nil {
		n := len(args) + 1
		q += keysetOn("b", n)
		args = append(args, after.CreatedAt, after.ID)
	}
	q += " ORDER BY b.created_at DESC, b.id DESC"
	if ls, lArgs := limitSQL(limit, len(args)+1); ls != "" {
		q += ls
		args = append(args, lArgs...)
	}
	rows, err := r.s.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, mapError(err)
	}
	defer rows.Close()
	var out []*GroupRoleBinding
	for rows.Next() {
		var b GroupRoleBinding
		if err := rows.Scan(&b.ID, &b.GroupID, &b.ScopeLevel, &b.ProjectID,
			&b.EnvironmentID, &b.Role, &b.CreatedBy, &b.CreatedAt,
			&b.GroupName, &b.GroupKind); err != nil {
			return nil, mapError(err)
		}
		out = append(out, &b)
	}
	return out, mapError(rows.Err())
}

// DerivedMembersForScope returns every user who reaches this scope THROUGH a
// group, with the group that granted it — the join a scope admin cannot do
// themselves, because listing a group's members needs instance `group:manage`
// while reading a scope's members only needs `member:read` there.
//
// limit bounds the result (the caller passes limit+1 to detect truncation);
// limit<=0 is unbounded. Unknown levels return ErrNotFound, matching
// ListForScopePage.
func (r *GroupBindingRepo) DerivedMembersForScope(ctx context.Context, level, scopeID string, limit int) ([]*DerivedMember, error) {
	base := `SELECT m.user_id::text, b.role, g.id::text, g.name
	           FROM group_role_bindings b
	           JOIN groups g        ON g.id = b.group_id
	           JOIN group_members m ON m.group_id = b.group_id
	          WHERE `
	var q string
	var args []any
	switch level {
	case "instance":
		q = base + `b.scope_level = 'instance'`
	case "project":
		q = base + `b.scope_level = 'project' AND b.project_id = $1::uuid`
		args = append(args, scopeID)
	case "environment":
		q = base + `b.scope_level = 'environment' AND b.environment_id = $1::uuid`
		args = append(args, scopeID)
	default:
		return nil, ErrNotFound
	}
	// Deterministic order so a truncated page is stable rather than arbitrary.
	q += " ORDER BY m.user_id, g.name"
	if ls, lArgs := limitSQL(limit, len(args)+1); ls != "" {
		q += ls
		args = append(args, lArgs...)
	}
	rows, err := r.s.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, mapError(err)
	}
	defer rows.Close()
	var out []*DerivedMember
	for rows.Next() {
		var d DerivedMember
		if err := rows.Scan(&d.UserID, &d.Role, &d.GroupID, &d.GroupName); err != nil {
			return nil, mapError(err)
		}
		out = append(out, &d)
	}
	return out, mapError(rows.Err())
}

// ListForGroup returns every scope a group grants access at ("where does this
// group reach?" — the question an admin asks before deleting one).
func (r *GroupBindingRepo) ListForGroup(ctx context.Context, groupID string) ([]*GroupRoleBinding, error) {
	rows, err := r.s.pool.Query(ctx,
		`SELECT `+groupBindingCols+` FROM group_role_bindings
		  WHERE group_id = $1::uuid ORDER BY created_at DESC, id DESC`, groupID)
	if err != nil {
		return nil, mapError(err)
	}
	defer rows.Close()
	var out []*GroupRoleBinding
	for rows.Next() {
		b, err := scanGroupBinding(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, mapError(rows.Err())
}

// DeleteForGroupScope revokes a group's binding at an exact scope. ErrNotFound
// if none exists.
func (r *GroupBindingRepo) DeleteForGroupScope(ctx context.Context, groupID, level string, projectID, environmentID *string) error {
	return r.s.execAffectingOne(ctx,
		`DELETE FROM group_role_bindings
		 WHERE group_id = $1::uuid AND scope_level = $2
		   AND project_id     IS NOT DISTINCT FROM $3::uuid
		   AND environment_id IS NOT DISTINCT FROM $4::uuid`,
		groupID, level, projectID, environmentID)
}
