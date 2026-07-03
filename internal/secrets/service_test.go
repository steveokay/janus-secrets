package secrets

import (
	"bytes"
	"testing"
)

func TestZeroize(t *testing.T) {
	b := []byte("super-secret-bytes")
	zeroize(b)
	if !bytes.Equal(b, make([]byte, len(b))) {
		t.Fatalf("zeroize left non-zero bytes: %v", b)
	}
}
