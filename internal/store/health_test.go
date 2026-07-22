package store

import (
	"context"
	"testing"
)

// TestHealthCountsEmptyStore verifies the aggregate COUNT helpers return zero on
// an empty instance (no runs, no leases, no audit events) without error.
func TestHealthCountsEmptyStore(t *testing.T) {
	st := requireStore(t)
	resetDB(t)
	ctx := context.Background()
	h := NewHealthRepo(st)

	checks := []struct {
		name string
		fn   func(context.Context) (int64, error)
	}{
		{"rotation runs failed", h.RotationRunsFailed},
		{"sync runs failed", h.SyncRunsFailed},
		{"dynamic leases active", h.DynamicLeasesActive},
		{"audit head seq", h.AuditHeadSeq},
		{"audit event count", h.AuditEventCount},
	}
	for _, c := range checks {
		got, err := c.fn(ctx)
		if err != nil {
			t.Errorf("%s: unexpected error: %v", c.name, err)
			continue
		}
		if got != 0 {
			t.Errorf("%s: want 0 on empty store, got %d", c.name, got)
		}
	}
}

// TestPoolStat exposes the pgx pool statistics; on a live pool MaxConns is
// positive.
func TestPoolStat(t *testing.T) {
	st := requireStore(t)
	stat := st.PoolStat()
	if stat == nil {
		t.Fatal("PoolStat returned nil")
	}
	if stat.MaxConns() <= 0 {
		t.Errorf("expected positive MaxConns, got %d", stat.MaxConns())
	}
}
