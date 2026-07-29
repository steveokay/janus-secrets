package authz

import (
	"context"

	"github.com/steveokay/janus-secrets/internal/auth"
	"github.com/steveokay/janus-secrets/internal/store"
)

// Member is a user's role at a scope (for the members list).
type Member struct {
	UserID string `json:"user_id"`
	Role   string `json:"role"`
}

// Grant creates or updates a binding.
func (e *Engine) Grant(ctx context.Context, in store.RoleBindingInput) error {
	_, err := e.bindings.Create(ctx, in)
	return err
}

// Revoke removes a subject's binding at a scope.
func (e *Engine) Revoke(ctx context.Context, subjectUserID, level string, projectID, environmentID *string) error {
	return e.bindings.DeleteForSubjectScope(ctx, subjectUserID, level, projectID, environmentID)
}

// ListMembers returns the bindings at a scope. It is the unbounded delegate of
// ListMembersPage.
func (e *Engine) ListMembers(ctx context.Context, level, scopeID string) ([]Member, error) {
	members, _, err := e.ListMembersPage(ctx, level, scopeID, 0, nil)
	return members, err
}

// ListMembersPage returns a page of members at a scope plus the keyset cursor
// for the next page (nil on the last page). limit<=0 is unbounded (the legacy
// ListMembers path). Members carry only user_id + role — never secret material.
func (e *Engine) ListMembersPage(ctx context.Context, level, scopeID string, limit int, after *store.Cursor) ([]Member, *store.Cursor, error) {
	bs, err := e.bindings.ListForScopePage(ctx, level, scopeID, limit, after)
	if err != nil {
		return nil, nil, err
	}
	out := make([]Member, 0, len(bs))
	for _, b := range bs {
		out = append(out, Member{UserID: b.SubjectUserID, Role: b.Role})
	}
	var next *store.Cursor
	if limit > 0 && len(bs) == limit {
		last := bs[len(bs)-1]
		next = &store.Cursor{CreatedAt: last.CreatedAt, ID: last.ID}
	}
	return out, next, nil
}

// CountInstanceOwners exposes the never-lock-out counter.
func (e *Engine) CountInstanceOwners(ctx context.Context) (int, error) {
	return e.bindings.CountInstanceOwners(ctx)
}

// EffectiveRole returns the highest-ranked role the user holds that applies to
// res, INCLUDING any active break-glass grant on that scope (max of bound role
// and active grant role), or "" if none.
func (e *Engine) EffectiveRole(ctx context.Context, userID string, res Resource) (Role, error) {
	best, err := e.BoundRole(ctx, userID, res)
	if err != nil {
		return "", err
	}
	if e.grants != nil {
		now := e.now()
		gs, err := e.grants.ListActiveForUser(ctx, userID, now)
		if err != nil {
			return "", err
		}
		for _, g := range gs {
			if g.Active(now) && grantApplies(g, res) && roleRank[Role(g.ElevatedRole)] > roleRank[best] {
				best = Role(g.ElevatedRole)
			}
		}
	}
	return best, nil
}

// BoundRole returns the highest-ranked role the user holds from role BINDINGS
// alone (excluding any break-glass grant) that applies to res, or "" if none.
// The break-glass guard uses this so a user cannot chain one grant into a
// higher one: activation is measured against the durable bound role.
//
// Group-derived bindings COUNT here. A group binding is durable — the same kind
// of thing as a direct one — so the delegation cap treats it the same. The M-1
// invariant is untouched: break-glass grants arrive through GrantStore, never
// through a binding source, so an elevation still cannot be laundered into a
// lasting binding.
func (e *Engine) BoundRole(ctx context.Context, userID string, res Resource) (Role, error) {
	bindings, err := e.bindingsFor(ctx, userID)
	if err != nil {
		return "", err
	}
	return RoleFromBindings(bindings, res), nil
}

// AllowedProjects filters projectIDs down to those where the principal may
// perform action, loading the principal's bindings and grants ONCE.
//
// It exists because the obvious loop — calling Can per project — re-queries
// direct bindings, group bindings and break-glass grants on every iteration.
// An instance with a thousand projects would issue thousands of queries to
// render one audit page. The decision itself is identical to Can's: the same
// union of direct and group bindings, with the same break-glass overlay.
//
// Service-token principals get an empty result: a token's scope is a config or
// environment, so it never confers a project-wide action.
func (e *Engine) AllowedProjects(ctx context.Context, p auth.Principal, action Action, projectIDs []string) ([]string, error) {
	if p.Kind != auth.KindUser || len(projectIDs) == 0 {
		return nil, nil
	}
	bindings, err := e.bindingsFor(ctx, p.ID)
	if err != nil {
		return nil, err
	}
	var grants []*store.BreakGlassGrant
	now := e.now()
	if e.grants != nil {
		grants, err = e.grants.ListActiveForUser(ctx, p.ID, now)
		if err != nil {
			return nil, err
		}
	}
	out := make([]string, 0, len(projectIDs))
	for _, pid := range projectIDs {
		res := Resource{ProjectID: pid}
		if userAllows(bindings, action, res) {
			out = append(out, pid)
			continue
		}
		for _, g := range grants {
			if g.Active(now) && grantApplies(g, res) && roleAllows(Role(g.ElevatedRole), action) {
				out = append(out, pid)
				break
			}
		}
	}
	return out, nil
}
