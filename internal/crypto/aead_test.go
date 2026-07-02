package crypto

import (
	"bytes"
	"crypto/cipher"
	"errors"
	"testing"
)

func TestEncryptDecryptRoundTrip(t *testing.T) {
	key := testKey(0x42)
	tests := []struct {
		name      string
		plaintext []byte
		aad       []byte
	}{
		{"basic", []byte("secret value"), []byte("aad")},
		{"empty plaintext", nil, []byte("aad")},
		{"nil aad", []byte("secret"), nil},
		{"large plaintext", bytes.Repeat([]byte("x"), 1<<16), []byte("aad")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ct, err := Encrypt(key, tt.plaintext, tt.aad)
			if err != nil {
				t.Fatalf("Encrypt: %v", err)
			}
			if len(ct.Nonce) != NonceSize {
				t.Fatalf("nonce length = %d, want %d", len(ct.Nonce), NonceSize)
			}
			got, err := Decrypt(key, ct, tt.aad)
			if err != nil {
				t.Fatalf("Decrypt: %v", err)
			}
			if !bytes.Equal(got, tt.plaintext) {
				t.Fatal("round trip mismatch")
			}
		})
	}
}

func TestDecryptFailures(t *testing.T) {
	key := testKey(0x42)
	aad := []byte("aad")
	ct, err := Encrypt(key, []byte("payload"), aad)
	if err != nil {
		t.Fatal(err)
	}

	// flip returns a deep copy of ct with f applied.
	flip := func(f func(*Ciphertext)) Ciphertext {
		c := Ciphertext{
			KeyVersion: ct.KeyVersion,
			Nonce:      append([]byte(nil), ct.Nonce...),
			Data:       append([]byte(nil), ct.Data...),
		}
		f(&c)
		return c
	}

	tests := []struct {
		name    string
		key     []byte
		ct      Ciphertext
		aad     []byte
		wantErr error
	}{
		{"wrong key", testKey(0x43), ct, aad, ErrDecryptFailed},
		{"wrong aad", key, ct, []byte("other"), ErrDecryptFailed},
		{"nonce bit flip", key, flip(func(c *Ciphertext) { c.Nonce[0] ^= 1 }), aad, ErrDecryptFailed},
		{"body bit flip", key, flip(func(c *Ciphertext) { c.Data[0] ^= 1 }), aad, ErrDecryptFailed},
		{"tag bit flip", key, flip(func(c *Ciphertext) { c.Data[len(c.Data)-1] ^= 1 }), aad, ErrDecryptFailed},
		{"truncated data", key, flip(func(c *Ciphertext) { c.Data = c.Data[:8] }), aad, ErrDecryptFailed},
		{"empty data", key, flip(func(c *Ciphertext) { c.Data = nil }), aad, ErrDecryptFailed},
		{"bad nonce length", key, flip(func(c *Ciphertext) { c.Nonce = c.Nonce[:4] }), aad, ErrDecryptFailed},
		{"bad key size", testKey(0x42)[:16], ct, aad, ErrInvalidKeySize},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := Decrypt(tt.key, tt.ct, tt.aad); !errors.Is(err, tt.wantErr) {
				t.Fatalf("got %v, want %v", err, tt.wantErr)
			}
		})
	}
}

func TestEncryptFailures(t *testing.T) {
	if _, err := Encrypt(make([]byte, 16), []byte("p"), nil); !errors.Is(err, ErrInvalidKeySize) {
		t.Fatalf("short key: got %v, want ErrInvalidKeySize", err)
	}

	restore := randReader
	randReader = failReader{}
	defer func() { randReader = restore }()
	if _, err := Encrypt(testKey(0x42), []byte("p"), nil); err == nil {
		t.Fatal("rand failure: want error, got nil")
	}
}

func TestAEADConstructorFailure(t *testing.T) {
	restore := aeadForKey
	aeadForKey = func([]byte) (cipher.AEAD, error) { return nil, errors.New("boom") }
	defer func() { aeadForKey = restore }()

	if _, err := Encrypt(testKey(0x42), []byte("p"), nil); !errors.Is(err, ErrInvalidKeySize) {
		t.Fatalf("Encrypt: got %v, want ErrInvalidKeySize", err)
	}
	if _, err := Decrypt(testKey(0x42), Ciphertext{Nonce: make([]byte, NonceSize), Data: []byte("x")}, nil); !errors.Is(err, ErrDecryptFailed) {
		t.Fatalf("Decrypt: got %v, want ErrDecryptFailed", err)
	}
}

func TestNonceUniqueness(t *testing.T) {
	const n = 100_000
	key := testKey(0x42)
	seen := make(map[[NonceSize]byte]struct{}, n)
	for i := 0; i < n; i++ {
		ct, err := Encrypt(key, nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		var nonce [NonceSize]byte
		copy(nonce[:], ct.Nonce)
		if _, dup := seen[nonce]; dup {
			t.Fatal("nonce collision detected")
		}
		seen[nonce] = struct{}{}
	}
}

func TestSamePlaintextDifferentCiphertext(t *testing.T) {
	key := testKey(0x42)
	a, err := Encrypt(key, []byte("same"), nil)
	if err != nil {
		t.Fatal(err)
	}
	b, err := Encrypt(key, []byte("same"), nil)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(a.Data, b.Data) {
		t.Fatal("two encryptions of identical plaintext produced identical ciphertext")
	}
}

func TestCiphertextMarshalRoundTrip(t *testing.T) {
	key := testKey(0x42)
	ct, err := Encrypt(key, []byte("payload"), []byte("aad"))
	if err != nil {
		t.Fatal(err)
	}
	ct.KeyVersion = 7

	parsed, err := ParseCiphertext(ct.Marshal())
	if err != nil {
		t.Fatal(err)
	}
	if parsed.KeyVersion != 7 {
		t.Fatalf("KeyVersion = %d, want 7", parsed.KeyVersion)
	}
	if !bytes.Equal(parsed.Nonce, ct.Nonce) || !bytes.Equal(parsed.Data, ct.Data) {
		t.Fatal("marshal round trip mismatch")
	}
	got, err := Decrypt(key, parsed, []byte("aad"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, []byte("payload")) {
		t.Fatal("decrypt after parse mismatch")
	}
}

func TestParseCiphertextFailures(t *testing.T) {
	key := testKey(0x42)
	ct, err := Encrypt(key, []byte("payload"), nil)
	if err != nil {
		t.Fatal(err)
	}
	valid := ct.Marshal()

	badVersion := append([]byte(nil), valid...)
	badVersion[0] = 0xFF

	tests := []struct {
		name string
		in   []byte
	}{
		{"nil", nil},
		{"empty", []byte{}},
		{"too short", valid[:10]},
		{"unknown format version", badVersion},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := ParseCiphertext(tt.in); !errors.Is(err, ErrDecryptFailed) {
				t.Fatalf("got %v, want ErrDecryptFailed", err)
			}
		})
	}
}
