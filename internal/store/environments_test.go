package store

import (
	"context"
	"errors"
	"fmt"
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

func TestEnvironmentRepo_ListByProjectPage(t *testing.T) {
	s := requireStore(t)
	resetDB(t)
	ctx := context.Background()
	repo := NewEnvironmentRepo(s)

	pid, err := s.NewID(ctx)
	if err != nil {
		t.Fatal(err)
	}
	p, err := NewProjectRepo(s).Create(ctx, pid, "acme", "Acme", []byte("k"), 1)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 5; i++ {
		if _, err := repo.Create(ctx, p.ID, fmt.Sprintf("env-%d", i), fmt.Sprintf("Env %d", i)); err != nil {
			t.Fatal(err)
		}
	}
	all, err := repo.ListByProjectPage(ctx, p.ID, 0, nil)
	if err != nil || len(all) != 5 {
		t.Fatalf("unbounded: len=%d err=%v", len(all), err)
	}
	seen := map[string]bool{}
	var after *Cursor
	for {
		page, err := repo.ListByProjectPage(ctx, p.ID, 2, after)
		if err != nil {
			t.Fatal(err)
		}
		for _, e := range page {
			if seen[e.ID] {
				t.Fatalf("duplicate id %s", e.ID)
			}
			seen[e.ID] = true
		}
		if len(page) < 2 {
			break
		}
		last := page[len(page)-1]
		after = &Cursor{CreatedAt: last.CreatedAt, ID: last.ID}
	}
	if len(seen) != 5 {
		t.Fatalf("covered %d of 5", len(seen))
	}
}

func TestEnvironmentRepo_LastActivity(t *testing.T) {
	s := requireStore(t)
	resetDB(t)
	ctx := context.Background()
	pr := NewProjectRepo(s)
	er := NewEnvironmentRepo(s)
	cr := NewConfigRepo(s)
	sr := NewSecretRepo(s)

	id, err := s.NewID(ctx)
	if err != nil {
		t.Fatal(err)
	}
	p, err := pr.Create(ctx, id, "p", "P", []byte("k"), 1)
	if err != nil {
		t.Fatal(err)
	}

	e1, err := er.Create(ctx, p.ID, "dev", "Dev") // will get a version
	if err != nil {
		t.Fatal(err)
	}
	e2, err := er.Create(ctx, p.ID, "prod", "Prod") // stays empty
	if err != nil {
		t.Fatal(err)
	}
	e3, err := er.Create(ctx, p.ID, "old", "Old") // has a version but soft-deleted
	if err != nil {
		t.Fatal(err)
	}
	c1, err := cr.Create(ctx, e1.ID, "root", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sr.SaveConfigVersion(ctx, c1.ID, nil, "init", "tester"); err != nil {
		t.Fatalf("save version: %v", err)
	}
	c3, err := cr.Create(ctx, e3.ID, "root", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sr.SaveConfigVersion(ctx, c3.ID, nil, "init", "tester"); err != nil {
		t.Fatalf("save version: %v", err)
	}
	if err := er.SoftDelete(ctx, e3.ID); err != nil {
		t.Fatalf("soft delete: %v", err)
	}

	m, err := er.LastActivity(ctx, []string{e1.ID, e2.ID, e3.ID})
	if err != nil {
		t.Fatalf("LastActivity: %v", err)
	}
	if _, ok := m[e1.ID]; !ok {
		t.Errorf("e1 should have activity")
	}
	if _, ok := m[e2.ID]; ok {
		t.Errorf("e2 empty, should be absent")
	}
	if _, ok := m[e3.ID]; ok {
		t.Errorf("e3 soft-deleted, should be absent despite having a version")
	}
}

func TestEnvironmentRepo_UpdateName(t *testing.T) {
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

	if err := repo.UpdateName(ctx, e.ID, "Prod (new)"); err != nil {
		t.Fatalf("UpdateName: %v", err)
	}
	got, err := repo.Get(ctx, e.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Name != "Prod (new)" {
		t.Errorf("name = %q, want %q", got.Name, "Prod (new)")
	}
	if got.Slug != "prod" {
		t.Errorf("slug must be immutable, got %q", got.Slug)
	}

	if err := repo.UpdateName(ctx, "00000000-0000-0000-0000-000000000000", "X"); !errors.Is(err, ErrNotFound) {
		t.Errorf("missing id: want ErrNotFound, got %v", err)
	}
}
