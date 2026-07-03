package store

import (
	"context"
	"errors"
	"testing"
)

func TestEnvironmentRepoCRUD(t *testing.T) {
	s := requireStore(t)
	resetDB(t)
	ctx := context.Background()
	projects := NewProjectRepo(s)
	repo := NewEnvironmentRepo(s)

	id, err := s.NewID(ctx)
	if err != nil {
		t.Fatal(err)
	}
	p, err := projects.Create(ctx, id, "acme", "Acme", []byte("k"), 1)
	if err != nil {
		t.Fatal(err)
	}

	e, err := repo.Create(ctx, p.ID, "prod", "Production")
	if err != nil {
		t.Fatal(err)
	}
	if e.ID == "" || e.ProjectID != p.ID || e.Slug != "prod" {
		t.Fatalf("unexpected env: %+v", e)
	}

	if _, err := repo.Get(ctx, e.ID); err != nil {
		t.Fatalf("Get: %v", err)
	}
	bySlug, err := repo.GetBySlug(ctx, p.ID, "prod")
	if err != nil || bySlug.ID != e.ID {
		t.Fatalf("GetBySlug: %+v err=%v", bySlug, err)
	}

	if _, err := repo.Create(ctx, p.ID, "prod", "dup"); !errors.Is(err, ErrAlreadyExists) {
		t.Fatalf("dup: got %v, want ErrAlreadyExists", err)
	}

	if _, err := repo.Create(ctx, "00000000-0000-0000-0000-000000000000", "x", ""); !errors.Is(err, ErrParentNotFound) {
		t.Fatalf("orphan: got %v, want ErrParentNotFound", err)
	}

	if _, err := repo.Create(ctx, p.ID, "staging", "Staging"); err != nil {
		t.Fatal(err)
	}
	list, err := repo.ListByProject(ctx, p.ID)
	if err != nil || len(list) != 2 {
		t.Fatalf("ListByProject: len=%d err=%v", len(list), err)
	}

	if err := repo.SoftDelete(ctx, e.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.Get(ctx, e.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Get after soft delete: got %v, want ErrNotFound", err)
	}
	if err := repo.Undelete(ctx, e.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.Get(ctx, e.ID); err != nil {
		t.Fatalf("Get after undelete: %v", err)
	}

	if err := repo.Destroy(ctx, e.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.Get(ctx, e.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Get after destroy: got %v, want ErrNotFound", err)
	}
}
