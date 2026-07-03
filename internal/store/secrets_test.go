package store

import (
	"context"
	"errors"
	"testing"
)

func ev(s string) *EncryptedValue {
	return &EncryptedValue{
		WrappedDEK: []byte("dek-" + s), Ciphertext: []byte("ct-" + s),
		Nonce: []byte("nonce-" + s), DEKKeyVersion: 1,
	}
}

// TestSaveConfigVersionCollapsesBatch verifies that when a single save contains
// multiple changes for the same key, only the last one takes effect and no
// orphan secret_values row is written for the superseded change.
func TestSaveConfigVersionCollapsesBatch(t *testing.T) {
	s := requireStore(t)
	resetDB(t)
	ctx := context.Background()
	_, _, configID := mkConfig(t, s, "prod")
	repo := NewSecretRepo(s)

	// Set K then delete K in the same batch: net effect is that K never exists.
	if _, err := repo.SaveConfigVersion(ctx, configID, []Change{
		{Key: "K", Value: ev("k1")},
		{Key: "K", Value: nil},
	}, "set-then-delete", "u"); err != nil {
		t.Fatal(err)
	}
	_, state, err := repo.GetLatest(ctx, configID)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := state["K"]; ok {
		t.Fatal("K should be absent after set-then-delete in one batch")
	}
	// No secret_values row should have been written for the superseded set.
	var rows int
	if err := s.pool.QueryRow(ctx,
		`SELECT count(*) FROM secret_values WHERE config_id = $1::uuid`, configID).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if rows != 0 {
		t.Fatalf("secret_values rows = %d, want 0 (no orphan from superseded set)", rows)
	}
}

func TestSaveAndGetLatest(t *testing.T) {
	s := requireStore(t)
	resetDB(t)
	ctx := context.Background()
	_, _, configID := mkConfig(t, s, "prod")
	repo := NewSecretRepo(s)

	// First save: two keys in one version.
	cv, err := repo.SaveConfigVersion(ctx, configID, []Change{
		{Key: "DB_URL", Value: ev("db1")},
		{Key: "API_KEY", Value: ev("api1")},
	}, "initial", "alice")
	if err != nil {
		t.Fatal(err)
	}
	if cv.Version != 1 || cv.Message != "initial" || cv.CreatedBy != "alice" {
		t.Fatalf("unexpected version: %+v", cv)
	}

	got, state, err := repo.GetLatest(ctx, configID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Version != 1 || len(state) != 2 {
		t.Fatalf("GetLatest v1: version=%d keys=%d", got.Version, len(state))
	}
	if string(state["DB_URL"].Ciphertext) != "ct-db1" {
		t.Fatalf("DB_URL ciphertext mismatch: %q", state["DB_URL"].Ciphertext)
	}
	if state["DB_URL"].ValueVersion != 1 {
		t.Fatalf("DB_URL value_version = %d, want 1", state["DB_URL"].ValueVersion)
	}

	// Second save: change one key, delete the other. Dedup expected.
	if _, err := repo.SaveConfigVersion(ctx, configID, []Change{
		{Key: "DB_URL", Value: ev("db2")},
		{Key: "API_KEY", Value: nil}, // delete
	}, "rotate", "bob"); err != nil {
		t.Fatal(err)
	}

	got, state, err = repo.GetLatest(ctx, configID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Version != 2 {
		t.Fatalf("version = %d, want 2", got.Version)
	}
	if len(state) != 1 {
		t.Fatalf("live keys = %d, want 1 (API_KEY deleted)", len(state))
	}
	if string(state["DB_URL"].Ciphertext) != "ct-db2" || state["DB_URL"].ValueVersion != 2 {
		t.Fatalf("DB_URL after rotate: %q v%d", state["DB_URL"].Ciphertext, state["DB_URL"].ValueVersion)
	}
	if _, ok := state["API_KEY"]; ok {
		t.Fatal("API_KEY should be absent after delete")
	}

	// Dedup: only ONE new secret_values row was written in v2 (DB_URL), not two.
	var rowCount int
	if err := s.pool.QueryRow(ctx,
		`SELECT count(*) FROM secret_values WHERE config_id = $1::uuid`, configID).Scan(&rowCount); err != nil {
		t.Fatal(err)
	}
	if rowCount != 3 { // db1, api1, db2
		t.Fatalf("secret_values rows = %d, want 3 (dedup failed)", rowCount)
	}

	// GetVersion(1) still shows the original state.
	v1, state1, err := repo.GetVersion(ctx, configID, 1)
	if err != nil {
		t.Fatal(err)
	}
	if v1.Version != 1 || len(state1) != 2 || string(state1["API_KEY"].Ciphertext) != "ct-api1" {
		t.Fatalf("GetVersion(1) mismatch: v%d keys=%d", v1.Version, len(state1))
	}

	// Empty first version is allowed on a fresh config.
	_, _, other := mkConfigNamed(t, s, "acme", "staging", "root")
	ecv, err := repo.SaveConfigVersion(ctx, other, nil, "empty", "alice")
	if err != nil {
		t.Fatalf("empty save: %v", err)
	}
	if ecv.Version != 1 {
		t.Fatalf("empty version = %d, want 1", ecv.Version)
	}

	// GetLatest on a config with no versions is ErrNotFound.
	_, _, empty := mkConfigNamed(t, s, "acme", "dev", "root")
	if _, _, err := repo.GetLatest(ctx, empty); !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetLatest no versions: got %v, want ErrNotFound", err)
	}

	// Save against a soft-deleted config is ErrConflict.
	if err := NewConfigRepo(s).SoftDelete(ctx, configID); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.SaveConfigVersion(ctx, configID, []Change{{Key: "X", Value: ev("x")}}, "m", "a"); !errors.Is(err, ErrConflict) {
		t.Fatalf("save to deleted config: got %v, want ErrConflict", err)
	}
}

// mkConfigNamed builds a chain reusing an existing project by slug (creating it
// if needed), a fresh environment, and a config.
func mkConfigNamed(t *testing.T, s *Store, projectSlug, envSlug, cfgName string) (projectID, envID, configID string) {
	t.Helper()
	ctx := context.Background()
	projects := NewProjectRepo(s)
	p, err := projects.GetBySlug(ctx, projectSlug)
	if errors.Is(err, ErrNotFound) {
		p, err = projects.Create(ctx, projectSlug, projectSlug, []byte("k"), 1)
	}
	if err != nil {
		t.Fatal(err)
	}
	e, err := NewEnvironmentRepo(s).Create(ctx, p.ID, envSlug, envSlug)
	if err != nil {
		t.Fatal(err)
	}
	c, err := NewConfigRepo(s).Create(ctx, e.ID, cfgName, nil)
	if err != nil {
		t.Fatal(err)
	}
	return p.ID, e.ID, c.ID
}
