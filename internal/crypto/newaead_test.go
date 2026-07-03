package crypto

import "testing"

// TestNewAEADBadKey covers newAEAD's aes.NewCipher error branch, which the
// public API can't reach (Encrypt/Decrypt validate the 32-byte key first)
// and which the aeadForKey injection var bypasses in other tests.
func TestNewAEADBadKey(t *testing.T) {
	if _, err := newAEAD([]byte("too short")); err == nil {
		t.Fatal("newAEAD with a 9-byte key: want error, got nil")
	}
}
