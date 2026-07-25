package store

import (
	"context"
	"testing"
)

// TestAuditRetentionRepo covers the two read-only translations from a
// minimum-retention floor to a prune-point ceiling.
func TestAuditRetentionRepo(t *testing.T) {
	if testStore == nil {
		t.Skip("postgres/docker not available")
	}
	resetDB(t)
	ctx := context.Background()
	audit := NewAuditRepo(testStore)
	ret := NewAuditRetentionRepo(testStore)

	// Empty log: both floors report "nothing may be pruned".
	if got, err := ret.SeqOlderThanDays(ctx, 1); err != nil || got != 0 {
		t.Fatalf("empty SeqOlderThanDays = %d, %v; want 0, nil", got, err)
	}
	if got, err := ret.SeqRetainingNewest(ctx, 5); err != nil || got != 0 {
		t.Fatalf("empty SeqRetainingNewest = %d, %v; want 0, nil", got, err)
	}

	// Seed 6 events, all written just now.
	for i := 0; i < 6; i++ {
		appendConst(t, audit, "token.mint")
	}

	// Every event is younger than a 1-day floor → nothing prunable.
	if got, err := ret.SeqOlderThanDays(ctx, 1); err != nil || got != 0 {
		t.Fatalf("SeqOlderThanDays(1) = %d, %v; want 0, nil", got, err)
	}
	// Backdate the first three events past the floor.
	if _, err := testStore.pool.Exec(ctx,
		`UPDATE audit_events SET occurred_at = now() - interval '10 days' WHERE seq <= 3`); err != nil {
		t.Fatal(err)
	}
	if got, err := ret.SeqOlderThanDays(ctx, 1); err != nil || got != 3 {
		t.Fatalf("SeqOlderThanDays(1) after backdating = %d, %v; want 3, nil", got, err)
	}
	// A wider floor than the backdate still retains everything.
	if got, err := ret.SeqOlderThanDays(ctx, 30); err != nil || got != 0 {
		t.Fatalf("SeqOlderThanDays(30) = %d, %v; want 0, nil", got, err)
	}

	// Count-based floor: retaining the newest 2 of 6 events leaves seq 4 as the
	// highest prunable seq.
	if got, err := ret.SeqRetainingNewest(ctx, 2); err != nil || got != 4 {
		t.Fatalf("SeqRetainingNewest(2) = %d, %v; want 4, nil", got, err)
	}
	// Retaining more events than exist prunes nothing.
	if got, err := ret.SeqRetainingNewest(ctx, 6); err != nil || got != 0 {
		t.Fatalf("SeqRetainingNewest(6) = %d, %v; want 0, nil", got, err)
	}
	if got, err := ret.SeqRetainingNewest(ctx, 100); err != nil || got != 0 {
		t.Fatalf("SeqRetainingNewest(100) = %d, %v; want 0, nil", got, err)
	}
	// Retaining exactly one leaves everything below the head prunable.
	if got, err := ret.SeqRetainingNewest(ctx, 1); err != nil || got != 5 {
		t.Fatalf("SeqRetainingNewest(1) = %d, %v; want 5, nil", got, err)
	}

	// The count floor is computed over seq, so it stays correct across the gaps a
	// previous prune leaves behind.
	if _, err := NewAuditCheckpointRepo(testStore).PruneThrough(ctx, 2); err != nil {
		t.Fatal(err)
	}
	// Surviving seqs are 3,4,5,6; retaining the newest 2 leaves 4 prunable.
	if got, err := ret.SeqRetainingNewest(ctx, 2); err != nil || got != 4 {
		t.Fatalf("SeqRetainingNewest(2) after prune = %d, %v; want 4, nil", got, err)
	}
	// Retaining 4 keeps every survivor.
	if got, err := ret.SeqRetainingNewest(ctx, 4); err != nil || got != 2 {
		t.Fatalf("SeqRetainingNewest(4) after prune = %d, %v; want 2, nil", got, err)
	}
}
