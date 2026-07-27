package store

import (
	"context"

	"github.com/jackc/pgx/v5"
)

// GroupRepo persists groups and their membership.
type GroupRepo struct{ s *Store }

// NewGroupRepo returns a group repository.
func NewGroupRepo(s *Store) *GroupRepo { return &GroupRepo{s: s} }

const groupCols = `id::text, name, kind, claim_value, description, can_create_projects, created_by::text, created_at`

func scanGroup(row interface{ Scan(...any) error }) (*Group, error) {
	var g Group
	if err := row.Scan(&g.ID, &g.Name, &g.Kind, &g.ClaimValue, &g.Description,
		&g.CanCreateProjects, &g.CreatedBy, &g.CreatedAt); err != nil {
		return nil, mapError(err)
	}
	return &g, nil
}

// Create inserts a group. The caller must have validated kind/claim_value
// pairing; the groups_claim_shape CHECK is the backstop. A duplicate name or
// claim value surfaces as ErrConflict.
func (r *GroupRepo) Create(ctx context.Context, in GroupInput) (*Group, error) {
	row := r.s.pool.QueryRow(ctx,
		`INSERT INTO groups (name, kind, claim_value, description, can_create_projects, created_by)
		 VALUES ($1, $2, $3, $4, $5, $6::uuid)
		 RETURNING `+groupCols,
		in.Name, in.Kind, in.ClaimValue, in.Description, in.CanCreateProjects, in.CreatedBy)
	return scanGroup(row)
}

// Get returns a group by id, with display counts. ErrNotFound if absent.
func (r *GroupRepo) Get(ctx context.Context, id string) (*Group, error) {
	row := r.s.pool.QueryRow(ctx,
		`SELECT `+groupCols+`,
		   (SELECT count(*) FROM group_members       m WHERE m.group_id = g.id),
		   (SELECT count(*) FROM group_role_bindings b WHERE b.group_id = g.id)
		 FROM groups g WHERE id = $1::uuid`, id)
	var g Group
	if err := row.Scan(&g.ID, &g.Name, &g.Kind, &g.ClaimValue, &g.Description,
		&g.CanCreateProjects, &g.CreatedBy, &g.CreatedAt, &g.MemberCount, &g.BindingCount); err != nil {
		return nil, mapError(err)
	}
	return &g, nil
}

// GetByName returns a group by its unique name (the CLI binds by name).
func (r *GroupRepo) GetByName(ctx context.Context, name string) (*Group, error) {
	row := r.s.pool.QueryRow(ctx, `SELECT `+groupCols+` FROM groups WHERE name = $1`, name)
	return scanGroup(row)
}

// List returns a page of groups in (created_at DESC, id DESC) order with keyset
// continuation, each carrying its member and binding counts for display.
func (r *GroupRepo) List(ctx context.Context, limit int, after *Cursor) ([]*Group, error) {
	q := `SELECT ` + groupCols + `,
	        (SELECT count(*) FROM group_members       m WHERE m.group_id = g.id),
	        (SELECT count(*) FROM group_role_bindings b WHERE b.group_id = g.id)
	      FROM groups g`
	var args []any
	if ks, ksArgs := keyset(after, 1); ks != "" {
		q += " WHERE " + ks
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
	var out []*Group
	for rows.Next() {
		var g Group
		if err := rows.Scan(&g.ID, &g.Name, &g.Kind, &g.ClaimValue, &g.Description,
			&g.CanCreateProjects, &g.CreatedBy, &g.CreatedAt, &g.MemberCount, &g.BindingCount); err != nil {
			return nil, mapError(err)
		}
		out = append(out, &g)
	}
	return out, mapError(rows.Err())
}

// Delete removes a group; members and bindings cascade. ErrNotFound if absent.
func (r *GroupRepo) Delete(ctx context.Context, id string) error {
	return r.s.execAffectingOne(ctx, `DELETE FROM groups WHERE id = $1::uuid`, id)
}

// AddMember adds a user to a LOCAL group. kind is passed through to the
// composite FK so the database — not this function — is what refuses a member
// on an OIDC group: the FK (group_id, 'local') simply has no matching row.
// Re-adding an existing member is a no-op rather than an error.
func (r *GroupRepo) AddMember(ctx context.Context, groupID, kind, userID string, createdBy *string) error {
	_, err := r.s.pool.Exec(ctx,
		`INSERT INTO group_members (group_id, group_kind, user_id, created_by)
		 VALUES ($1::uuid, $2, $3::uuid, $4::uuid)
		 ON CONFLICT (group_id, user_id) DO NOTHING`,
		groupID, kind, userID, createdBy)
	return mapError(err)
}

// RemoveMember drops a membership row. ErrNotFound if there was none.
func (r *GroupRepo) RemoveMember(ctx context.Context, groupID, userID string) error {
	return r.s.execAffectingOne(ctx,
		`DELETE FROM group_members WHERE group_id = $1::uuid AND user_id = $2::uuid`,
		groupID, userID)
}

// ListMembers returns a page of a group's members.
func (r *GroupRepo) ListMembers(ctx context.Context, groupID string, limit int, after *Cursor) ([]*GroupMember, error) {
	q := `SELECT group_id::text, user_id::text, created_by::text, created_at
	      FROM group_members WHERE group_id = $1::uuid`
	args := []any{groupID}
	// group_members is keyed (group_id, user_id) and has no id column, so the
	// shared keyset() helper — which hardcodes (created_at, id) — does not apply.
	// The cursor tie-breaks on user_id instead, carried in Cursor.ID.
	if after != nil {
		q += " AND (created_at, user_id) < ($2, $3::uuid)"
		args = append(args, after.CreatedAt, after.ID)
	}
	q += " ORDER BY created_at DESC, user_id DESC"
	if ls, lArgs := limitSQL(limit, len(args)+1); ls != "" {
		q += ls
		args = append(args, lArgs...)
	}
	rows, err := r.s.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, mapError(err)
	}
	defer rows.Close()
	var out []*GroupMember
	for rows.Next() {
		var m GroupMember
		if err := rows.Scan(&m.GroupID, &m.UserID, &m.CreatedBy, &m.CreatedAt); err != nil {
			return nil, mapError(err)
		}
		out = append(out, &m)
	}
	return out, mapError(rows.Err())
}

