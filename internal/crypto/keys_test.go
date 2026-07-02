package crypto

import (
	"bytes"
	"errors"
	"testing"
)

func TestGenerateKey(t *testing.T) {
	k1, err := GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	k2, err := GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	if len(k1) != KeySize || len(k2) != KeySize {
		t.Fatalf("key sizes = %d, %d; want %d", len(k1), len(k2), KeySize)
	}
	if bytes.Equal(k1, k2) {
		t.Fatal("two generated keys are identical")
	}
}

func TestGenerateKeyRandFailure(t *testing.T) {
	restore := randReader
	randReader = failReader{}
	defer func() { randReader = restore }()
	if _, err := GenerateKey(); err == nil {
		t.Fatal("want error, got nil")
	}
}

func TestWrapUnwrapKey(t *testing.T) {
	wrapping := testKey(0x01)
	material := testKey(0x02)
	aad := ProjectKEKAAD("proj-123")

	wrapped, err := WrapKey(wrapping, material, aad)
	if err != nil {
		t.Fatal(err)
	}
	got, err := UnwrapKey(wrapping, wrapped, aad)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, material) {
		t.Fatal("unwrap mismatch")
	}
}

func TestWrapKeyRejectsBadMaterial(t *testing.T) {
	if _, err := WrapKey(testKey(0x01), []byte("short"), nil); !errors.Is(err, ErrInvalidKeySize) {
		t.Fatalf("got %v, want ErrInvalidKeySize", err)
	}
}

func TestUnwrapKeyFailures(t *testing.T) {
	wrapping := testKey(0x01)
	wrapped, err := WrapKey(wrapping, testKey(0x02), ProjectKEKAAD("proj-a"))
	if err != nil {
		t.Fatal(err)
	}

	// AAD binding: a KEK wrapped for project A must not unwrap as project B.
	if _, err := UnwrapKey(wrapping, wrapped, ProjectKEKAAD("proj-b")); !errors.Is(err, ErrDecryptFailed) {
		t.Fatalf("cross-project unwrap: got %v, want ErrDecryptFailed", err)
	}

	// A valid decryption that yields non-key-sized material is rejected.
	notAKey, err := Encrypt(wrapping, []byte("only 16 bytes!!!"), nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := UnwrapKey(wrapping, notAKey, nil); !errors.Is(err, ErrDecryptFailed) {
		t.Fatalf("short unwrapped material: got %v, want ErrDecryptFailed", err)
	}
}

func TestAADHelpers(t *testing.T) {
	if bytes.Equal(ProjectKEKAAD("a"), ProjectKEKAAD("b")) {
		t.Fatal("ProjectKEKAAD not distinct per project")
	}
	a := DEKAAD("p1", "DB_URL", 1)
	tests := [][]byte{
		DEKAAD("p2", "DB_URL", 1),
		DEKAAD("p1", "API_KEY", 1),
		DEKAAD("p1", "DB_URL", 2),
	}
	for i, other := range tests {
		if bytes.Equal(a, other) {
			t.Fatalf("DEKAAD case %d not distinct", i)
		}
	}
}

func TestZero(t *testing.T) {
	b := []byte{1, 2, 3}
	zero(b)
	if !bytes.Equal(b, []byte{0, 0, 0}) {
		t.Fatal("zero did not clear bytes")
	}
	zero(nil) // must not panic
}
