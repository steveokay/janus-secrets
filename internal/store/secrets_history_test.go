package store

import (
	"context"
	"testing"
)

func TestHistoryDiffRollback(t *testing.T) {
	s := requireStore(t)
	resetDB(t)
	ctx := context.Background()
	_, _, configID := mkConfig(t, s, "prod")
	repo := NewSecretRepo(s)

	// v1: A=a1, B=b1
	if _, err := repo.SaveConfigVersion(ctx, configID, []Change{
		{Key: "A", Value: ev("a1")}, {Key: "B", Value: ev("b1")},
	}, "v1", "u"); err != nil {
		t.Fatal(err)
	}
	// v2: A=a2 (change), C=c1 (add), B deleted (remove)
	if _, err := repo.SaveConfigVersion(ctx, configID, []Change{
		{Key: "A", Value: ev("a2")}, {Key: "C", Value: ev("c1")}, {Key: "B", Value: nil},
	}, "v2", "u"); err != nil {
		t.Fatal(err)
	}

	// ListVersions.
	versions, err := repo.ListVersions(ctx, configID)
	if err != nil || len(versions) != 2 {
		t.Fatalf("ListVersions: len=%d err=%v", len(versions), err)
	}
	if versions[0].Version != 1 || versions[1].Version != 2 {
		t.Fatalf("versions not ordered ascending: %+v", versions)
	}

	// GetKeyHistory for A: a1 (v1), a2 (v2).
	hist, err := repo.GetKeyHistory(ctx, configID, "A")
	if err != nil {
		t.Fatal(err)
	}
	if len(hist) != 2 || hist[0].ValueVersion != 1 || hist[1].ValueVersion != 2 {
		t.Fatalf("A history: %+v", hist)
	}
	if string(hist[1].Ciphertext) != "ct-a2" {
		t.Fatalf("A v2 ciphertext = %q", hist[1].Ciphertext)
	}

	// Diff(1,2): added=[C], changed=[A], removed=[B].
	d, err := repo.Diff(ctx, configID, 1, 2)
	if err != nil {
		t.Fatal(err)
	}
	if !eqSet(d.Added, []string{"C"}) || !eqSet(d.Changed, []string{"A"}) || !eqSet(d.Removed, []string{"B"}) {
		t.Fatalf("diff mismatch: %+v", d)
	}

	// Rollback to v1 → v3 whose state equals v1 (A=a1, B=b1, C gone).
	rb, err := repo.Rollback(ctx, configID, 1, "rollback to v1", "u")
	if err != nil {
		t.Fatal(err)
	}
	if rb.Version != 3 {
		t.Fatalf("rollback version = %d, want 3", rb.Version)
	}
	_, state, err := repo.GetLatest(ctx, configID)
	if err != nil {
		t.Fatal(err)
	}
	if len(state) != 2 || string(state["A"].Ciphertext) != "ct-a1" || string(state["B"].Ciphertext) != "ct-b1" {
		t.Fatalf("state after rollback: %+v", state)
	}
	if _, ok := state["C"]; ok {
		t.Fatal("C should be gone after rollback to v1")
	}

	// Rollback reused existing secret_values rows (no new value rows written).
	var rowCount int
	if err := s.pool.QueryRow(ctx,
		`SELECT count(*) FROM secret_values WHERE config_id = $1::uuid`, configID).Scan(&rowCount); err != nil {
		t.Fatal(err)
	}
	if rowCount != 4 { // a1, b1, a2, c1 — rollback added none
		t.Fatalf("secret_values rows = %d, want 4 (rollback must reuse)", rowCount)
	}
}

// eqSet compares two string slices as sets.
func eqSet(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	m := map[string]int{}
	for _, x := range a {
		m[x]++
	}
	for _, x := range b {
		m[x]--
	}
	for _, v := range m {
		if v != 0 {
			return false
		}
	}
	return true
}
