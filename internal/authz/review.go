package authz

import (
	"context"

	"github.com/steveokay/janus-secrets/internal/store"
)

// ApplicableBindings returns the subset of bindings that are in scope for res —
// the WHY behind a role, not just the role.
//
// A cross-scope access review has to say where a grant came from ("admin here
// because of an instance binding", "developer here via the payments group"), so
// it needs the contributing bindings, not a single collapsed answer. Sharing
// bindingApplies with the decision path is the point: a review that used its own
// scope-matching rule could report access the engine does not grant, or miss
// access it does — and either way the review would be worse than useless,
// because it would look authoritative.
//
// The slice is filtered, never copied deeply: the elements are the caller's own
// bindings.
func ApplicableBindings(bindings []*store.RoleBinding, res Resource) []*store.RoleBinding {
	var out []*store.RoleBinding
	for _, b := range bindings {
		if bindingApplies(b, res) {
			out = append(out, b)
		}
	}
	return out
}

// RoleFromBindings returns the highest-ranked role among bindings that apply to
// res, or "" if none applies. This is the union rule itself — no precedence
// between direct and group-derived bindings, no deny rules — factored out so
// BoundRole (one user, one scope, freshly loaded) and an access review (many
// users, many scopes, ONE load) evaluate the identical predicate.
func RoleFromBindings(bindings []*store.RoleBinding, res Resource) Role {
	best := Role("")
	for _, b := range bindings {
		if bindingApplies(b, res) && roleRank[Role(b.Role)] > roleRank[best] {
			best = Role(b.Role)
		}
	}
	return best
}

// BoundRoles answers BoundRole for several resources on ONE binding load.
//
// It exists for the same reason AllowedProjects does: the obvious loop calls
// BoundRole per scope, and BoundRole re-queries direct bindings AND group
// bindings every time. A bulk revoke that checked its delegation cap that way
// would issue two queries per scope it touched. The rule is unchanged —
// bindings only, never break-glass grants, so an elevation still cannot be
// laundered into authority over a durable binding (M-1).
func (e *Engine) BoundRoles(ctx context.Context, userID string, resources []Resource) ([]Role, error) {
	out := make([]Role, len(resources))
	if len(resources) == 0 {
		return out, nil
	}
	bindings, err := e.bindingsFor(ctx, userID)
	if err != nil {
		return nil, err
	}
	for i, res := range resources {
		out[i] = RoleFromBindings(bindings, res)
	}
	return out, nil
}
