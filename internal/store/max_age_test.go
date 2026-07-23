package store

import (
	"context"
	"testing"
)

func TestMaxAgeRepo(t *testing.T) {
	s := requireStore(t)
	resetDB(t)
	ctx := context.Background()
	_, _, cid := mkConfig(t, s, "default")
	mr := NewMaxAgeRepo(s)

	// Empty to start.
	if got, err := mr.List(ctx, cid); err != nil || len(got) != 0 {
		t.Fatalf("List empty = %v, %v", got, err)
	}

	// Config default (sentinel) + a per-key override.
	if err := mr.Set(ctx, cid, MaxAgeSentinel, 2160*3600, ""); err != nil {
		t.Fatalf("Set default: %v", err)
	}
	if err := mr.Set(ctx, cid, "DATABASE_URL", 24*3600, ""); err != nil {
		t.Fatalf("Set key: %v", err)
	}
	got, err := mr.List(ctx, cid)
	if err != nil || len(got) != 2 {
		t.Fatalf("List = %v, %v", got, err)
	}
	// Sorted by key: "" first, then "DATABASE_URL".
	if got[0].Key != MaxAgeSentinel || got[0].MaxAgeSeconds != 2160*3600 {
		t.Fatalf("default entry = %+v", got[0])
	}
	if got[1].Key != "DATABASE_URL" || got[1].MaxAgeSeconds != 24*3600 {
		t.Fatalf("key entry = %+v", got[1])
	}

	// Upsert overwrites.
	if err := mr.Set(ctx, cid, "DATABASE_URL", 48*3600, ""); err != nil {
		t.Fatalf("Set upsert: %v", err)
	}
	got, _ = mr.List(ctx, cid)
	var seen int64
	for _, e := range got {
		if e.Key == "DATABASE_URL" {
			seen = e.MaxAgeSeconds
		}
	}
	if seen != 48*3600 {
		t.Fatalf("upsert seconds = %d", seen)
	}

	// Clear one; sentinel remains.
	if err := mr.Clear(ctx, cid, "DATABASE_URL"); err != nil {
		t.Fatalf("Clear: %v", err)
	}
	got, _ = mr.List(ctx, cid)
	if len(got) != 1 || got[0].Key != MaxAgeSentinel {
		t.Fatalf("after clear = %v", got)
	}

	// Clearing an absent policy is a no-op.
	if err := mr.Clear(ctx, cid, "NOPE"); err != nil {
		t.Fatalf("Clear absent: %v", err)
	}

	// Clear the default too.
	if err := mr.Clear(ctx, cid, MaxAgeSentinel); err != nil {
		t.Fatalf("Clear default: %v", err)
	}
	if got, _ := mr.List(ctx, cid); len(got) != 0 {
		t.Fatalf("after clear-all = %v", got)
	}
}
