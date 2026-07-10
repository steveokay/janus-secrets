package authz

import "testing"

// allActions is every action in the vocabulary.
var allActions = []Action{
	SecretRead, SecretWrite, ConfigRead, ConfigCreate, ConfigDelete,
	EnvCreate, EnvDelete, ProjectRead, ProjectCreate, ProjectDelete,
	MemberRead, MemberManage, TokenRead, TokenMint, TokenRevoke,
	UserManage, AuditRead, SysSeal, SysBackup, OIDCManage,
}

func TestMatrixExhaustive(t *testing.T) {
	allowed := map[Role][]Action{
		RoleViewer:    {SecretRead, ConfigRead, ProjectRead, MemberRead},
		RoleDeveloper: {SecretRead, ConfigRead, ProjectRead, MemberRead, SecretWrite, ConfigCreate},
		RoleAdmin: {SecretRead, ConfigRead, ProjectRead, MemberRead, SecretWrite, ConfigCreate,
			ConfigDelete, EnvCreate, EnvDelete, ProjectCreate, MemberManage,
			TokenRead, TokenMint, TokenRevoke, UserManage, AuditRead, SysSeal, SysBackup, OIDCManage},
		RoleOwner: {SecretRead, ConfigRead, ProjectRead, MemberRead, SecretWrite, ConfigCreate,
			ConfigDelete, EnvCreate, EnvDelete, ProjectCreate, MemberManage,
			TokenRead, TokenMint, TokenRevoke, UserManage, AuditRead, SysSeal, SysBackup, OIDCManage, ProjectDelete},
	}
	for role, acts := range allowed {
		want := setOf(acts...)
		for _, a := range allActions {
			if got := roleAllows(role, a); got != want[a] {
				t.Errorf("roleAllows(%s, %s) = %v, want %v", role, a, got, want[a])
			}
		}
	}
	// Unknown role grants nothing.
	for _, a := range allActions {
		if roleAllows(Role("root"), a) {
			t.Errorf("unknown role granted %s", a)
		}
	}
}

func TestRoleRankAndValidity(t *testing.T) {
	if !RoleAtLeast(RoleOwner, RoleAdmin) || RoleAtLeast(RoleViewer, RoleAdmin) {
		t.Fatal("rank ordering wrong")
	}
	if ValidRole("root") || !ValidRole("owner") {
		t.Fatal("ValidRole wrong")
	}
}
