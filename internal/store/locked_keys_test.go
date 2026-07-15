package store

import (
	"context"
	"testing"
)

func TestLockedKeyRepo(t *testing.T) {
	s := requireStore(t)
	resetDB(t)
	ctx := context.Background()
	_, _, cid := mkConfig(t, s, "default")
	lr := NewLockedKeyRepo(s)

	if err := lr.Lock(ctx, cid, "DATABASE_URL", ""); err != nil {
		t.Fatalf("Lock: %v", err)
	}
	if err := lr.Lock(ctx, cid, "DATABASE_URL", ""); err != nil {
		t.Fatalf("Lock idempotent: %v", err)
	}
	keys, err := lr.List(ctx, cid)
	if err != nil || len(keys) != 1 || keys[0] != "DATABASE_URL" {
		t.Fatalf("List = %v, %v", keys, err)
	}
	m, err := lr.AreLocked(ctx, cid, []string{"DATABASE_URL", "API_KEY"})
	if err != nil || !m["DATABASE_URL"] || m["API_KEY"] {
		t.Fatalf("AreLocked = %v, %v", m, err)
	}
	if err := lr.Unlock(ctx, cid, "DATABASE_URL"); err != nil {
		t.Fatal(err)
	}
	if keys, _ := lr.List(ctx, cid); len(keys) != 0 {
		t.Fatalf("after unlock List = %v", keys)
	}
}
