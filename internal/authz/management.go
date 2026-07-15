package authz

import (
	"context"

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
// res (for the delegation constraint), or "" if none.
func (e *Engine) EffectiveRole(ctx context.Context, userID string, res Resource) (Role, error) {
	bindings, err := e.bindings.ListForUser(ctx, userID)
	if err != nil {
		return "", err
	}
	best := Role("")
	for _, b := range bindings {
		if bindingApplies(b, res) && roleRank[Role(b.Role)] > roleRank[best] {
			best = Role(b.Role)
		}
	}
	return best, nil
}
