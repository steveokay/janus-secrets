package store

import (
	"context"
	"errors"
	"testing"
)

// mkUser inserts a throwaway user and returns its id.
func mkUser(t *testing.T, email string) string {
	t.Helper()
	u, err := NewUserRepo(testStore).Create(context.Background(), email, nil)
	if err != nil {
		t.Fatal(err)
	}
	return u.ID
}

func TestRoleBindingUpsertAndList(t *testing.T) {
	if testStore == nil {
		t.Skip("postgres/docker not available")
	}
	resetDB(t)
	ctx := context.Background()
	repo := NewRoleBindingRepo(testStore)
	uid := mkUser(t, "rb1@example.com")

	// Instance-level grant.
	b, err := repo.Create(ctx, RoleBindingInput{SubjectUserID: uid, ScopeLevel: "instance", Role: "owner"})
	if err != nil {
		t.Fatal(err)
	}
	if b.Role != "owner" || b.ProjectID != nil || b.EnvironmentID != nil {
		t.Fatalf("binding = %+v", b)
	}

	// Re-grant the same scope updates the role in place (upsert), not a 2nd row.
	if _, err := repo.Create(ctx, RoleBindingInput{SubjectUserID: uid, ScopeLevel: "instance", Role: "admin"}); err != nil {
		t.Fatal(err)
	}
	all, err := repo.ListForUser(ctx, uid)
	if err != nil || len(all) != 1 || all[0].Role != "admin" {
		t.Fatalf("after upsert: %d %+v (err %v)", len(all), all, err)
	}

	if n, err := repo.CountInstanceOwners(ctx); err != nil || n != 0 {
		t.Fatalf("owners after downgrade: %d (err %v)", n, err)
	}
}

func TestRoleBindingScopeAndDelete(t *testing.T) {
	if testStore == nil {
		t.Skip("postgres/docker not available")
	}
	resetDB(t)
	ctx := context.Background()
	repo := NewRoleBindingRepo(testStore)
	uid := mkUser(t, "rb2@example.com")

	id, _ := testStore.NewID(ctx)
	p, err := NewProjectRepo(testStore).Create(ctx, id, "rbproj", "P", []byte("k"), 1)
	if err != nil {
		t.Fatal(err)
	}
	pid := p.ID
	if _, err := repo.Create(ctx, RoleBindingInput{SubjectUserID: uid, ScopeLevel: "project", ProjectID: &pid, Role: "developer"}); err != nil {
		t.Fatal(err)
	}

	members, err := repo.ListForScope(ctx, "project", pid)
	if err != nil || len(members) != 1 || members[0].SubjectUserID != uid {
		t.Fatalf("members: %d %+v (err %v)", len(members), members, err)
	}

	if err := repo.DeleteForSubjectScope(ctx, uid, "project", &pid, nil); err != nil {
		t.Fatal(err)
	}
	if err := repo.DeleteForSubjectScope(ctx, uid, "project", &pid, nil); !errors.Is(err, ErrNotFound) {
		t.Fatalf("double delete: %v", err)
	}
}
