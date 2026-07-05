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

// ListMembers returns the bindings at a scope.
func (e *Engine) ListMembers(ctx context.Context, level, scopeID string) ([]Member, error) {
	bs, err := e.bindings.ListForScope(ctx, level, scopeID)
	if err != nil {
		return nil, err
	}
	out := make([]Member, 0, len(bs))
	for _, b := range bs {
		out = append(out, Member{UserID: b.SubjectUserID, Role: b.Role})
	}
	return out, nil
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
