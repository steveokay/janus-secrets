package authz

import "github.com/steveokay/janus-secrets/internal/store"

// tokenCapabilities maps a token access level to its allowed actions.
func tokenCapabilities(access string) map[Action]bool {
	switch access {
	case "read":
		return setOf(SecretRead, ConfigRead)
	case "readwrite":
		return setOf(SecretRead, ConfigRead, SecretWrite)
	default:
		return nil
	}
}

// transitTokenCapabilities maps a transit token's access to actions.
func transitTokenCapabilities(access string) map[Action]bool {
	switch access {
	case "use":
		return setOf(TransitRead, TransitUse)
	case "manage":
		return setOf(TransitRead, TransitUse, TransitManage)
	default:
		return nil
	}
}

// tokenAllows reports whether a token's scope+access permits action on res.
func tokenAllows(scope TokenScope, action Action, res Resource) bool {
	switch scope.Kind {
	case "config":
		return tokenCapabilities(scope.Access)[action] && res.ConfigID != "" && res.ConfigID == scope.ID
	case "environment":
		return tokenCapabilities(scope.Access)[action] && res.EnvID != "" && res.EnvID == scope.ID
	case "transit":
		if !transitTokenCapabilities(scope.Access)[action] {
			return false
		}
		return scope.ID == "" || scope.ID == res.TransitKey // "" = all keys
	default:
		return false
	}
}

// bindingApplies reports whether a binding is in scope for res.
func bindingApplies(b *store.RoleBinding, res Resource) bool {
	switch b.ScopeLevel {
	case "instance":
		return true
	case "project":
		return b.ProjectID != nil && res.ProjectID != "" && *b.ProjectID == res.ProjectID
	case "environment":
		return b.EnvironmentID != nil && res.EnvID != "" && *b.EnvironmentID == res.EnvID
	default:
		return false
	}
}

// grantApplies reports whether a break-glass grant's scope covers res. It uses
// exactly the same scope-matching as bindingApplies, so a grant can never apply
// to a different scope than the one it was activated on (deny-by-default).
func grantApplies(g *store.BreakGlassGrant, res Resource) bool {
	switch g.ScopeLevel {
	case "instance":
		return true
	case "project":
		return g.ProjectID != nil && res.ProjectID != "" && *g.ProjectID == res.ProjectID
	case "environment":
		return g.EnvironmentID != nil && res.EnvID != "" && *g.EnvironmentID == res.EnvID
	default:
		return false
	}
}

// userAllows reports whether any applicable binding's role grants action.
func userAllows(bindings []*store.RoleBinding, action Action, res Resource) bool {
	for _, b := range bindings {
		if bindingApplies(b, res) && roleAllows(Role(b.Role), action) {
			return true
		}
	}
	return false
}
