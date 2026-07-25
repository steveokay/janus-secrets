package authz

import "testing"

// TestSecretPruneOwnerOnly pins secret:prune to the owner role. It is the only
// action in the vocabulary that irreversibly destroys secret value history, so
// an accidental widening of the matrix (e.g. folding it into adminActions)
// should fail loudly here.
func TestSecretPruneOwnerOnly(t *testing.T) {
	if !roleAllows(RoleOwner, SecretPrune) {
		t.Fatal("owner must hold secret:prune")
	}
	for _, role := range []Role{RoleViewer, RoleDeveloper, RoleAdmin} {
		if roleAllows(role, SecretPrune) {
			t.Errorf("%s must not hold secret:prune", role)
		}
	}
	if roleAllows(Role("root"), SecretPrune) {
		t.Error("unknown role granted secret:prune")
	}
}
