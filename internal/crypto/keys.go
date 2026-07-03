package crypto

import (
	"encoding/binary"
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

// AAD field encoding is length-prefixed so it is injective over
// user-influenced fields: each variable field is preceded by its 8-byte
// big-endian length, so no combination of field contents (including the
// delimiter-like ':' characters) can produce a colliding AAD. The length is
// a uint64 widened from a non-negative len(), so the prefix can represent any
// possible slice length without overflow.
//
// The encoding is a binding format baked into stored ciphertext; changing
// it is a breaking change that requires re-wrapping every affected key.
func appendField(b []byte, field string) []byte {
	b = binary.BigEndian.AppendUint64(b, uint64(len(field)))
	return append(b, field...)
}

// ProjectKEKAAD binds a wrapped project KEK to its project. A KEK ciphertext
// copied onto another project's row will fail to unwrap.
func ProjectKEKAAD(projectID string) []byte {
	return appendField([]byte("janus:kek:project"), projectID)
}

// DEKAAD binds a wrapped DEK to a project, secret path, and value version.
func DEKAAD(projectID, secretPath string, version uint64) []byte {
	b := []byte("janus:dek")
	b = appendField(b, projectID)
	b = appendField(b, secretPath)
	return binary.BigEndian.AppendUint64(b, version)
}

// zero overwrites b with zeros. Best-effort in Go: the GC may have copied
// the bytes; this narrows the window, it does not guarantee erasure.
func zero(b []byte) {
	for i := range b {
		b[i] = 0
	}
}
