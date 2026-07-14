package store

import (
	"context"
	"testing"
)

func TestProjectRepoRotateKEK(t *testing.T) {
	s := requireStore(t)
	resetDB(t)
	ctx := context.Background()
	pr := NewProjectRepo(s)

	id, err := s.NewID(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pr.Create(ctx, id, "p", "P", []byte("wrapped-v1"), 1); err != nil {
		t.Fatal(err)
	}

	newVer, err := pr.RotateKEK(ctx, id, func(oldVersion int) ([]byte, error) {
		if oldVersion != 1 {
			t.Fatalf("wrapNew got oldVersion %d, want 1", oldVersion)
		}
		return []byte("wrapped-v2"), nil
	})
	if err != nil {
		t.Fatalf("RotateKEK: %v", err)
	}
	if newVer != 2 {
		t.Fatalf("newVer = %d, want 2", newVer)
	}

	got, err := pr.Get(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if got.KEKVersion != 2 || string(got.WrappedKEK) != "wrapped-v2" {
		t.Fatalf("project after rotate = v%d %q", got.KEKVersion, got.WrappedKEK)
	}
	// Preserved v1 via raw SQL.
	var oldWrapped []byte
	if err := s.pool.QueryRow(ctx,
		`SELECT wrapped_kek FROM project_kek_versions WHERE project_id=$1::uuid AND version=1`, id).Scan(&oldWrapped); err != nil {
		t.Fatalf("select preserved v1: %v", err)
	}
	if string(oldWrapped) != "wrapped-v1" {
		t.Fatalf("preserved v1 = %q, want wrapped-v1", oldWrapped)
	}

	// Missing project -> ErrNotFound.
	if _, err := pr.RotateKEK(ctx, "00000000-0000-0000-0000-000000000000", func(int) ([]byte, error) { return nil, nil }); err != ErrNotFound {
		t.Fatalf("RotateKEK(missing) = %v, want ErrNotFound", err)
	}
}
