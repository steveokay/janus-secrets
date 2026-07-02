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
