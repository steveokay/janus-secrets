package store

import (
	"bytes"
	"context"
	"sync"
	"testing"
	"time"
)

// appendConst appends a row whose fields are fixed except the chain linkage,
// which it derives from head — a stand-in for the real engine closure.
func appendConst(t *testing.T, repo *AuditRepo, action string) AuditRow {
	t.Helper()
	row, err := repo.Append(context.Background(), func(h AuditHead) (AuditRow, error) {
		prev := h.Hash
		if prev == nil {
			prev = make([]byte, 32)
		}
		self := append([]byte{}, prev...)
		self[0] ^= byte(h.Seq + 1) // cheap deterministic "hash"
		return AuditRow{
			Seq: h.Seq + 1, OccurredAt: time.Now().UTC().Truncate(time.Microsecond),
			ActorKind: "user", ActorName: "a@b.c", Action: action, Resource: "r",
			Result: "success", IP: "1.2.3.4", PrevHash: prev, Hash: self,
		}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return row
}

func TestAuditAppendChainsAndReads(t *testing.T) {
	if testStore == nil {
		t.Skip("postgres/docker not available")
	}
	resetDB(t)
	ctx := context.Background()
	repo := NewAuditRepo(testStore)

	r1 := appendConst(t, repo, "token.mint")
	r2 := appendConst(t, repo, "user.create")
	if r1.Seq != 1 || r2.Seq != 2 {
		t.Fatalf("seqs = %d,%d", r1.Seq, r2.Seq)
	}
	if !bytes.Equal(r2.PrevHash, r1.Hash) {
		t.Fatalf("chain not linked")
	}

	// Iterate returns them in seq order.
	var seen []int64
	if err := repo.Iterate(ctx, func(row AuditRow) error { seen = append(seen, row.Seq); return nil }); err != nil {
		t.Fatal(err)
	}
	if len(seen) != 2 || seen[0] != 1 || seen[1] != 2 {
		t.Fatalf("iterate = %v", seen)
	}

	// Filter by action.
	var n int
	if err := repo.List(ctx, AuditFilter{Action: "user.create"}, func(AuditRow) error { n++; return nil }); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("filtered = %d", n)
	}
}

func TestAuditAppendSerializes(t *testing.T) {
	if testStore == nil {
		t.Skip("postgres/docker not available")
	}
	resetDB(t)
	repo := NewAuditRepo(testStore)
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); appendConst(t, repo, "token.mint") }()
	}
	wg.Wait()
	// Seqs must be contiguous 1..20 with no gaps or dupes (advisory lock held).
	seqs := map[int64]bool{}
	_ = repo.Iterate(context.Background(), func(row AuditRow) error { seqs[row.Seq] = true; return nil })
	for i := int64(1); i <= 20; i++ {
		if !seqs[i] {
			t.Fatalf("missing seq %d (got %d distinct)", i, len(seqs))
		}
	}
}