// ListForUser returns the groups a user belongs to (the "why do I have access"
// view, and the offboarding answer).
func (r *GroupRepo) ListForUser(ctx context.Context, userID string) ([]*Group, error) {
	// Columns must be alias-qualified here: group_members also has created_by
	// and created_at, so the bare list is ambiguous across the join.
	rows, err := r.s.pool.Query(ctx,
		`SELECT g.id::text, g.name, g.kind, g.claim_value, g.description,
		        g.can_create_projects, g.created_by::text, g.created_at
		   FROM groups g
		   JOIN group_members m ON m.group_id = g.id
		  WHERE m.user_id = $1::uuid
		  ORDER BY g.name`, userID)
	if err != nil {
		return nil, mapError(err)
	}
	defer rows.Close()
	var out []*Group
	for rows.Next() {
		g, err := scanGroup(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, mapError(rows.Err())
}

// SyncOIDCMembership replaces a user's OIDC-group membership with exactly the
// groups matching claimValues, in ONE transaction — there is never a window in
// which the user has no groups. Claim values with no configured group match
// nothing and grant nothing (groups are never auto-created: a claim value only
// matters once an admin has created a group for it).
//
// Only rows whose group is kind='oidc' are touched, so a user's local-group
// membership is never disturbed by a login.
func (r *GroupRepo) SyncOIDCMembership(ctx context.Context, userID string, claimValues []string) (GroupSyncResult, error) {
	var res GroupSyncResult
	// Normalise: an empty slice must still be a non-nil array for = ANY($2).
	if claimValues == nil {
		claimValues = []string{}
	}
	err := r.s.withTx(ctx, func(tx pgx.Tx) error {
		removed, err := collectNames(tx.Query(ctx,
			`DELETE FROM group_members m
			   USING groups g
			  WHERE m.group_id = g.id
			    AND m.user_id = $1::uuid
			    AND m.group_kind = 'oidc'
			    AND NOT (g.claim_value = ANY($2::text[]))
			  RETURNING g.name`, userID, claimValues))
		if err != nil {
			return err
		}
		added, err := collectNames(tx.Query(ctx,
			`WITH ins AS (
			   INSERT INTO group_members (group_id, group_kind, user_id)
			   SELECT g.id, g.kind, $1::uuid
			     FROM groups g
			    WHERE g.kind = 'oidc' AND g.claim_value = ANY($2::text[])
			   ON CONFLICT (group_id, user_id) DO NOTHING
			   RETURNING group_id
			 )
			 SELECT g.name FROM groups g JOIN ins ON ins.group_id = g.id`, userID, claimValues))
		if err != nil {
			return err
		}
		res.Added, res.Removed = added, removed
		return nil
	})
	return res, err
}

// collectNames drains a single-text-column result set.
func collectNames(rows pgx.Rows, err error) ([]string, error) {
	if err != nil {
		return nil, mapError(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			return nil, mapError(err)
		}
		out = append(out, n)
	}
	return out, mapError(rows.Err())
}

// SetCanCreateProjects toggles a group's delegated project-creation capability.
func (r *GroupRepo) SetCanCreateProjects(ctx context.Context, id string, enabled bool) error {
	return r.s.execAffectingOne(ctx,
		`UPDATE groups SET can_create_projects = $2 WHERE id = $1::uuid`, id, enabled)
}

// CreatorGroupsForUser returns the groups a user belongs to that may create
// projects. Empty means the user has no delegated creation capability at all —
// the deny-by-default answer.
func (r *GroupRepo) CreatorGroupsForUser(ctx context.Context, userID string) ([]*Group, error) {
	rows, err := r.s.pool.Query(ctx,
		`SELECT g.id::text, g.name, g.kind, g.claim_value, g.description,
		        g.can_create_projects, g.created_by::text, g.created_at
		   FROM groups g
		   JOIN group_members m ON m.group_id = g.id
		  WHERE m.user_id = $1::uuid AND g.can_create_projects
		  ORDER BY g.name`, userID)
	if err != nil {
		return nil, mapError(err)
	}
	defer rows.Close()
	var out []*Group
	for rows.Next() {
		g, err := scanGroup(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, mapError(rows.Err())
}
