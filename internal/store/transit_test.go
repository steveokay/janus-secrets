package store

import (
	"context"
	"errors"
	"testing"
)

func TestTransitRepoLifecycle(t *testing.T) {
	s := requireStore(t)
	ctx := context.Background()
	// Isolate from any other transit rows (resetDB does not cover these tables).
	if _, err := s.pool.Exec(ctx, `TRUNCATE transit_keys RESTART IDENTITY CASCADE`); err != nil {
		t.Fatalf("truncate: %v", err)
	}
	r := NewTransitRepo(s)

	newID := func() string {
		id, err := s.NewID(ctx)
		if err != nil {
			t.Fatalf("NewID: %v", err)
		}
		return id
	}

	k, err := r.Create(ctx, newID(), "billing", "aes256-gcm",
		&TransitKeyVersion{ID: newID(), Version: 1, WrappedMaterial: []byte("wrapped-v1")})
	if err != nil {
		t.Fatal(err)
	}
	if k.Name != "billing" || k.LatestVersion != 1 || k.MinDecryptionVersion != 1 {
		t.Fatalf("bad key: %+v", k)
	}

	// Duplicate name → ErrAlreadyExists.
	_, err = r.Create(ctx, newID(), "billing", "aes256-gcm",
		&TransitKeyVersion{ID: newID(), Version: 1, WrappedMaterial: []byte("x")})
	if !errors.Is(err, ErrAlreadyExists) {
		t.Fatalf("dup name: want ErrAlreadyExists, got %v", err)
	}

	// Append v2, bump latest.
	if err := r.AppendVersion(ctx, k.ID,
		&TransitKeyVersion{ID: newID(), Version: 2, WrappedMaterial: []byte("wrapped-v2")}); err != nil {
		t.Fatal(err)
	}

	got, err := r.GetByName(ctx, "billing")
	if err != nil {
		t.Fatal(err)
	}
	if got.LatestVersion != 2 || len(got.Versions) != 2 {
		t.Fatalf("after append: %+v", got)
	}
	if string(got.Versions[1].WrappedMaterial) != "wrapped-v2" {
		t.Fatalf("v2 material: %q", got.Versions[1].WrappedMaterial)
	}

	// GetByID mirrors GetByName.
	byID, err := r.GetByID(ctx, k.ID)
	if err != nil {
		t.Fatal(err)
	}
	if byID.Name != "billing" || byID.LatestVersion != 2 || len(byID.Versions) != 2 {
		t.Fatalf("GetByID: %+v", byID)
	}

	// Config update.
	if err := r.UpdateConfig(ctx, k.ID, ptrInt(2), ptrBool(true)); err != nil {
		t.Fatal(err)
	}
	got, _ = r.GetByName(ctx, "billing")
	if got.MinDecryptionVersion != 2 || !got.DeletionAllowed {
		t.Fatalf("after config: %+v", got)
	}

	// List returns metadata (no versions).
	list, err := r.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].Name != "billing" || len(list[0].Versions) != 0 {
		t.Fatalf("list: %+v", list)
	}

	// TrimBelow removes v1.
	if err := r.TrimBelow(ctx, k.ID, 2); err != nil {
		t.Fatal(err)
	}
	got, _ = r.GetByName(ctx, "billing")
	if len(got.Versions) != 1 || got.Versions[0].Version != 2 {
		t.Fatalf("after trim: %+v", got)
	}

	// Delete removes the key.
	if err := r.Delete(ctx, k.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := r.GetByName(ctx, "billing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("after delete: want ErrNotFound, got %v", err)
	}
	if err := r.Delete(ctx, k.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("double delete: want ErrNotFound, got %v", err)
	}

	// Missing key → ErrNotFound.
	if _, err := r.GetByName(ctx, "nope"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing: want ErrNotFound, got %v", err)
	}
	if _, err := r.GetByID(ctx, newID()); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing by id: want ErrNotFound, got %v", err)
	}
}

func ptrInt(i int) *int    { return &i }
func ptrBool(b bool) *bool { return &b }
