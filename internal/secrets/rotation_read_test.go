package secrets

import (
	"bytes"
	"context"
	"testing"

	"github.com/steveokay/janus-secrets/internal/crypto"
)

// TestRotationReadUsesSupersededKEK simulates a project-KEK rotation that has
// NOT yet rewrapped an existing secret's DEK: the secret's WrappedDEK is still
// wrapped under project-KEK v1, while projects.kek_version has advanced to v2
// (with a brand-new wrapped_kek). The old v1 wrapped KEK lives in
// project_kek_versions. The read path must resolve the correct KEK for the
// value's dek_key_version (v1, from project_kek_versions) rather than only the
// latest project KEK, so the reveal still returns the original plaintext.
//
// Before the rotation-aware read change this fails with ErrDecrypt (the read
// unwrapped only the latest KEK, which cannot unwrap a v1-wrapped DEK).
func TestRotationReadUsesSupersededKEK(t *testing.T) {
	s := newService(t)
	ctx := context.Background()
	projectID, configID := mkChain(t, s)

	// Write "DB"="s3cr3t" — lands at dek_key_version=1, wrapped under KEK v1.
	if _, err := s.SetSecrets(ctx, configID, []SecretChange{
		{Key: "DB", Value: []byte("s3cr3t")},
	}, "initial", "alice"); err != nil {
		t.Fatal(err)
	}

	// Capture the project's current (v1) wrapped KEK so we can stash it as the
	// superseded version before overwriting the row with a fresh v2 KEK.
	proj, err := s.projects.Get(ctx, projectID)
	if err != nil {
		t.Fatal(err)
	}
	if proj.KEKVersion != 1 {
		t.Fatalf("precondition: project KEK version = %d, want 1", proj.KEKVersion)
	}
	oldWrapped := append([]byte(nil), proj.WrappedKEK...)

	// Simulate a rotate WITHOUT rewrap, at the store level: generate a fresh
	// project KEK, wrap it under the master, INSERT the old (v1) wrapped KEK
	// into project_kek_versions, then advance projects to the new v2 KEK.
	newKEK, err := crypto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	newCT, err := s.keyring.WrapProjectKEK(newKEK, projectID)
	if err != nil {
		t.Fatal(err)
	}
	newWrapped := newCT.Marshal()

	if _, err := testPool.Exec(ctx,
		`INSERT INTO project_kek_versions (project_id, version, wrapped_kek) VALUES ($1::uuid, $2, $3)`,
		projectID, 1, oldWrapped); err != nil {
		t.Fatalf("insert superseded KEK v1: %v", err)
	}
	if _, err := testPool.Exec(ctx,
		`UPDATE projects SET wrapped_kek=$2, kek_version=2 WHERE id=$1::uuid`,
		projectID, newWrapped); err != nil {
		t.Fatalf("advance project to KEK v2: %v", err)
	}

	// Sanity: the stored secret's DEK is still at version 1 (not rewrapped).
	if _, state, err := s.secrets.GetLatest(ctx, configID); err != nil {
		t.Fatal(err)
	} else if sv, ok := state["DB"]; !ok || sv.DEKKeyVersion != 1 {
		t.Fatalf("DB dek_key_version = %d (found=%v), want 1", sv.DEKKeyVersion, ok)
	}

	// The read path must unwrap KEK v1 from project_kek_versions and still
	// return the original plaintext.
	got, err := s.GetSecret(ctx, configID, "DB")
	if err != nil {
		t.Fatalf("GetSecret after unrewrapped rotate: %v", err)
	}
	if !bytes.Equal(got.Value, []byte("s3cr3t")) {
		t.Fatalf("DB = %q, want s3cr3t", got.Value)
	}

	// RevealConfig exercises the same resolver over the whole config.
	if _, all, err := s.RevealConfig(ctx, configID); err != nil {
		t.Fatalf("RevealConfig after unrewrapped rotate: %v", err)
	} else if !bytes.Equal(all["DB"].Value, []byte("s3cr3t")) {
		t.Fatalf("RevealConfig DB = %q, want s3cr3t", all["DB"].Value)
	}
}
