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

// TestRekeyCeremonyHappyPath: a 3-of-5 Shamir ceremony reaches threshold with
// the current shares, verifies possession, rotates the master, and returns the
// new shares once. The OLD shares no longer reconstruct against the stored cfg;
// the NEW shares do.
func TestRekeyCeremonyHappyPath(t *testing.T) {
	h := newShamirCeremonyHarness(t)
	ctx := context.Background()

	nonce, required, err := h.svc.RekeyInit(ctx)
	if err != nil {
		t.Fatalf("RekeyInit: %v", err)
	}
	if required != 3 {
		t.Fatalf("required = %d, want 3", required)
	}

	var newShares [][]byte
	var version int
	for i := 0; i < required; i++ {
		complete, ns, ver, submitted, req, serr := h.svc.RekeySubmit(ctx, nonce, h.shares[i])
		if serr != nil {
			t.Fatalf("RekeySubmit %d: %v", i, serr)
		}
		if req != 3 {
			t.Fatalf("submit %d: required = %d, want 3", i, req)
		}
		if i < required-1 {
			if complete {
				t.Fatalf("submit %d: complete=true before threshold", i)
			}
			if submitted != i+1 {
				t.Fatalf("submit %d: submitted = %d, want %d", i, submitted, i+1)
			}
			continue
		}
		// final submit completes
		if !complete {
			t.Fatalf("final submit: complete=false, want true")
		}
		newShares, version = ns, ver
	}

	if version != 2 {
		t.Fatalf("version = %d, want 2", version)
	}
	if len(newShares) != 5 {
		t.Fatalf("len(newShares) = %d, want 5", len(newShares))
	}

	// Ceremony closed after completion.
	st, err := h.svc.Status(ctx)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if st.RekeyInProg {
		t.Fatalf("RekeyInProg = true after completion, want false")
	}

	cfg, err := h.seals.Get(ctx)
	if err != nil {
		t.Fatalf("seals.Get: %v", err)
	}
	// OLD shares no longer reconstruct+verify against the new stored cfg.
	if _, err := crypto.ReconstructAndVerifyShamir(cfg, h.shares[:3]); err == nil {
		t.Fatalf("old shares still verify against new cfg, want failure")
	}
	// NEW shares reconstruct+verify against the new stored cfg.
	if _, err := crypto.ReconstructAndVerifyShamir(cfg, newShares[:3]); err != nil {
		t.Fatalf("new shares fail to verify against new cfg: %v", err)
	}
}

// TestRekeyRejectsWrongShares: submitting (threshold-1) valid shares plus one
// tampered share on the completing submit fails possession, performs NO
// rotation (version stays 1), and closes the ceremony.
func TestRekeyRejectsWrongShares(t *testing.T) {
	h := newShamirCeremonyHarness(t)
	ctx := context.Background()

	nonce, required, err := h.svc.RekeyInit(ctx)
	if err != nil {
		t.Fatalf("RekeyInit: %v", err)
	}

	// threshold-1 valid shares.
	for i := 0; i < required-1; i++ {
		if _, _, _, _, _, serr := h.svc.RekeySubmit(ctx, nonce, h.shares[i]); serr != nil {
			t.Fatalf("RekeySubmit %d: %v", i, serr)
		}
	}
	// Tamper a distinct valid share so it's accepted into the set but poisons
	// reconstruction.
	bad := append([]byte(nil), h.shares[required-1]...)
	bad[len(bad)-1] ^= 0xFF
	complete, _, _, _, _, serr := h.svc.RekeySubmit(ctx, nonce, bad)
	if serr == nil {
		t.Fatalf("completing submit with tampered share: err=nil, want possession failure")
	}
	if complete {
		t.Fatalf("completing submit with tampered share: complete=true, want false")
	}

	// No rotation: version stays 1.
	meta, err := h.svc.repo.GetMasterKeyMeta(ctx)
	if err != nil {
		t.Fatalf("GetMasterKeyMeta: %v", err)
	}
	if meta.Version != 1 {
		t.Fatalf("version = %d after failed possession, want 1", meta.Version)
	}

	// Ceremony closed.
	st, err := h.svc.Status(ctx)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if st.RekeyInProg {
		t.Fatalf("RekeyInProg = true after failed possession, want false")
	}
}

// TestRekeyOnlyOneCeremony: a second RekeyInit before the first finishes returns
// ErrRekeyInProgress.
func TestRekeyOnlyOneCeremony(t *testing.T) {
	h := newShamirCeremonyHarness(t)
	ctx := context.Background()

	if _, _, err := h.svc.RekeyInit(ctx); err != nil {
		t.Fatalf("first RekeyInit: %v", err)
	}
	if _, _, err := h.svc.RekeyInit(ctx); !errors.Is(err, ErrRekeyInProgress) {
		t.Fatalf("second RekeyInit = %v, want ErrRekeyInProgress", err)
	}
}

// TestRekeyCancelClearsState: cancel after one submit resets progress.
func TestRekeyCancelClearsState(t *testing.T) {
	h := newShamirCeremonyHarness(t)
	ctx := context.Background()

	nonce, _, err := h.svc.RekeyInit(ctx)
	if err != nil {
		t.Fatalf("RekeyInit: %v", err)
	}
	if _, _, _, _, _, serr := h.svc.RekeySubmit(ctx, nonce, h.shares[0]); serr != nil {
		t.Fatalf("RekeySubmit: %v", serr)
	}
	if err := h.svc.RekeyCancel(); err != nil {
		t.Fatalf("RekeyCancel: %v", err)
	}
	st, err := h.svc.Status(ctx)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if st.RekeyInProg {
		t.Fatalf("RekeyInProg = true after cancel, want false")
	}
	if st.Submitted != 0 {
		t.Fatalf("Submitted = %d after cancel, want 0", st.Submitted)
	}
}

// TestRekeyRequiresShamir: a KMS-sealed instance has no ceremony.
func TestRekeyRequiresShamir(t *testing.T) {
	h := newKMSHarness(t)
	ctx := context.Background()

	if _, _, err := h.svc.RekeyInit(ctx); !errors.Is(err, ErrKMSNoCeremony) {
		t.Fatalf("RekeyInit (kms) = %v, want ErrKMSNoCeremony", err)
	}
}

// TestRekeySealed: a sealed keyring rejects RekeyInit with ErrSealed.
func TestRekeySealed(t *testing.T) {
	h := newShamirCeremonyHarness(t)
	ctx := context.Background()

	h.kr.Seal()

	if _, _, err := h.svc.RekeyInit(ctx); !errors.Is(err, crypto.ErrSealed) {
		t.Fatalf("RekeyInit sealed = %v, want ErrSealed", err)
	}
}
