package store

import (
	"context"
	"errors"
	"fmt"
	"testing"
)

func TestProjectRepoCRUD(t *testing.T) {
	s := requireStore(t)
	resetDB(t)
	ctx := context.Background()
	repo := NewProjectRepo(s)

	// Create.
	id, err := s.NewID(ctx)
	if err != nil {
		t.Fatal(err)
	}
	p, err := repo.Create(ctx, id, "acme", "Acme Web", []byte("wrapped-kek"), 1)
	if err != nil {
		t.Fatal(err)
	}
	if p.ID == "" || p.Slug != "acme" || p.KEKVersion != 1 {
		t.Fatalf("unexpected project: %+v", p)
	}

	// Get by id and by slug.
	got, err := repo.Get(ctx, p.ID)
	if err != nil || got.Slug != "acme" {
		t.Fatalf("Get: %+v err=%v", got, err)
	}
	bySlug, err := repo.GetBySlug(ctx, "acme")
	if err != nil || bySlug.ID != p.ID {
		t.Fatalf("GetBySlug: %+v err=%v", bySlug, err)
	}

	// Duplicate slug rejected (different id, same slug).
	dupID, err := s.NewID(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repo.Create(ctx, dupID, "acme", "dup", []byte("k"), 1); !errors.Is(err, ErrAlreadyExists) {
		t.Fatalf("dup slug: got %v, want ErrAlreadyExists", err)
	}

	// List.
	list, err := repo.List(ctx)
	if err != nil || len(list) != 1 {
		t.Fatalf("List: len=%d err=%v", len(list), err)
	}

	// Soft delete hides from Get and List.
	if err := repo.SoftDelete(ctx, p.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.Get(ctx, p.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Get after soft delete: got %v, want ErrNotFound", err)
	}
	if list, _ := repo.List(ctx); len(list) != 0 {
		t.Fatalf("List after soft delete: len=%d, want 0", len(list))
	}

	// Slug is reusable after soft delete (partial unique index).
	reuseID, err := s.NewID(ctx)
	if err != nil {
		t.Fatal(err)
	}
	reused, err := repo.Create(ctx, reuseID, "acme", "reuse", []byte("k"), 1)
	if err != nil {
		t.Fatalf("recreate after soft delete: %v", err)
	}

	// The reused row also holds the live "acme" slug, so restoring the
	// original would collide with it under the partial unique index. Soft
	// delete it first to free the slug for p.
	if err := repo.SoftDelete(ctx, reused.ID); err != nil {
		t.Fatal(err)
	}

	// Undelete restores.
	if err := repo.Undelete(ctx, p.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.Get(ctx, p.ID); err != nil {
		t.Fatalf("Get after undelete: %v", err)
	}

	// Destroy hard-deletes.
	if err := repo.Destroy(ctx, p.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.Get(ctx, p.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Get after destroy: got %v, want ErrNotFound", err)
	}

	// Get missing.
	if _, err := repo.Get(ctx, "00000000-0000-0000-0000-000000000000"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Get missing: got %v, want ErrNotFound", err)
	}
}

func TestProjectRepo_ListPage(t *testing.T) {
	s := requireStore(t)
	resetDB(t)
	repo := NewProjectRepo(s)
	ctx := context.Background()
	for i := 0; i < 5; i++ {
		id, err := s.NewID(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := repo.Create(ctx, id, fmt.Sprintf("proj-%d", i), fmt.Sprintf("Proj %d", i), []byte("wrapped-kek-000"), 1); err != nil {
			t.Fatal(err)
		}
	}
	all, err := repo.ListPage(ctx, 0, nil)
	if err != nil || len(all) != 5 {
		t.Fatalf("unbounded: len=%d err=%v", len(all), err)
	}
	seen := map[string]bool{}
	var after *Cursor
	for {
		page, err := repo.ListPage(ctx, 2, after)
		if err != nil {
			t.Fatal(err)
		}
		for _, p := range page {
			if seen[p.ID] {
				t.Fatalf("duplicate id %s", p.ID)
			}
			seen[p.ID] = true
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
