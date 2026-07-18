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
	return appendResult(t, repo, action, "success")
}

// appendResult is appendConst with an explicit result, for seeding rows with a
// non-default result (e.g. "denied") alongside the happy-path helper above.
func appendResult(t *testing.T, repo *AuditRepo, action, result string) AuditRow {
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
			Result: result, IP: "1.2.3.4", PrevHash: prev, Hash: self,
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

func TestAuditListPage(t *testing.T) {
	if testStore == nil {
		t.Skip("postgres/docker not available")
	}
	resetDB(t)
	ctx := context.Background()
	repo := NewAuditRepo(testStore)

	// Seed 5 events, actions "a.1".."a.5" (seq 1..5); "a.3" is the denied one.
	for i := 1; i <= 5; i++ {
		action := "a." + itoa(i)
		result := "success"
		if i == 3 {
			result = "denied"
		}
		appendResult(t, repo, action, result)
	}

	seqsOf := func(rows []AuditRow) []int64 {
		out := make([]int64, len(rows))
		for i, r := range rows {
			out[i] = r.Seq
		}
		return out
	}
	assertSeqs := func(t *testing.T, rows []AuditRow, want []int64) {
		t.Helper()
		got := seqsOf(rows)
		if len(got) != len(want) {
			t.Fatalf("seqs = %v, want %v", got, want)
		}
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("seqs = %v, want %v", got, want)
			}
		}
	}

	// Page 1: from head, limit 2 -> newest-first [5,4].
	page1, err := repo.ListPage(ctx, AuditFilter{}, 0, 2)
	if err != nil {
		t.Fatal(err)
	}
	assertSeqs(t, page1, []int64{5, 4})

	// Page 2: before seq 4, limit 2 -> [3,2].
	page2, err := repo.ListPage(ctx, AuditFilter{}, 4, 2)
	if err != nil {
		t.Fatal(err)
	}
	assertSeqs(t, page2, []int64{3, 2})

	// Page 3: before seq 2, limit 2 -> [1] (only one row remains).
	page3, err := repo.ListPage(ctx, AuditFilter{}, 2, 2)
	if err != nil {
		t.Fatal(err)
	}
	assertSeqs(t, page3, []int64{1})

	// Filter + cursor compose: denied filter from head -> exactly the denied row.
	denied, err := repo.ListPage(ctx, AuditFilter{Result: "denied"}, 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	assertSeqs(t, denied, []int64{3})

	// Limit respected: from head, limit 1 -> just the newest row.
	limited, err := repo.ListPage(ctx, AuditFilter{}, 0, 1)
	if err != nil {
		t.Fatal(err)
	}
	assertSeqs(t, limited, []int64{5})
}

func TestAuditRepo_Histogram(t *testing.T) {
	if testStore == nil {
		t.Skip("postgres/docker not available")
	}
	resetDB(t)
	ctx := context.Background()
	repo := NewAuditRepo(testStore)

	appendConst(t, repo, "token.mint")     // success
	appendConst(t, repo, "secret.reveal")  // success
	appendResult(t, repo, "secret.write", "denied")

	buckets, err := repo.Histogram(ctx, AuditFilter{}, "day")
	if err != nil {
		t.Fatalf("Histogram: %v", err)
	}
	counts := map[string]int{}
	for _, b := range buckets {
		counts[b.Result] += b.Count
	}
	if counts["success"] != 2 || counts["denied"] != 1 {
		t.Errorf("counts = %v, want success:2 denied:1", counts)
	}

	only, err := repo.Histogram(ctx, AuditFilter{Result: "denied"}, "day")
	if err != nil {
		t.Fatalf("Histogram (filtered): %v", err)
	}
	total := 0
	for _, b := range only {
		total += b.Count
	}
	if total != 1 {
		t.Errorf("denied-filtered total = %d, want 1", total)
	}
}
