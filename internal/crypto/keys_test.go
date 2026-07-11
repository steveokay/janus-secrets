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

// TestAADInjective guards against delimiter-ambiguity collisions: the AAD
// encoding must be injective even when the user-influenced fields contain
// the delimiter characters. A colliding encoding would let a DEK bound to
// one (project, path) authenticate at a different location.
func TestAADInjective(t *testing.T) {
	dek := [][2][]byte{
		{DEKAAD("p1", "a:b", 1), DEKAAD("p1:a", "b", 1)},
		{DEKAAD("p", "x", 1), DEKAAD("p", "x:v1", 0)},
		{DEKAAD("a", "b", 1), DEKAAD("a:b", "", 1)},
	}
	for i, pair := range dek {
		if bytes.Equal(pair[0], pair[1]) {
			t.Fatalf("DEKAAD collision case %d: distinct locations share an AAD", i)
		}
	}

	if bytes.Equal(ProjectKEKAAD("a:b"), ProjectKEKAAD("a")) {
		t.Fatal("ProjectKEKAAD collision: distinct projects share an AAD")
	}
	// Cross-family separation: a KEK AAD must never equal a DEK AAD.
	if bytes.Equal(ProjectKEKAAD("p"), DEKAAD("p", "", 0)) {
		t.Fatal("KEK/DEK AAD families overlap")
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

func TestRotationAADs(t *testing.T) {
	// Config vs pending for the same policy must differ (distinct domains).
	if bytes.Equal(RotationConfigAAD("p1"), RotationPendingAAD("p1")) {
		t.Fatal("config and pending AAD must differ for the same policy")
	}
	// Different policies must differ (binding).
	if bytes.Equal(RotationConfigAAD("p1"), RotationConfigAAD("p2")) {
		t.Fatal("config AAD must bind to policy id")
	}
	// Injective over the id (length-prefix guard, mirrors DEKAAD design).
	if bytes.Equal(RotationConfigAAD("ab"), RotationConfigAAD("a\x00b")) {
		t.Fatal("AAD must be injective over policy id")
	}
}

func TestSyncCredsAAD(t *testing.T) {
	if bytes.Equal(SyncCredsAAD("t1"), SyncCredsAAD("t2")) {
		t.Fatal("SyncCredsAAD must bind to target id")
	}
	if bytes.Equal(SyncCredsAAD("ab"), SyncCredsAAD("a\x00b")) {
		t.Fatal("SyncCredsAAD must be injective over target id")
	}
	// distinct domain from rotation config AAD
	if bytes.Equal(SyncCredsAAD("x"), RotationConfigAAD("x")) {
		t.Fatal("sync creds AAD must differ from rotation config AAD")
	}
}

func TestDynamicConfigAADDomainSeparation(t *testing.T) {
	if bytes.Equal(DynamicConfigAAD("x"), SyncCredsAAD("x")) {
		t.Fatal("dynamic and sync AADs must differ")
	}
	if bytes.Equal(DynamicConfigAAD("x"), RotationConfigAAD("x")) {
		t.Fatal("dynamic and rotation AADs must differ")
	}
	if bytes.Equal(DynamicConfigAAD("r1"), DynamicConfigAAD("r2")) {
		t.Fatal("different role ids must yield different AADs")
	}
	if bytes.Equal(DynamicConfigAAD("ab"), DynamicConfigAAD("a\x00b")) {
		t.Fatal("length-prefixed encoding must resist boundary collisions")
	}
}
