package authz

import (
	"context"
	"slices"

	"github.com/steveokay/janus-secrets/internal/auth"
	"github.com/steveokay/janus-secrets/internal/store"
)

// AllActions returns every grantable action, sorted.
//
// It is DERIVED from the owner bundle rather than being a second hand-written
// list: owner is the cumulative union of every role's actions, so an action
// that appears in no bundle is one no binding can ever confer, and leaving it
// out is correct. A hand-maintained list would silently go stale the first time
// someone added an action and forgot this file.
func AllActions() []Action {
	out := make([]Action, 0, len(ownerActions))
	for a := range ownerActions {
		out = append(out, a)
	}
	slices.Sort(out)
	return out
}

// Effective is a principal's permission set, split by WHERE it holds.
//
// The split is the whole point. Instance-scoped features (transit, the group
// catalog, user management) are reachable only with an instance-level binding,
// while project-shaped features are reachable with a binding on any single
// project. A caller that conflates the two re-creates the problem this exists
// to solve: a project viewer holds `transit:read` inside their project, and
// showing them Transit on that basis just moves the 403 one click later.
//
// This is a HINT for presentation. The server re-decides every request through
// Can; nothing here grants anything.
type Effective struct {
	// Instance holds the actions allowed against the instance-scoped resource.
	Instance []Action
	// Anywhere holds the actions allowed at instance scope OR on at least one
	// project or environment the principal is bound to. It answers "is this
	// feature worth showing at all", never "may this specific call proceed".
	Anywhere []Action
}

// Effective computes the principal's permission set in ONE binding resolution.
//
// It evaluates the same predicates Can does — userAllows, tokenAllows,
// grantsAllow — against a set of candidate resources, so the answer cannot
// drift from the answer Can would give for the same (action, resource). Adding
// a rule to Can without adding it here would show up as a nav item that 403s;
// the reverse shows up as a hidden feature the user can still reach by URL.
func (e *Engine) Effective(ctx context.Context, p auth.Principal, scope *TokenScope) (Effective, error) {
	all := AllActions()

	switch p.Kind {
	case auth.KindServiceToken:
		if scope == nil {
			return Effective{Instance: []Action{}, Anywhere: []Action{}}, nil
		}
		allow := func(res Resource) map[Action]bool {
			out := map[Action]bool{}
			for _, a := range all {
				if tokenAllows(*scope, a, res) {
					out[a] = true
				}
			}
			return out
		}
		inst := allow(Instance())
		return Effective{
			Instance: sortedActions(inst),
			Anywhere: sortedActions(union(inst, allow(tokenResource(*scope)))),
		}, nil

	case auth.KindUser:
		bindings, err := e.bindingsFor(ctx, p.ID)
		if err != nil {
			return Effective{}, err
		}
		var grants []*store.BreakGlassGrant
		now := e.now()
		if e.grants != nil {
			// Break-glass elevation is part of what the principal can do RIGHT
			// NOW, so leaving it out would hide the very screens someone
			// elevated in order to reach.
			grants, err = e.grants.ListActiveForUser(ctx, p.ID, now)
			if err != nil {
				return Effective{}, err
			}
		}

		allow := func(res Resource) map[Action]bool {
			out := map[Action]bool{}
			for _, a := range all {
				if userAllows(bindings, a, res) || grantsAllow(grants, now, a, res) {
					out[a] = true
				}
			}
			return out
		}

		inst := allow(Instance())
		anywhere := inst
		// Each binding and each grant contributes its OWN scope as a candidate
		// resource. An instance-level binding already applies to every resource
		// (bindingApplies returns true for it), so this loop only ever adds
		// reach that a narrower binding confers.
		for _, res := range candidateScopes(bindings, grants) {
			anywhere = union(anywhere, allow(res))
		}
		return Effective{Instance: sortedActions(inst), Anywhere: sortedActions(anywhere)}, nil

	default:
		return Effective{Instance: []Action{}, Anywhere: []Action{}}, nil
	}
}

// candidateScopes returns the distinct resources a principal's bindings and
// grants are attached to. Instance scopes are skipped: Effective always
// evaluates Instance() anyway.
func candidateScopes(bindings []*store.RoleBinding, grants []*store.BreakGlassGrant) []Resource {
	var out []Resource
	seen := map[Resource]bool{}
	add := func(r Resource) {
		if r == (Resource{}) || seen[r] {
			return
		}
		seen[r] = true
		out = append(out, r)
	}
	for _, b := range bindings {
		switch b.ScopeLevel {
		case "project":
			if b.ProjectID != nil {
				add(Resource{ProjectID: *b.ProjectID})
			}
		case "environment":
			if b.EnvironmentID != nil {
				add(Resource{EnvID: *b.EnvironmentID})
			}
		}
	}
	for _, g := range grants {
		switch g.ScopeLevel {
		case "project":
			if g.ProjectID != nil {
				add(Resource{ProjectID: *g.ProjectID})
			}
		case "environment":
			if g.EnvironmentID != nil {
				add(Resource{EnvID: *g.EnvironmentID})
			}
		}
	}
	return out
}

// tokenResource is the resource a token's own scope names.
func tokenResource(s TokenScope) Resource {
	switch s.Kind {
	case "config":
		return Resource{ConfigID: s.ID}
	case "environment":
		return Resource{EnvID: s.ID}
	case "transit":
		return Resource{TransitKey: s.ID}
	default:
		return Resource{}
	}
}

func sortedActions(set map[Action]bool) []Action {
	out := make([]Action, 0, len(set))
	for a := range set {
		out = append(out, a)
	}
	slices.Sort(out)
	return out
}
