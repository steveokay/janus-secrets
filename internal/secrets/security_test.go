package secrets

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// TestTamperedCiphertextIsDetected overwrites key A's ciphertext+nonce with key
// B's, leaving A's own wrapped_dek in place. A unwraps its own DEK fine, then
// AES-GCM fails to open B's ciphertext under A's DEK (the DEKs differ — each
// value gets a fresh DEK), so the read fails closed with ErrDecrypt rather than
// silently returning a wrong value. This exercises per-value DEK isolation +
// GCM integrity; the DEKAAD slot binding is proven separately by
// TestRelocatedValueRejectedByDEKAAD below.
func TestTamperedCiphertextIsDetected(t *testing.T) {
	s := newService(t)
	ctx := context.Background()
	_, configID := mkChain(t, s)

	if _, err := s.SetSecrets(ctx, configID, []SecretChange{
		{Key: "A", Value: []byte("value-a")},
		{Key: "B", Value: []byte("value-b")},
	}, "m", "u"); err != nil {
		t.Fatal(err)
	}

	// Overwrite A's ciphertext/nonce with B's; A's own DEK cannot open B's data.
	if _, err := testPool.Exec(ctx,
		`UPDATE secret_values a
		 SET ciphertext = b.ciphertext, nonce = b.nonce
		 FROM secret_values b
		 WHERE a.config_id = $1::uuid AND a.key = 'A'
		   AND b.config_id = $1::uuid AND b.key = 'B'`, configID); err != nil {
		t.Fatal(err)
	}

	if _, err := s.GetSecret(ctx, configID, "A"); !errors.Is(err, ErrDecrypt) {
		t.Fatalf("tampered A: got %v, want ErrDecrypt", err)
	}
}

// TestRelocatedValueRejectedByDEKAAD relocates key B's ENTIRE crypto triple
// (wrapped_dek + ciphertext + nonce) into key A's row, so A now stores a DEK
// that would decrypt B's value. This is the case where the DEKAAD slot binding
// is load-bearing: reading A builds DEKAAD(project, config/"A", version) and
// tries to unwrap the relocated DEK, but that DEK was wrapped under
// DEKAAD(project, config/"B", version). The path components differ, so the DEK
// unwrap fails and the read returns ErrDecrypt. Without the AAD binding, A would
// unwrap B's DEK and return B's plaintext ("value-b") — so this test would fail
// if the slot binding were removed, making it a genuine guard on that property.
func TestRelocatedValueRejectedByDEKAAD(t *testing.T) {
	s := newService(t)
	ctx := context.Background()
	_, configID := mkChain(t, s)

	if _, err := s.SetSecrets(ctx, configID, []SecretChange{
		{Key: "A", Value: []byte("value-a")},
		{Key: "B", Value: []byte("value-b")},
	}, "m", "u"); err != nil {
		t.Fatal(err)
	}

	// Move B's whole crypto triple into A's row (A and B share value_version 1,
	// so only the key/path component of the DEKAAD differs).
	if _, err := testPool.Exec(ctx,
		`UPDATE secret_values a
		 SET wrapped_dek = b.wrapped_dek, ciphertext = b.ciphertext, nonce = b.nonce
		 FROM secret_values b
		 WHERE a.config_id = $1::uuid AND a.key = 'A'
		   AND b.config_id = $1::uuid AND b.key = 'B'`, configID); err != nil {
		t.Fatal(err)
	}

	if _, err := s.GetSecret(ctx, configID, "A"); !errors.Is(err, ErrDecrypt) {
		t.Fatalf("relocated B->A: got %v, want ErrDecrypt (DEKAAD slot binding must reject)", err)
	}
}

