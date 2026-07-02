package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/binary"
	"io"
)

const (
	// KeySize is the size of all symmetric keys (AES-256).
	KeySize = 32
	// NonceSize is the standard GCM nonce size.
	NonceSize = 12
	// ciphertextFormatVersion is the serialization format version byte.
	ciphertextFormatVersion = 1
	// minMarshaledLen: format version (1) + key version (4) + nonce (12) + GCM tag (16).
	minMarshaledLen = 1 + 4 + NonceSize + 16
)

// Test injection points. Production code never reassigns these; tests
// override them to exercise otherwise-unreachable error branches.
var (
	randReader io.Reader = rand.Reader
	aeadForKey           = newAEAD
)

func newAEAD(key []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

// Ciphertext is an AES-256-GCM ciphertext with its nonce and the version of
// the key that encrypted it. Data includes the GCM tag.
type Ciphertext struct {
	KeyVersion uint32
	Nonce      []byte
	Data       []byte
}

// Encrypt seals plaintext under a 32-byte key with a fresh random nonce.
// aad is authenticated but not encrypted; Decrypt must present the same aad.
func Encrypt(key, plaintext, aad []byte) (Ciphertext, error) {
	if len(key) != KeySize {
		return Ciphertext{}, ErrInvalidKeySize
	}
	aead, err := aeadForKey(key)
	if err != nil {
		return Ciphertext{}, ErrInvalidKeySize
	}
	nonce := make([]byte, NonceSize)
	if _, err := io.ReadFull(randReader, nonce); err != nil {
		return Ciphertext{}, err
	}
	return Ciphertext{
		Nonce: nonce,
		Data:  aead.Seal(nil, nonce, plaintext, aad),
	}, nil
}

// Decrypt opens ct. Any failure — wrong key, wrong aad, tampering,
// malformed input — is reported as ErrDecryptFailed with no detail.
func Decrypt(key []byte, ct Ciphertext, aad []byte) ([]byte, error) {
	if len(key) != KeySize {
		return nil, ErrInvalidKeySize
	}
	if len(ct.Nonce) != NonceSize {
		return nil, ErrDecryptFailed
	}
	aead, err := aeadForKey(key)
	if err != nil {
		return nil, ErrDecryptFailed
	}
	plaintext, err := aead.Open(nil, ct.Nonce, ct.Data, aad)
	if err != nil {
		return nil, ErrDecryptFailed
	}
	return plaintext, nil
}

// Marshal encodes ct as: formatVersion(1) | keyVersion(4, big-endian) | nonce(12) | data.
func (c Ciphertext) Marshal() []byte {
	out := make([]byte, 0, 1+4+len(c.Nonce)+len(c.Data))
	out = append(out, ciphertextFormatVersion)
	out = binary.BigEndian.AppendUint32(out, c.KeyVersion)
	out = append(out, c.Nonce...)
	out = append(out, c.Data...)
	return out
}

// ParseCiphertext decodes a blob produced by Marshal. Malformed input is
// reported as ErrDecryptFailed (fail closed, no detail).
func ParseCiphertext(b []byte) (Ciphertext, error) {
	if len(b) < minMarshaledLen {
		return Ciphertext{}, ErrDecryptFailed
	}
	if b[0] != ciphertextFormatVersion {
		return Ciphertext{}, ErrDecryptFailed
	}
	return Ciphertext{
		KeyVersion: binary.BigEndian.Uint32(b[1:5]),
		Nonce:      append([]byte(nil), b[5:5+NonceSize]...),
		Data:       append([]byte(nil), b[5+NonceSize:]...),
	}, nil
}
