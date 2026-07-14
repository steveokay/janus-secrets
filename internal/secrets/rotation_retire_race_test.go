package secrets

import (
	"bytes"
	"context"
	"testing"

	"github.com/steveokay/janus-secrets/internal/crypto"
)

// TestReadRecoversFromRewrapRetireRace reproduces the narrow TOCTOU where a
// concurrent KEK rewrap advances a value's DEK to the latest version AND retires
// the old superseded KEK version in the window between a reader snapshotting the
// row and the reader resolving that row's KEK.
//
// To hit the race the reader's PROJECT snapshot must already be at the newer KEK
// version (post-rotate, v2) while the value it snapshotted is still wrapped under
// the OLDER version (v1, not yet rewrapped). Resolving v1 then goes to
// project_kek_versions — and if a concurrent rewrap retired v1 in the meantime,
// that lookup 404s. The read path must detect the retire, re-read the row +
// project fresh, and still return the original plaintext.
//
// Before the retire-race fix this fails: forVersion(1) 404s and the read errors
// instead of recovering.
func TestReadRecoversFromRewrapRetireRace(t *testing.T) {
	s := newService(t)
	ctx := context.Background()
	projectID, configID := mkChain(t, s)

	// Write "DB"="s3cr3t" — lands at dek_key_version=1, wrapped under KEK v1.
	if _, err := s.SetSecrets(ctx, configID, []SecretChange{
		{Key: "DB", Value: []byte("s3cr3t")},
	}, "initial", "alice"); err != nil {
		t.Fatal(err)
	}

	// Rotate the project to KEK v2 (store level), preserving v1 in
	// project_kek_versions. The secret's DEK stays at v1 (NOT yet rewrapped).
	proj1, err := s.projects.Get(ctx, projectID)
	if err != nil {
		t.Fatal(err)
	}
	oldWrappedKEK := append([]byte(nil), proj1.WrappedKEK...)
	newKEK, err := crypto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	newKEKCT, err := s.keyring.WrapProjectKEK(newKEK, projectID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := testPool.Exec(ctx,
		`INSERT INTO project_kek_versions (project_id, version, wrapped_kek) VALUES ($1::uuid, 1, $2)`,
		projectID, oldWrappedKEK); err != nil {
		t.Fatalf("insert superseded KEK v1: %v", err)
	}
	if _, err := testPool.Exec(ctx,
		`UPDATE projects SET wrapped_kek=$2, kek_version=2 WHERE id=$1::uuid`,
		projectID, newKEKCT.Marshal()); err != nil {
		t.Fatalf("advance project to KEK v2: %v", err)
	}

	// The reader's snapshot: project at v2 (fresh) but the value still at DEK v1.
	// This is exactly the window in which resolving the value's KEK hits
	// project_kek_versions rather than proj.WrappedKEK.
	cfg, projSnap, err := s.resolveProject(ctx, configID)
	if err != nil {
		t.Fatal(err)
	}
	if projSnap.KEKVersion != 2 {
		t.Fatalf("precondition: reader project KEK version = %d, want 2", projSnap.KEKVersion)
	}
	_, state, err := s.secrets.GetLatest(ctx, cfg.ID)
	if err != nil {
		t.Fatal(err)
	}
	svStale, ok := state["DB"]
	if !ok || svStale.DEKKeyVersion != 1 {
		t.Fatalf("precondition: DB dek_key_version = %d (found=%v), want 1", svStale.DEKKeyVersion, ok)
	}

	// --- Concurrent rewrap+retire lands after the snapshot, before resolution ---

	// Rewrap the row: re-wrap the DEK from v1 KEK onto v2 KEK under the same AAD
	// (value ciphertext untouched), advancing the row to dek_key_version=2.
	aad, err := dekAAD(projectID, cfg.ID+"/DB", svStale.ValueVersion)
	if err != nil {
		t.Fatal(err)
	}
	oldKEK, err := s.keyring.UnwrapProjectKEK(mustParseCT(t, oldWrappedKEK), projectID)
	if err != nil {
		t.Fatal(err)
	}
	dek, err := crypto.UnwrapKey(oldKEK, mustParseCT(t, svStale.WrappedDEK), aad)
	if err != nil {
		t.Fatal(err)
	}
	newDEKCT, err := crypto.WrapKey(newKEK, dek, aad)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := testPool.Exec(ctx,
		`UPDATE secret_values SET wrapped_dek=$2, dek_key_version=2 WHERE id=$1::uuid`,
		svStale.ID, newDEKCT.Marshal()); err != nil {
		t.Fatalf("rewrap row to v2: %v", err)
	}
	// Retire: DeleteEmpty removes the now-unreferenced v1 KEK version.
	if _, err := testPool.Exec(ctx,
		`DELETE FROM project_kek_versions WHERE project_id=$1::uuid AND version=1`, projectID); err != nil {
		t.Fatalf("retire KEK v1: %v", err)
	}

	// --- The stale reader resolves now: v1 is gone, but the read must recover ---
	res := s.newKEKResolver(projSnap)
	defer res.zero()
	pt, err := s.decryptValue(ctx, projSnap, cfg.ID, svStale, res)
	if err != nil {
		t.Fatalf("decryptValue did not recover from retire race: %v", err)
	}
	if !bytes.Equal(pt, []byte("s3cr3t")) {
		t.Fatalf("recovered value = %q, want s3cr3t", pt)
	}

	// And a normal fresh read still works (row now at v2, decrypted via latest KEK).
	got, err := s.GetSecret(ctx, configID, "DB")
	if err != nil {
		t.Fatalf("fresh GetSecret after rewrap: %v", err)
	}
	if !bytes.Equal(got.Value, []byte("s3cr3t")) {
		t.Fatalf("fresh DB = %q, want s3cr3t", got.Value)
	}
}

func mustParseCT(t *testing.T, b []byte) crypto.Ciphertext {
	t.Helper()
	ct, err := crypto.ParseCiphertext(b)
	if err != nil {
		t.Fatalf("parse ciphertext: %v", err)
	}
	return ct
}
