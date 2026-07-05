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

// tokenAllows reports whether a token's scope+access permits action on res.
func tokenAllows(scope TokenScope, action Action, res Resource) bool {
	if !tokenCapabilities(scope.Access)[action] {
		return false
	}
	switch scope.Kind {
	case "config":
		return res.ConfigID != "" && res.ConfigID == scope.ID
	case "environment":
		return res.EnvID != "" && res.EnvID == scope.ID
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

// userAllows reports whether any applicable binding's role grants action.
func userAllows(bindings []*store.RoleBinding, action Action, res Resource) bool {
	for _, b := range bindings {
		if bindingApplies(b, res) && roleAllows(Role(b.Role), action) {
			return true
		}
	}
	return false
}
