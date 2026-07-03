package store

import (
	"context"
	"errors"
	"testing"
)

// mkConfig builds a project→env→config chain. Returns the ids.
func mkConfig(t *testing.T, s *Store, cfgName string) (projectID, envID, configID string) {
	t.Helper()
	ctx := context.Background()
	id, err := s.NewID(ctx)
	if err != nil {
		t.Fatal(err)
	}
	p, err := NewProjectRepo(s).Create(ctx, id, "acme", "Acme", []byte("k"), 1)
	if err != nil {
		t.Fatal(err)
	}
	e, err := NewEnvironmentRepo(s).Create(ctx, p.ID, "prod", "Production")
	if err != nil {
		t.Fatal(err)
	}
	c, err := NewConfigRepo(s).Create(ctx, e.ID, cfgName, nil)
	if err != nil {
		t.Fatal(err)
	}
	return p.ID, e.ID, c.ID
}

func TestConfigRepoCRUD(t *testing.T) {
	s := requireStore(t)
	resetDB(t)
	ctx := context.Background()
	repo := NewConfigRepo(s)

	pid, err := s.NewID(ctx)
	if err != nil {
		t.Fatal(err)
	}
	p, err := NewProjectRepo(s).Create(ctx, pid, "acme", "Acme", []byte("k"), 1)
	if err != nil {
		t.Fatal(err)
	}
	e, err := NewEnvironmentRepo(s).Create(ctx, p.ID, "prod", "Production")
	if err != nil {
		t.Fatal(err)
	}

	c, err := repo.Create(ctx, e.ID, "prod", nil)
	if err != nil {
		t.Fatal(err)
	}
	if c.ID == "" || c.Name != "prod" || c.InheritsFrom != nil {
		t.Fatalf("unexpected config: %+v", c)
	}

	branch, err := repo.Create(ctx, e.ID, "prod-ci", &c.ID)
	if err != nil {
		t.Fatal(err)
	}
	if branch.InheritsFrom == nil || *branch.InheritsFrom != c.ID {
		t.Fatalf("inherits_from not stored: %+v", branch)
	}

	if _, err := repo.Get(ctx, c.ID); err != nil {
		t.Fatalf("Get: %v", err)
	}
	byName, err := repo.GetByName(ctx, e.ID, "prod")
	if err != nil || byName.ID != c.ID {
		t.Fatalf("GetByName: %+v err=%v", byName, err)
	}

	if _, err := repo.Create(ctx, e.ID, "prod", nil); !errors.Is(err, ErrAlreadyExists) {
		t.Fatalf("dup: got %v, want ErrAlreadyExists", err)
	}

	if _, err := repo.Create(ctx, "00000000-0000-0000-0000-000000000000", "x", nil); !errors.Is(err, ErrParentNotFound) {
		t.Fatalf("orphan: got %v, want ErrParentNotFound", err)
	}

	list, err := repo.ListByEnvironment(ctx, e.ID)
	if err != nil || len(list) != 2 {
		t.Fatalf("ListByEnvironment: len=%d err=%v", len(list), err)
	}

	if err := repo.SoftDelete(ctx, c.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.Get(ctx, c.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Get after soft delete: got %v, want ErrNotFound", err)
	}
	if err := repo.Undelete(ctx, c.ID); err != nil {
		t.Fatal(err)
	}
	if err := repo.Destroy(ctx, branch.ID); err != nil {
		t.Fatal(err)
	}
}
