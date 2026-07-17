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

// set returns a Change.Encrypt closure yielding ev(s) regardless of version.
func set(s string) func(int) (*EncryptedValue, error) {
	return func(int) (*EncryptedValue, error) { return ev(s), nil }
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
		{Key: "K", Encrypt: set("k1")},
		{Key: "K"},
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

// TestSaveConfigVersionPassesAssignedValueVersion asserts the store hands the
// Encrypt closure exactly the value_version it persists: 1 for a fresh key, then
// 2 on the next change of that key.
func TestSaveConfigVersionPassesAssignedValueVersion(t *testing.T) {
	s := requireStore(t)
	resetDB(t)
	ctx := context.Background()
	_, _, configID := mkConfig(t, s, "prod")
	repo := NewSecretRepo(s)

	var got int
	capture := func(str string) func(int) (*EncryptedValue, error) {
		return func(vv int) (*EncryptedValue, error) { got = vv; return ev(str), nil }
	}

	// First save of a fresh key "K": closure must receive value_version 1.
	got = 0
	if _, err := repo.SaveConfigVersion(ctx, configID, []Change{
		{Key: "K", Encrypt: capture("k1")},
	}, "v1", "u"); err != nil {
		t.Fatal(err)
	}
	if got != 1 {
		t.Fatalf("first save: closure received value_version %d, want 1", got)
	}
	_, state, err := repo.GetLatest(ctx, configID)
	if err != nil {
		t.Fatal(err)
	}
	if state["K"].ValueVersion != 1 {
		t.Fatalf("first save: persisted value_version = %d, want 1", state["K"].ValueVersion)
	}

	// Second save changing "K": closure must receive value_version 2.
	got = 0
	if _, err := repo.SaveConfigVersion(ctx, configID, []Change{
		{Key: "K", Encrypt: capture("k2")},
	}, "v2", "u"); err != nil {
		t.Fatal(err)
	}
	if got != 2 {
		t.Fatalf("second save: closure received value_version %d, want 2", got)
	}
	_, state, err = repo.GetLatest(ctx, configID)
	if err != nil {
		t.Fatal(err)
	}
	if state["K"].ValueVersion != 2 {
		t.Fatalf("second save: persisted value_version = %d, want 2", state["K"].ValueVersion)
	}
}

// TestSaveConfigVersionClosureErrorRollsBack asserts that an error returned from
// an Encrypt closure aborts the whole save: the transaction rolls back and no
// new config version or secret_values row is persisted.
func TestSaveConfigVersionClosureErrorRollsBack(t *testing.T) {
	s := requireStore(t)
	resetDB(t)
	ctx := context.Background()
	_, _, configID := mkConfig(t, s, "prod")
	repo := NewSecretRepo(s)

	// Baseline: a successful v1 save with one key.
	if _, err := repo.SaveConfigVersion(ctx, configID, []Change{
		{Key: "A", Encrypt: set("a1")},
	}, "v1", "u"); err != nil {
		t.Fatal(err)
	}

	var baselineRows int
	if err := s.pool.QueryRow(ctx,
		`SELECT count(*) FROM secret_values WHERE config_id = $1::uuid`, configID).Scan(&baselineRows); err != nil {
		t.Fatal(err)
	}

	// Attempt a v2 save whose closure fails partway through.
	errBoom := errors.New("boom")
	setErr := func() func(int) (*EncryptedValue, error) {
		return func(int) (*EncryptedValue, error) { return nil, errBoom }
	}
	_, err := repo.SaveConfigVersion(ctx, configID, []Change{
		{Key: "B", Encrypt: setErr()},
	}, "v2", "u")
	if !errors.Is(err, errBoom) {
		t.Fatalf("closure error: got %v, want errBoom", err)
	}

	// Nothing new was persisted: still exactly one config version...
	versions, err := repo.ListVersions(ctx, configID)
	if err != nil {
		t.Fatal(err)
	}
	if len(versions) != 1 {
		t.Fatalf("config versions = %d, want 1 (v2 rolled back)", len(versions))
	}
	// ...and the secret_values count is unchanged from the baseline.
	var afterRows int
	if err := s.pool.QueryRow(ctx,
		`SELECT count(*) FROM secret_values WHERE config_id = $1::uuid`, configID).Scan(&afterRows); err != nil {
		t.Fatal(err)
	}
	if afterRows != baselineRows {
		t.Fatalf("secret_values rows = %d, want %d (rollback should persist nothing)", afterRows, baselineRows)
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
		{Key: "DB_URL", Encrypt: set("db1")},
		{Key: "API_KEY", Encrypt: set("api1")},
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
		{Key: "DB_URL", Encrypt: set("db2")},
		{Key: "API_KEY"}, // delete
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
	if _, err := repo.SaveConfigVersion(ctx, configID, []Change{{Key: "X", Encrypt: set("x")}}, "m", "a"); !errors.Is(err, ErrConflict) {
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
		var id string
		id, err = s.NewID(ctx)
		if err != nil {
			t.Fatal(err)
		}
		p, err = projects.Create(ctx, id, projectSlug, projectSlug, []byte("k"), 1)
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

// TestSecretRepo_TypePersisted asserts that a Change's Type is persisted on
// its secret_values row and read back on SecretValue, and that an empty Type
// defaults to "string".
func TestSecretRepo_TypePersisted(t *testing.T) {
	st := requireStore(t)
	resetDB(t)
	ctx := context.Background()
	_, _, configID := mkConfig(t, st, "prod")
	sr := NewSecretRepo(st)
	enc := func(vv int) (*EncryptedValue, error) {
		return &EncryptedValue{WrappedDEK: []byte("w"), Ciphertext: []byte("c"), Nonce: []byte("n"), DEKKeyVersion: 1}, nil
	}
	if _, err := sr.SaveConfigVersion(ctx, configID, []Change{{Key: "CONF", Type: "json", Encrypt: enc}}, "init", "tester"); err != nil {
		t.Fatalf("save: %v", err)
	}
	_, state, err := sr.GetLatest(ctx, configID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if state["CONF"].Type != "json" {
		t.Errorf("Type = %q, want json", state["CONF"].Type)
	}
	// empty Type must default to "string"
	if _, err := sr.SaveConfigVersion(ctx, configID, []Change{{Key: "PLAIN", Encrypt: enc}}, "", "tester"); err != nil {
		t.Fatalf("save2: %v", err)
	}
	_, state2, err := sr.GetLatest(ctx, configID)
	if err != nil {
		t.Fatalf("get2: %v", err)
	}
	if state2["PLAIN"].Type != "string" {
		t.Errorf("empty Type should default to string, got %q", state2["PLAIN"].Type)
	}
}
