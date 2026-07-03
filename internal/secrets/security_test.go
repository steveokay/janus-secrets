package secrets

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// TestTamperedCiphertextIsDetected swaps one key's ciphertext for another's in
// the database; the AAD binding must make the decrypt fail with ErrDecrypt
// rather than return a wrong value.
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

	// Overwrite A's ciphertext/nonce with B's — A's DEK+AAD won't open B's data.
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

// TestSaveToSoftDeletedConfigConflicts verifies a write to a soft-deleted
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
func TestSaveToSoftDeletedConfigConflicts(t *testing.T) {
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
