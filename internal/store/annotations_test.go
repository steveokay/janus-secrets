package store

import (
	"context"
	"testing"
)

func strp(s string) *string { return &s }

func TestAnnotationRepo(t *testing.T) {
	s := requireStore(t)
	resetDB(t)
	ctx := context.Background()
	_, _, cid := mkConfig(t, s, "default")
	ar := NewAnnotationRepo(s)

	// Empty to start.
	if got, err := ar.List(ctx, cid); err != nil || len(got) != 0 {
		t.Fatalf("List empty = %v, %v", got, err)
	}

	// Owner + note.
	if err := ar.Set(ctx, cid, "DATABASE_URL", strp("team-data"), strp("primary DB dsn"), ""); err != nil {
		t.Fatalf("Set owner+note: %v", err)
	}
	// Owner only (note NULL).
	if err := ar.Set(ctx, cid, "API_KEY", strp("team-api"), nil, ""); err != nil {
		t.Fatalf("Set owner-only: %v", err)
	}
	got, err := ar.List(ctx, cid)
	if err != nil || len(got) != 2 {
		t.Fatalf("List = %v, %v", got, err)
	}
	// Sorted by key: API_KEY first, then DATABASE_URL.
	if got[0].Key != "API_KEY" || got[0].Owner == nil || *got[0].Owner != "team-api" || got[0].Note != nil {
		t.Fatalf("API_KEY entry = %+v", got[0])
	}
	if got[1].Key != "DATABASE_URL" || got[1].Owner == nil || *got[1].Owner != "team-data" ||
		got[1].Note == nil || *got[1].Note != "primary DB dsn" {
		t.Fatalf("DATABASE_URL entry = %+v", got[1])
	}

	// Upsert overwrites (note now set, owner cleared to NULL).
	if err := ar.Set(ctx, cid, "API_KEY", nil, strp("rotate quarterly"), ""); err != nil {
		t.Fatalf("Set upsert: %v", err)
	}
	got, _ = ar.List(ctx, cid)
	for _, e := range got {
		if e.Key == "API_KEY" {
			if e.Owner != nil {
				t.Fatalf("API_KEY owner should be NULL after upsert, got %v", *e.Owner)
			}
			if e.Note == nil || *e.Note != "rotate quarterly" {
				t.Fatalf("API_KEY note = %v", e.Note)
			}
		}
	}

	// Clear one.
	if err := ar.Clear(ctx, cid, "API_KEY"); err != nil {
		t.Fatalf("Clear: %v", err)
	}
	got, _ = ar.List(ctx, cid)
	if len(got) != 1 || got[0].Key != "DATABASE_URL" {
		t.Fatalf("after clear = %v", got)
	}

	// Clearing an absent annotation is a no-op.
	if err := ar.Clear(ctx, cid, "NOPE"); err != nil {
		t.Fatalf("Clear absent: %v", err)
	}
}
