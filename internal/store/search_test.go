package store

import (
	"context"
	"testing"
)

// saveKeys writes each key with a throwaway ciphertext into a new config
// version. Values are irrelevant to SearchKeys (names only).
func saveKeys(t *testing.T, s *Store, configID string, keys ...string) {
	t.Helper()
	changes := make([]Change, 0, len(keys))
	for _, k := range keys {
		changes = append(changes, Change{Key: k, Encrypt: set("v")})
	}
	if _, err := NewSecretRepo(s).SaveConfigVersion(context.Background(), configID, changes, "seed", "u"); err != nil {
		t.Fatal(err)
	}
}

// keySet flattens matches into a set of "configID/key" strings.
func keySet(ms []KeyMatch) map[string]bool {
	out := map[string]bool{}
	for _, m := range ms {
		out[m.ConfigID+"/"+m.Key] = true
	}
	return out
}

func TestSearchKeysSubstringCaseInsensitive(t *testing.T) {
	s := requireStore(t)
	resetDB(t)
	ctx := context.Background()
	_, _, cid := mkConfig(t, s, "prod")
	saveKeys(t, s, cid, "STRIPE_KEY", "stripe_secret", "DATABASE_URL", "AWS_REGION")
	repo := NewSecretRepo(s)

	// "stripe" matches both STRIPE_KEY and stripe_secret (case-insensitive).
	got, err := repo.SearchKeys(ctx, "stripe", 50)
	if err != nil {
		t.Fatal(err)
	}
	set := keySet(got)
	if !set[cid+"/STRIPE_KEY"] || !set[cid+"/stripe_secret"] {
		t.Fatalf("stripe search missed a key: %+v", got)
	}
	if set[cid+"/DATABASE_URL"] || set[cid+"/AWS_REGION"] {
		t.Fatalf("stripe search over-matched: %+v", got)
	}

	// Uppercase query still matches lowercase key (case-insensitive).
	got, err = repo.SearchKeys(ctx, "REGION", 50)
	if err != nil {
		t.Fatal(err)
	}
	if !keySet(got)[cid+"/AWS_REGION"] {
		t.Fatalf("REGION search missed AWS_REGION: %+v", got)
	}
}

func TestSearchKeysExcludesSoftDeletedConfig(t *testing.T) {
	s := requireStore(t)
	resetDB(t)
	ctx := context.Background()
	_, envID, live := mkConfig(t, s, "prod")

	// A second config in the same env that we soft-delete.
	deleted, err := NewConfigRepo(s).Create(ctx, envID, "staging", nil)
	if err != nil {
		t.Fatal(err)
	}
	saveKeys(t, s, live, "SHARED_KEY")
	saveKeys(t, s, deleted.ID, "SHARED_KEY")
	if err := NewConfigRepo(s).SoftDelete(ctx, deleted.ID); err != nil {
		t.Fatal(err)
	}

	got, err := NewSecretRepo(s).SearchKeys(ctx, "SHARED", 50)
	if err != nil {
		t.Fatal(err)
	}
	set := keySet(got)
	if !set[live+"/SHARED_KEY"] {
		t.Fatalf("live config's key missing: %+v", got)
	}
	if set[deleted.ID+"/SHARED_KEY"] {
		t.Fatalf("soft-deleted config's key leaked into results: %+v", got)
	}
}

func TestSearchKeysExcludesRemovedKeyInLatestVersion(t *testing.T) {
	s := requireStore(t)
	resetDB(t)
	ctx := context.Background()
	_, _, cid := mkConfig(t, s, "prod")
	repo := NewSecretRepo(s)

	// v1: TEMP_KEY present. v2: TEMP_KEY deleted (tombstoned), OTHER_KEY added.
	saveKeys(t, s, cid, "TEMP_KEY")
	if _, err := repo.SaveConfigVersion(ctx, cid, []Change{
		{Key: "TEMP_KEY"},                 // delete
		{Key: "OTHER_KEY", Encrypt: set("v")}, // add
	}, "v2", "u"); err != nil {
		t.Fatal(err)
	}

	got, err := repo.SearchKeys(ctx, "KEY", 50)
	if err != nil {
		t.Fatal(err)
	}
	set := keySet(got)
	if set[cid+"/TEMP_KEY"] {
		t.Fatalf("removed key TEMP_KEY still returned from an older version: %+v", got)
	}
	if !set[cid+"/OTHER_KEY"] {
		t.Fatalf("live key OTHER_KEY missing: %+v", got)
	}
}

func TestSearchKeysRespectsLimit(t *testing.T) {
	s := requireStore(t)
	resetDB(t)
	ctx := context.Background()
	_, _, cid := mkConfig(t, s, "prod")
	saveKeys(t, s, cid, "AKEY1", "AKEY2", "AKEY3", "AKEY4", "AKEY5")

	got, err := NewSecretRepo(s).SearchKeys(ctx, "AKEY", 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("limit not respected: got %d, want 3 (%+v)", len(got), got)
	}
}

func TestSearchKeysLikeMetacharactersLiteral(t *testing.T) {
	s := requireStore(t)
	resetDB(t)
	ctx := context.Background()
	_, _, cid := mkConfig(t, s, "prod")
	// Keys literally containing % and _ , plus a decoy that a wildcard would hit.
	saveKeys(t, s, cid, "DISCOUNT_100%", "PLAIN", "a_b", "axb")
	repo := NewSecretRepo(s)

	// "100%" must match the literal key, not act as a "starts-with-100" wildcard.
	got, err := repo.SearchKeys(ctx, "100%", 50)
	if err != nil {
		t.Fatal(err)
	}
	set := keySet(got)
	if !set[cid+"/DISCOUNT_100%"] {
		t.Fatalf(`"100%%" did not match the literal key: %+v`, got)
	}
	if set[cid+"/PLAIN"] {
		t.Fatalf(`"100%%" over-matched PLAIN as a wildcard: %+v`, got)
	}

	// "a_b" must match only the literal "a_b", not "axb" (where _ = any char).
	got, err = repo.SearchKeys(ctx, "a_b", 50)
	if err != nil {
		t.Fatal(err)
	}
	set = keySet(got)
	if !set[cid+"/a_b"] {
		t.Fatalf(`"a_b" did not match the literal key: %+v`, got)
	}
	if set[cid+"/axb"] {
		t.Fatalf(`"a_b" over-matched axb (underscore treated as wildcard): %+v`, got)
	}
}

func TestSearchKeysEmptyQuery(t *testing.T) {
	s := requireStore(t)
	resetDB(t)
	ctx := context.Background()
	_, _, cid := mkConfig(t, s, "prod")
	saveKeys(t, s, cid, "SOME_KEY")
	repo := NewSecretRepo(s)

	for _, q := range []string{"", "   ", "\t"} {
		got, err := repo.SearchKeys(ctx, q, 50)
		if err != nil {
			t.Fatalf("q=%q: %v", q, err)
		}
		if len(got) != 0 {
			t.Fatalf("q=%q: want empty, got %+v", q, got)
		}
	}
}
