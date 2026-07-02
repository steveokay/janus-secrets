package crypto

import (
	"fmt"
	"io"
)

// GenerateKey returns 32 bytes of cryptographically random key material.
func GenerateKey() ([]byte, error) {
	key := make([]byte, KeySize)
	if _, err := io.ReadFull(randReader, key); err != nil {
		return nil, err
	}
	return key, nil
}

// WrapKey encrypts 32-byte key material under wrappingKey. aad binds the
// wrapped key to its storage location (see ProjectKEKAAD / DEKAAD).
func WrapKey(wrappingKey, keyMaterial, aad []byte) (Ciphertext, error) {
	if len(keyMaterial) != KeySize {
		return Ciphertext{}, ErrInvalidKeySize
	}
	return Encrypt(wrappingKey, keyMaterial, aad)
}

// UnwrapKey decrypts a wrapped key and verifies the result is key-sized.
func UnwrapKey(wrappingKey []byte, ct Ciphertext, aad []byte) ([]byte, error) {
	key, err := Decrypt(wrappingKey, ct, aad)
	if err != nil {
		return nil, err
	}
	if len(key) != KeySize {
		zero(key)
		return nil, ErrDecryptFailed
	}
	return key, nil
}

// ProjectKEKAAD binds a wrapped project KEK to its project. A KEK ciphertext
// copied onto another project's row will fail to unwrap.
func ProjectKEKAAD(projectID string) []byte {
	return []byte("keyhaven:kek:project:" + projectID)
}

// DEKAAD binds a wrapped DEK to a project, secret path, and value version.
func DEKAAD(projectID, secretPath string, version uint64) []byte {
	return []byte(fmt.Sprintf("keyhaven:dek:%s:%s:v%d", projectID, secretPath, version))
}

// zero overwrites b with zeros. Best-effort in Go: the GC may have copied
// the bytes; this narrows the window, it does not guarantee erasure.
func zero(b []byte) {
	for i := range b {
		b[i] = 0
	}
}
