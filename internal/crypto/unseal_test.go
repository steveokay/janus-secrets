package crypto

import (
	"errors"
	"testing"
)

func TestKCVRoundTrip(t *testing.T) {
	master := testKey(0xAA)
	kcv, err := makeKCV(master)
	if err != nil {
		t.Fatal(err)
	}
	if err := verifyKCV(master, kcv); err != nil {
		t.Fatal(err)
	}
}

func TestKCVFailures(t *testing.T) {
	master := testKey(0xAA)
	kcv, err := makeKCV(master)
	if err != nil {
		t.Fatal(err)
	}

	tampered := append([]byte(nil), kcv...)
	tampered[len(tampered)-1] ^= 1

	tests := []struct {
		name   string
		master []byte
		kcv    []byte
	}{
		{"wrong master", testKey(0xAB), kcv},
		{"tampered kcv", master, tampered},
		{"garbage kcv", master, []byte("not a ciphertext")},
		{"nil kcv", master, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := verifyKCV(tt.master, tt.kcv); !errors.Is(err, ErrKeyCheckFailed) {
				t.Fatalf("got %v, want ErrKeyCheckFailed", err)
			}
		})
	}
}

func TestMakeKCVRandFailure(t *testing.T) {
	restore := randReader
	randReader = failReader{}
	defer func() { randReader = restore }()
	if _, err := makeKCV(testKey(0xAA)); err == nil {
		t.Fatal("want error, got nil")
	}
}

// TestVerifyKCVWrongPlaintext reaches the constant-time-compare branch: a
// KCV that is a well-formed ciphertext under the correct key and AAD but
// wraps a DIFFERENT plaintext must be rejected. GCM authentication passes
// here, so only the payload compare can catch it.
func TestVerifyKCVWrongPlaintext(t *testing.T) {
	master := testKey(0xAA)
	other, err := Encrypt(master, []byte("janus-key-check-v2"), kcvAAD)
	if err != nil {
		t.Fatal(err)
	}
	if err := verifyKCV(master, other.Marshal()); !errors.Is(err, ErrKeyCheckFailed) {
		t.Fatalf("valid ciphertext of wrong plaintext: got %v, want ErrKeyCheckFailed", err)
	}
}
