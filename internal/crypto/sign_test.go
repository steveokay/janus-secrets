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

func TestTransitKeyAADInjective(t *testing.T) {
	a := TransitKeyAAD("billing", 1)
	b := TransitKeyAAD("billing", 2)
	c := TransitKeyAAD("billin", 1) // different name, watch for delimiter collision
	if bytes.Equal(a, b) || bytes.Equal(a, c) || bytes.Equal(b, c) {
		t.Fatal("AAD must be injective across (name, version)")
	}
}
