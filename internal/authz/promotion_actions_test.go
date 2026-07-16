package authz

import "testing"

func TestPromotionActionMatrix(t *testing.T) {
	cases := []struct {
		role   Role
		action Action
		want   bool
	}{
		{RoleViewer, SecretPromote, false},
		{RoleDeveloper, SecretPromote, true},
		{RoleAdmin, SecretPromote, true},
		{RoleOwner, SecretPromote, true},
		{RoleViewer, PromotionManage, false},
		{RoleDeveloper, PromotionManage, false},
		{RoleAdmin, PromotionManage, true},
		{RoleOwner, PromotionManage, true},
		{RoleViewer, PromotionRequest, false},
		{RoleDeveloper, PromotionRequest, true},
		{RoleAdmin, PromotionRequest, true},
		{RoleOwner, PromotionRequest, true},
	}
	for _, c := range cases {
		if got := roleAllows(c.role, c.action); got != c.want {
			t.Errorf("roleAllows(%s,%s)=%v want %v", c.role, c.action, got, c.want)
		}
	}
}
