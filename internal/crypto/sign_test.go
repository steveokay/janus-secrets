package crypto

import (
	"bytes"
	"testing"
)

func TestEd25519SignVerifyRoundTrip(t *testing.T) {
	pub, priv, err := GenerateEd25519Key()
	if err != nil {
		t.Fatal(err)
	}
	msg := []byte("transit message")
	sig := Sign(priv, msg)
	if !Verify(pub, msg, sig) {
		t.Fatal("valid signature must verify")
	}
	if Verify(pub, []byte("tampered"), sig) {
		t.Fatal("tampered message must not verify")
	}
	bad := make([]byte, len(sig))
	copy(bad, sig)
	bad[0] ^= 0xff
	if Verify(pub, msg, bad) {
		t.Fatal("tampered signature must not verify")
	}
}

func TestVerifyRejectsMalformedKey(t *testing.T) {
	if Verify([]byte("short"), []byte("m"), make([]byte, 64)) {
		t.Fatal("malformed public key must not verify")
	}
}

func TestGenerateEd25519KeyRandFailure(t *testing.T) {
	restore := randReader
	randReader = failReader{}
	defer func() { randReader = restore }()
	if _, _, err := GenerateEd25519Key(); err == nil {
		t.Fatal("expected error when the random source fails")
	}
}

func TestWrapUnwrapTransitKey(t *testing.T) {
	master, _ := GenerateKey()
	kr := NewKeyring()

	// Sealed keyring rejects both operations.
	if _, err := kr.WrapTransitKey([]byte("m"), "k", 1); err != ErrSealed {
		t.Fatalf("wrap while sealed: want ErrSealed, got %v", err)
	}
	if _, err := kr.UnwrapTransitKey(Ciphertext{}, "k", 1); err != ErrSealed {
		t.Fatalf("unwrap while sealed: want ErrSealed, got %v", err)
	}

	if err := kr.Unseal(master); err != nil {
		t.Fatal(err)
	}
	material := []byte("thirty-two-byte-key-material!!!!")
	ct, err := kr.WrapTransitKey(material, "billing", 2)
	if err != nil {
		t.Fatal(err)
	}
	got, err := kr.UnwrapTransitKey(ct, "billing", 2)
	if err != nil || !bytes.Equal(got, material) {
		t.Fatalf("round trip: %q %v", got, err)
	}
	// Wrong (name, version) → AEAD failure (anti-swap).
	if _, err := kr.UnwrapTransitKey(ct, "billing", 3); err == nil {
		t.Fatal("unwrap with wrong version must fail")
	}
	if _, err := kr.UnwrapTransitKey(ct, "other", 2); err == nil {
		t.Fatal("unwrap with wrong name must fail")
	}
}

func TestTransitKeyAADInjective(t *testing.T) {
	a := TransitKeyAAD("billing", 1)
	b := TransitKeyAAD("billing", 2)
	c := TransitKeyAAD("billin", 1) // different name, watch for delimiter collision
	if bytes.Equal(a, b) || bytes.Equal(a, c) || bytes.Equal(b, c) {
		t.Fatal("AAD must be injective across (name, version)")
	}
}