// TestSaveToSoftDeletedConfigRejected verifies a write to a soft-deleted
// config is rejected rather than silently applied.
//
// The plan assumed this surfaces the store's ErrConflict (SaveConfigVersion
// returns ErrConflict for an absent/soft-deleted config). In practice
// SetSecrets first resolves the config via resolveProject -> configs.Get, and
// Get filters "deleted_at IS NULL", so a soft-deleted config short-circuits
// with ErrNotFound before SaveConfigVersion runs. Either sentinel proves the
// security property (the write is rejected); the observable behavior is
// ErrNotFound, so that is what we assert. No production change: the store's
// ErrConflict branch remains the belt-and-suspenders guard against a
// soft-delete racing between the Get and the save.
func TestSaveToSoftDeletedConfigRejected(t *testing.T) {
	s := newService(t)
	ctx := context.Background()
	_, configID := mkChain(t, s)
	if err := s.configs.SoftDelete(ctx, configID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetSecrets(ctx, configID, []SecretChange{
		{Key: "K", Value: []byte("v")},
	}, "m", "u"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("save to deleted config: got %v, want ErrNotFound", err)
	}
}

// TestNoPlaintextInErrors drives error paths with a distinctive secret value
// and asserts the plaintext never appears in any returned error string.
func TestNoPlaintextInErrors(t *testing.T) {
	s := newService(t)
	ctx := context.Background()
	_, configID := mkChain(t, s)

	const marker = "PLAINTEXT-LEAK-CANARY-9271"
	if _, err := s.SetSecrets(ctx, configID, []SecretChange{
		{Key: "SECRET", Value: []byte(marker)},
	}, "m", "u"); err != nil {
		t.Fatal(err)
	}

	// Tamper so decrypt fails, then collect errors from several operations.
	if _, err := testPool.Exec(ctx,
		`UPDATE secret_values SET nonce = decode('000000000000000000000000','hex')
		 WHERE config_id = $1::uuid AND key = 'SECRET'`, configID); err != nil {
		t.Fatal(err)
	}

	var errs []error
	if _, err := s.GetSecret(ctx, configID, "SECRET"); err != nil {
		errs = append(errs, err)
	}
	if _, _, err := s.RevealConfig(ctx, configID); err != nil {
		errs = append(errs, err)
	}
	if _, err := s.GetSecret(ctx, configID, "MISSING"); err != nil {
		errs = append(errs, err)
	}
	if _, err := s.SetSecrets(ctx, configID, []SecretChange{{Key: "bad-key", Value: []byte(marker)}}, "m", "u"); err != nil {
		errs = append(errs, err)
	}

	if len(errs) == 0 {
		t.Fatal("expected at least one error to inspect")
	}
	for _, err := range errs {
		if strings.Contains(err.Error(), marker) {
			t.Fatalf("plaintext leaked in error: %v", err)
		}
	}
}

// TestReadPathSealedReturnsErrSealed covers the read path when the keyring is
// sealed after a successful unsealed write: both GetSecret and RevealConfig
// must return ErrSealed (the write path was already covered elsewhere).
func TestReadPathSealedReturnsErrSealed(t *testing.T) {
	s := newService(t)
	ctx := context.Background()
	_, configID := mkChain(t, s)

	if _, err := s.SetSecrets(ctx, configID, []SecretChange{
		{Key: "K", Value: []byte("v")},
	}, "m", "u"); err != nil {
		t.Fatal(err)
	}

	s.keyring.Seal()

	if _, err := s.GetSecret(ctx, configID, "K"); !errors.Is(err, ErrSealed) {
		t.Fatalf("GetSecret sealed: got %v, want ErrSealed", err)
	}
	if _, _, err := s.RevealConfig(ctx, configID); !errors.Is(err, ErrSealed) {
		t.Fatalf("RevealConfig sealed: got %v, want ErrSealed", err)
	}
}

// TestGetSecretVersionAbsentReturnsNotFound covers the version-absent
// early-return path in GetSecretVersion (before any KEK unwrap): a key that
// exists but at a version that was never written must yield ErrNotFound.
func TestGetSecretVersionAbsentReturnsNotFound(t *testing.T) {
	s := newService(t)
	ctx := context.Background()
	_, configID := mkChain(t, s)

	if _, err := s.SetSecrets(ctx, configID, []SecretChange{
		{Key: "A", Value: []byte("a1")},
	}, "m", "u"); err != nil {
		t.Fatal(err)
	}

	if _, err := s.GetSecretVersion(ctx, configID, "A", 99); !errors.Is(err, ErrNotFound) {
		t.Fatalf("absent version: got %v, want ErrNotFound", err)
	}
}
