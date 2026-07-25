package store

import (
	"bytes"
	"context"
	"errors"
	"testing"
)

func TestAuditCheckpointRepoRoundTrip(t *testing.T) {
	if testStore == nil {
		t.Skip("postgres/docker not available")
	}
	resetDB(t)
	ctx := context.Background()
	audit := NewAuditRepo(testStore)
	ck := NewAuditCheckpointRepo(testStore)

	// Empty chain: Head reports genesis, Latest reports none.
	seq, hash, count, err := ck.Head(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if seq != 0 || hash != nil || count != 0 {
		t.Fatalf("empty head = (%d,%v,%d)", seq, hash, count)
	}
	if latest, err := ck.Latest(ctx); err != nil || latest != nil {
		t.Fatalf("empty latest = %+v err=%v", latest, err)
	}

	// Seed 5 events, then checkpoint at the head.
	var last AuditRow
	for i := 0; i < 5; i++ {
		last = appendConst(t, audit, "token.mint")
	}
	seq, hash, count, err = ck.Head(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if seq != 5 || count != 5 || !bytes.Equal(hash, last.Hash) {
		t.Fatalf("head = (%d, count %d) hash-match=%v", seq, count, bytes.Equal(hash, last.Hash))
	}

	if err := ck.Insert(ctx, AuditCheckpointRow{
		ThroughSeq: seq, ThroughHash: hash, EventCount: count, MAC: []byte("macbytes"),
	}); err != nil {
		t.Fatal(err)
	}
	// Duplicate anchor at the same seq → ErrAlreadyExists (UNIQUE constraint).
	if err := ck.Insert(ctx, AuditCheckpointRow{
		ThroughSeq: seq, ThroughHash: hash, EventCount: count, MAC: []byte("macbytes"),
	}); !errors.Is(err, ErrAlreadyExists) {
		t.Fatalf("duplicate insert = %v, want ErrAlreadyExists", err)
	}

	latest, err := ck.Latest(ctx)
	if err != nil || latest == nil {
		t.Fatalf("latest = %+v err=%v", latest, err)
	}
	if latest.ThroughSeq != 5 || latest.EventCount != 5 || !bytes.Equal(latest.MAC, []byte("macbytes")) {
		t.Fatalf("latest = %+v", latest)
	}

	// Add 3 more events, then prune the anchored prefix.
	for i := 0; i < 3; i++ {
		appendConst(t, audit, "user.create")
	}
	deleted, err := ck.PruneThrough(ctx, 5)
	if err != nil {
		t.Fatal(err)
	}
	if deleted != 5 {
		t.Fatalf("pruned %d, want 5", deleted)
	}

	// The anchor row survives the prune.
	if latest, err := ck.Latest(ctx); err != nil || latest == nil || latest.ThroughSeq != 5 {
		t.Fatalf("anchor missing after prune: %+v err=%v", latest, err)
	}

	// IterateFrom(6) returns only the surviving suffix, in order.
	var seen []int64
	if err := audit.IterateFrom(ctx, 6, func(row AuditRow) error {
		seen = append(seen, row.Seq)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if len(seen) != 3 || seen[0] != 6 || seen[2] != 8 {
		t.Fatalf("IterateFrom(6) = %v", seen)
	}
}
