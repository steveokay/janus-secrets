package crypto

import (
	"bytes"
	"errors"
)

// testKey returns a deterministic 32-byte key filled with b.
func testKey(b byte) []byte { return bytes.Repeat([]byte{b}, KeySize) }

// failReader always errors. Used to force crypto/rand failure paths.
type failReader struct{}

func (failReader) Read([]byte) (int, error) { return 0, errors.New("simulated rand failure") }

// failAfterReader succeeds n times, then errors. Used to fail a later
// random read inside a multi-step operation (e.g. KCV creation after
// key generation succeeded).
type failAfterReader struct{ n int }

func (r *failAfterReader) Read(p []byte) (int, error) {
	if r.n <= 0 {
		return 0, errors.New("simulated rand failure")
	}
	r.n--
	for i := range p {
		p[i] = 0xAB
	}
	return len(p), nil
}
