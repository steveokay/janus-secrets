package masterkeys

import (
	"context"
	"errors"
	"testing"

	"github.com/steveokay/janus-secrets/internal/crypto"
)

// TestKMSRotateRoundTrip is the end-to-end property for a KMS-unsealed instance:
// a secret written before rotation stays readable after Rotate re-wraps every
// project KEK under a fresh master and swaps the in-memory master, and the
// master-key version advances to 2.
func TestKMSRotateRoundTrip(t *testing.T) {
	h := newKMSHarness(t)
	ctx := context.Background()
	_, configID := h.mkChain(t)

	writeSecret(t, h.sec, configID, "DB", "s3cr3t")
	if got := reveal(t, h.sec, configID, "DB"); got != "s3cr3t" {
		t.Fatalf("pre-rotate DB = %q, want s3cr3t", got)
	}

	version, err := h.svc.Rotate(ctx)
	if err != nil {
		t.Fatalf("Rotate: %v", err)
	}
	if version != 2 {
		t.Fatalf("Rotate returned version %d, want 2", version)
	}

	// The secret is still readable: its DEK was re-wrapped under the new master's
	// project KEK and the swapped in-memory master decrypts it.
	if got := reveal(t, h.sec, configID, "DB"); got != "s3cr3t" {
		t.Fatalf("post-rotate DB = %q, want s3cr3t", got)
	}

	meta, err := h.svc.repo.GetMasterKeyMeta(ctx)
	if err != nil {
		t.Fatalf("GetMasterKeyMeta: %v", err)
	}
	if meta.Version != 2 {
		t.Fatalf("meta.Version = %d, want 2", meta.Version)
	}
}

// TestRotateNeverDecryptsValue proves rotation re-wraps KEKs only and never
// opens a secret value: even when the stored value ciphertext is corrupted,
// Rotate still succeeds.
func TestRotateNeverDecryptsValue(t *testing.T) {
	h := newKMSHarness(t)
	ctx := context.Background()
	_, configID := h.mkChain(t)

	writeSecret(t, h.sec, configID, "K", "value")

	// Corrupt the value ciphertext (not the wrapped_dek and not the KEK).
	if _, err := testPool.Exec(ctx,
		`UPDATE secret_values SET ciphertext = E'\\xdeadbeef'
		  WHERE config_id=$1::uuid AND key='K'`, configID); err != nil {
		t.Fatalf("corrupt ciphertext: %v", err)
	}

	version, err := h.svc.Rotate(ctx)
	if err != nil {
		t.Fatalf("Rotate must succeed despite corrupt value ciphertext: %v", err)
	}
	if version != 2 {
		t.Fatalf("Rotate returned version %d, want 2", version)
	}
}

// TestRotateSealed: a sealed keyring makes Rotate fail with crypto.ErrSealed
// (the seal guard fires inside performRotation before any crypto).
func TestRotateSealed(t *testing.T) {
	h := newKMSHarness(t)
	ctx := context.Background()

	h.kr.Seal()

	if _, err := h.svc.Rotate(ctx); !errors.Is(err, crypto.ErrSealed) {
		t.Fatalf("Rotate sealed = %v, want ErrSealed", err)
	}
}

// TestShamirRotateRejected: the single-call Rotate must refuse a Shamir-sealed
// instance and direct the operator to the rekey ceremony instead.
func TestShamirRotateRejected(t *testing.T) {
	h := newShamirHarness(t)
	ctx := context.Background()

	if _, err := h.svc.Rotate(ctx); !errors.Is(err, ErrShamirCeremonyRequired) {
		t.Fatalf("Rotate (shamir) = %v, want ErrShamirCeremonyRequired", err)
	}
}
