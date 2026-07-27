package store

import (
	"context"
	"errors"
	"fmt"
	"testing"
)

func mkGroup(t *testing.T, name, kind, claim string) *Group {
	t.Helper()
	in := GroupInput{Name: name, Kind: kind}
	if kind == GroupKindOIDC {
		in.ClaimValue = &claim
	}
	g, err := NewGroupRepo(testStore).Create(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	return g
}

func mkProject(t *testing.T, slug string) string {
	t.Helper()
	ctx := context.Background()
	id, _ := testStore.NewID(ctx)
	p, err := NewProjectRepo(testStore).Create(ctx, id, slug, "P", []byte("k"), 1)
	if err != nil {
		t.Fatal(err)
	}
	return p.ID
}

// The load-bearing schema invariant: a hand-added member of an IdP-fed group is
// UNREPRESENTABLE, not merely rejected by a handler. That is what lets us say
// "access granted via an IdP group is fully described by the IdP" and mean it.
func TestLocalMemberCannotJoinOIDCGroup(t *testing.T) {
	if testStore == nil {
		t.Skip("postgres/docker not available")
	}
	resetDB(t)
	ctx := context.Background()
	repo := NewGroupRepo(testStore)
	uid := mkUser(t, "gm1@example.com")
	oidcGroup := mkGroup(t, "payments", GroupKindOIDC, "grp-payments")

	// The composite FK (group_id, group_kind) has no ('…','local') row for an
	// oidc group, so this cannot be written at all.
	if err := repo.AddMember(ctx, oidcGroup.ID, GroupKindLocal, uid, nil); err == nil {
		t.Fatal("a local member must not be insertable into an oidc group")
	}
	// And the mirror: the sync path cannot write into a local group.
	localGroup := mkGroup(t, "oncall", GroupKindLocal, "")
	if err := repo.AddMember(ctx, localGroup.ID, GroupKindOIDC, uid, nil); err == nil {
		t.Fatal("an oidc member must not be insertable into a local group")
	}
}

func TestGroupShapeConstraints(t *testing.T) {
	if testStore == nil {
		t.Skip("postgres/docker not available")
	}
	resetDB(t)
	ctx := context.Background()
	repo := NewGroupRepo(testStore)
	claim := "grp-a"

	tests := []struct {
		name string
		in   GroupInput
	}{
		{"oidc group without a claim value", GroupInput{Name: "a", Kind: GroupKindOIDC}},
		{"local group with a claim value", GroupInput{Name: "b", Kind: GroupKindLocal, ClaimValue: &claim}},
		{"unknown kind", GroupInput{Name: "c", Kind: "ldap"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := repo.Create(ctx, tc.in); err == nil {
				t.Fatal("expected the schema to reject this shape")
			}
		})
	}

	// Two groups matching the same claim would make the effective grant depend
	// on row order.
	mkGroup(t, "first", GroupKindOIDC, "dup-claim")
	if _, err := repo.Create(ctx, GroupInput{Name: "second", Kind: GroupKindOIDC, ClaimValue: strPtr("dup-claim")}); err == nil {
		t.Fatal("two oidc groups must not share a claim value")
	}
	// A local group and an oidc group cannot share a NAME either — that
	// collision is what makes cross-authority group injection possible.
	if _, err := repo.Create(ctx, GroupInput{Name: "first", Kind: GroupKindLocal}); err == nil {
		t.Fatal("group names must be unique across kinds")
	}
}

func TestSyncOIDCMembershipReplacesSnapshot(t *testing.T) {
	if testStore == nil {
		t.Skip("postgres/docker not available")
	}
	resetDB(t)
	ctx := context.Background()
	repo := NewGroupRepo(testStore)
	uid := mkUser(t, "sync@example.com")

	mkGroup(t, "payments", GroupKindOIDC, "grp-payments")
	mkGroup(t, "platform", GroupKindOIDC, "grp-platform")
	local := mkGroup(t, "oncall", GroupKindLocal, "")
	if err := repo.AddMember(ctx, local.ID, GroupKindLocal, uid, nil); err != nil {
		t.Fatal(err)
	}

	// First login: joins both IdP groups. An unknown claim value matches
	// nothing — groups are never auto-created.
	res, err := repo.SyncOIDCMembership(ctx, uid, []string{"grp-payments", "grp-platform", "grp-unknown"})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Added) != 2 || len(res.Removed) != 0 {
		t.Fatalf("first sync: added=%v removed=%v", res.Added, res.Removed)
	}

	// Second login with one group dropped in the IdP.
	res, err = repo.SyncOIDCMembership(ctx, uid, []string{"grp-payments"})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Added) != 0 || len(res.Removed) != 1 || res.Removed[0] != "platform" {
		t.Fatalf("second sync: added=%v removed=%v", res.Added, res.Removed)
	}

	// Unchanged membership must report no change, so a routine login writes no
	// audit event.
	res, err = repo.SyncOIDCMembership(ctx, uid, []string{"grp-payments"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Changed() {
		t.Fatalf("idempotent sync reported a change: %+v", res)
	}

	// Local membership survives every login — a sync only ever owns oidc rows.
	gs, err := repo.ListForUser(ctx, uid)
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for _, g := range gs {
		names[g.Name] = true
	}
	if !names["oncall"] || !names["payments"] || names["platform"] {
		t.Fatalf("membership after syncs = %v", names)
	}

	// An empty claim list clears IdP membership but still leaves local intact.
	if _, err := repo.SyncOIDCMembership(ctx, uid, nil); err != nil {
		t.Fatal(err)
	}
	gs, err = repo.ListForUser(ctx, uid)
	if err != nil {
		t.Fatal(err)
	}
	if len(gs) != 1 || gs[0].Name != "oncall" {
		t.Fatalf("after clearing: %+v", gs)
	}
}

func TestGroupBindingsAndDerivedResolution(t *testing.T) {
	if testStore == nil {
		t.Skip("postgres/docker not available")
	}
	resetDB(t)
	ctx := context.Background()
	groups := NewGroupRepo(testStore)
	bindings := NewGroupBindingRepo(testStore)
	uid := mkUser(t, "gb@example.com")
	pid := mkProject(t, "gbproj")
	g := mkGroup(t, "payments", GroupKindLocal, "")
	if err := groups.AddMember(ctx, g.ID, GroupKindLocal, uid, nil); err != nil {
		t.Fatal(err)
	}

	if _, err := bindings.Create(ctx, GroupRoleBindingInput{
		GroupID: g.ID, ScopeLevel: "project", ProjectID: &pid, Role: "developer",
	}); err != nil {
		t.Fatal(err)
	}

	// The user resolves the binding through membership, stamped with its origin.
	derived, err := bindings.ListForUser(ctx, uid)
	if err != nil || len(derived) != 1 {
		t.Fatalf("derived = %d (err %v)", len(derived), err)
	}
	if derived[0].Role != "developer" || derived[0].ViaGroupID == nil || *derived[0].ViaGroupID != g.ID {
		t.Fatalf("derived binding = %+v", derived[0])
	}
	if derived[0].SubjectUserID != uid {
		t.Fatalf("derived binding subject = %q, want %q", derived[0].SubjectUserID, uid)
	}

	// Upsert in place rather than a second row at the same scope.
	if _, err := bindings.Create(ctx, GroupRoleBindingInput{
		GroupID: g.ID, ScopeLevel: "project", ProjectID: &pid, Role: "admin",
	}); err != nil {
		t.Fatal(err)
	}
	derived, err = bindings.ListForUser(ctx, uid)
	if err != nil || len(derived) != 1 || derived[0].Role != "admin" {
		t.Fatalf("after upsert: %d %+v (err %v)", len(derived), derived, err)
	}

	// Owner is not a role a group can carry.
	if _, err := bindings.Create(ctx, GroupRoleBindingInput{
		GroupID: g.ID, ScopeLevel: "instance", Role: "owner",
	}); err == nil {
		t.Fatal("a group binding must not accept owner")
	}

	// Scope listing joins the group's display identity.
	scoped, err := bindings.ListForScopePage(ctx, "project", pid, 0, nil)
	if err != nil || len(scoped) != 1 || scoped[0].GroupName != "payments" {
		t.Fatalf("scoped = %+v (err %v)", scoped, err)
	}

	// Removing the membership removes the derived access; the binding remains.
	if err := groups.RemoveMember(ctx, g.ID, uid); err != nil {
		t.Fatal(err)
	}
	derived, err = bindings.ListForUser(ctx, uid)
	if err != nil || len(derived) != 0 {
		t.Fatalf("after removal: %d (err %v)", len(derived), err)
	}

	// Deleting the group cascades the binding away entirely.
	if err := groups.Delete(ctx, g.ID); err != nil {
		t.Fatal(err)
	}
	scoped, err = bindings.ListForScopePage(ctx, "project", pid, 0, nil)
	if err != nil || len(scoped) != 0 {
		t.Fatalf("after group delete: %+v (err %v)", scoped, err)
	}
}

// Deleting a project must cascade group bindings exactly as it does direct
// ones, or a recreated project id would inherit stale grants.
func TestGroupBindingCascadesWithProject(t *testing.T) {
	if testStore == nil {
		t.Skip("postgres/docker not available")
	}
	resetDB(t)
	ctx := context.Background()
	bindings := NewGroupBindingRepo(testStore)
	pid := mkProject(t, "cascade")
	g := mkGroup(t, "team", GroupKindLocal, "")
	if _, err := bindings.Create(ctx, GroupRoleBindingInput{
		GroupID: g.ID, ScopeLevel: "project", ProjectID: &pid, Role: "viewer",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := testStore.pool.Exec(ctx, `DELETE FROM projects WHERE id = $1::uuid`, pid); err != nil {
		t.Fatal(err)
	}
	got, err := bindings.ListForGroup(ctx, g.ID)
	if err != nil || len(got) != 0 {
		t.Fatalf("bindings after project delete: %+v (err %v)", got, err)
	}
}

func TestGroupMemberPaginationAndDelete(t *testing.T) {
	if testStore == nil {
		t.Skip("postgres/docker not available")
	}
	resetDB(t)
	ctx := context.Background()
	repo := NewGroupRepo(testStore)
	g := mkGroup(t, "big", GroupKindLocal, "")
	for i := 0; i < 5; i++ {
		uid := mkUser(t, fmt.Sprintf("gp%d@example.com", i))
		if err := repo.AddMember(ctx, g.ID, GroupKindLocal, uid, nil); err != nil {
			t.Fatal(err)
		}
		// Re-adding is a no-op, not a duplicate or an error.
		if err := repo.AddMember(ctx, g.ID, GroupKindLocal, uid, nil); err != nil {
			t.Fatal(err)
		}
	}
	// group_members has no id column, so its cursor tie-breaks on user_id —
	// this walks every page to prove the keyset neither skips nor repeats.
	seen := map[string]bool{}
	var after *Cursor
	for {
		page, err := repo.ListMembers(ctx, g.ID, 2, after)
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range page {
			if seen[m.UserID] {
				t.Fatalf("duplicate member %s", m.UserID)
			}
			seen[m.UserID] = true
		}
		if len(page) < 2 {
			break
		}
		last := page[len(page)-1]
		after = &Cursor{CreatedAt: last.CreatedAt, ID: last.UserID}
	}
	if len(seen) != 5 {
		t.Fatalf("covered %d of 5", len(seen))
	}

	if g2, err := repo.Get(ctx, g.ID); err != nil || g2.MemberCount != 5 {
		t.Fatalf("member count = %+v (err %v)", g2, err)
	}
	for uid := range seen {
		if err := repo.RemoveMember(ctx, g.ID, uid); err != nil {
			t.Fatal(err)
		}
		if err := repo.RemoveMember(ctx, g.ID, uid); !errors.Is(err, ErrNotFound) {
			t.Fatalf("double remove: %v", err)
		}
		break
	}
}

func strPtr(s string) *string { return &s }
