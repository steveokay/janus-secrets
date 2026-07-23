package store

import (
	"context"
	"testing"
)

func TestAuditShipRepo(t *testing.T) {
	s := requireStore(t)
	resetDB(t)
	ctx := context.Background()
	repo := NewAuditShipRepo(s)

	// Fresh DB: the mark is seeded to the audit head (0 on a reset DB).
	got, err := repo.GetHighWater(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got != 0 {
		t.Fatalf("initial high-water = %d, want 0", got)
	}

	// Advancing forward moves the mark.
	if err := repo.AdvanceHighWater(ctx, 10); err != nil {
		t.Fatal(err)
	}
	if got, _ = repo.GetHighWater(ctx); got != 10 {
		t.Fatalf("high-water after advance = %d, want 10", got)
	}

	// The monotonic guard: an equal or lower seq is a no-op (never rewinds).
	if err := repo.AdvanceHighWater(ctx, 5); err != nil {
		t.Fatal(err)
	}
	if got, _ = repo.GetHighWater(ctx); got != 10 {
		t.Fatalf("lower advance rewound mark to %d, want 10", got)
	}
	if err := repo.AdvanceHighWater(ctx, 10); err != nil {
		t.Fatal(err)
	}
	if got, _ = repo.GetHighWater(ctx); got != 10 {
		t.Fatalf("equal advance changed mark to %d, want 10", got)
	}

	// A higher seq advances again.
	if err := repo.AdvanceHighWater(ctx, 42); err != nil {
		t.Fatal(err)
	}
	if got, _ = repo.GetHighWater(ctx); got != 42 {
		t.Fatalf("high-water = %d, want 42", got)
	}
}
