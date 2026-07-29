package store

import (
	"context"
	"strconv"
)

// AccessScopeFilter names the exact scopes an access review is allowed to look
// at, and optionally narrows the answer to one subject.
//
// It exists so a cross-scope question ("who can write prod?", "what can this
// person reach?") is ONE query per binding source rather than one per scope.
// The alternative — looping over scopes and asking the engine each time —
// re-queries direct bindings, group bindings and break-glass grants on every
// iteration; on an instance with hundreds of projects that is thousands of
// queries to render one page. Same reasoning as AllowedProjects.
//
// The filter is also the authorization boundary. The caller computes it from
// what they may actually see, and the SQL never widens it: an empty filter
// selects nothing, so a principal with no bindings gets an empty answer rather
// than a leak. Environment scopes are named by their own ids (the caller
// already lists the environments to label the grid), so no join is needed and
// there is no path by which an environment outside the filter can be returned.
type AccessScopeFilter struct {
	// SubjectUserID limits the result to one user; "" means every user.
	SubjectUserID string
	// Instance includes instance-scoped bindings.
	Instance bool
	// ProjectIDs / EnvIDs are the project- and environment-scoped keys in view.
	ProjectIDs []string
	EnvIDs     []string
}

// Empty reports whether the filter selects no scope at all, in which case the
// repositories skip the query entirely and return nothing. Deny-by-default is
// expressed here once rather than at every call site.
func (f AccessScopeFilter) Empty() bool {
	return !f.Instance && len(f.ProjectIDs) == 0 && len(f.EnvIDs) == 0
}

// scopeWhere renders the filter as a WHERE clause over an aliased bindings
// table, plus its arguments. Parameterized throughout; the ids are never
// interpolated.
func (f AccessScopeFilter) scopeWhere(alias string) (string, []any) {
	q := "(" + alias + ".scope_level = 'instance' AND $1::bool)" +
		" OR (" + alias + ".scope_level = 'project' AND " + alias + ".project_id::text = ANY($2))" +
		" OR (" + alias + ".scope_level = 'environment' AND " + alias + ".environment_id::text = ANY($3))"
	// pgx encodes a nil []string as NULL, and `x = ANY(NULL)` is NULL (not
	// false) — harmless inside an OR, but an empty slice is clearer and matches
	// what the caller means.
	projects := f.ProjectIDs
	if projects == nil {
		projects = []string{}
	}
	envs := f.EnvIDs
	if envs == nil {
		envs = []string{}
	}
	return "(" + q + ")", []any{f.Instance, projects, envs}
}

// ListForScopes returns every DIRECT role binding at the filtered scopes,
// newest first, bounded by limit (<=0 is unbounded). The caller passes limit+1
// to detect truncation: an access review that silently dropped rows would
// understate who has access, which is the one failure this is meant to prevent.
func (r *RoleBindingRepo) ListForScopes(ctx context.Context, f AccessScopeFilter, limit int) ([]*RoleBinding, error) {
	if f.Empty() {
		return nil, nil
	}
	where, args := f.scopeWhere("b")
	q := `SELECT b.id::text, b.subject_user_id::text, b.scope_level,
	             b.project_id::text, b.environment_id::text, b.role,
	             b.created_by::text, b.created_at
	        FROM role_bindings b
	       WHERE ` + where
	if f.SubjectUserID != "" {
		q += " AND b.subject_user_id = $" + strconv.Itoa(len(args)+1) + "::uuid"
		args = append(args, f.SubjectUserID)
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

// DerivedForScopes returns every binding held THROUGH GROUP MEMBERSHIP at the
// filtered scopes, materialised as ordinary RoleBindings carrying ViaGroupID
// and ViaGroupName. One row per (user, group binding) pair — a user in two
// granting groups produces two, and the caller unions them exactly as it unions
// two direct bindings.
//
// The returned ID belongs to group_role_bindings, NOT role_bindings; it is
// provenance, never something to feed to a role_bindings mutation. That is
// precisely why revoke-all can act on ListForScopes' output and must not act on
// this one.
func (r *GroupBindingRepo) DerivedForScopes(ctx context.Context, f AccessScopeFilter, limit int) ([]*RoleBinding, error) {
	if f.Empty() {
		return nil, nil
	}
	where, args := f.scopeWhere("b")
	q := `SELECT b.id::text, m.user_id::text, b.scope_level,
	             b.project_id::text, b.environment_id::text, b.role,
	             b.created_by::text, b.created_at, g.id::text, g.name
	        FROM group_role_bindings b
	        JOIN groups g        ON g.id = b.group_id
	        JOIN group_members m ON m.group_id = b.group_id
	       WHERE ` + where
	if f.SubjectUserID != "" {
		q += " AND m.user_id = $" + strconv.Itoa(len(args)+1) + "::uuid"
		args = append(args, f.SubjectUserID)
	}
	// Deterministic order so a truncated page is stable rather than arbitrary.
	q += " ORDER BY m.user_id, g.name, b.id"
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
		var b RoleBinding
		var gid string
		if err := rows.Scan(&b.ID, &b.SubjectUserID, &b.ScopeLevel,
			&b.ProjectID, &b.EnvironmentID, &b.Role, &b.CreatedBy, &b.CreatedAt,
			&gid, &b.ViaGroupName); err != nil {
			return nil, mapError(err)
		}
		b.ViaGroupID = &gid
		out = append(out, &b)
	}
	return out, mapError(rows.Err())
}

// ListForProjects returns the non-deleted environments of the given projects in
// ONE query — the columns of a cross-scope grid, without a per-project round
// trip. Bounded by limit (<=0 unbounded); empty input queries nothing.
func (r *EnvironmentRepo) ListForProjects(ctx context.Context, projectIDs []string, limit int) ([]*Environment, error) {
	if len(projectIDs) == 0 {
		return nil, nil
	}
	q := `SELECT ` + envCols + ` FROM environments
	       WHERE project_id::text = ANY($1) AND deleted_at IS NULL
	       ORDER BY project_id, slug`
	args := []any{projectIDs}
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
