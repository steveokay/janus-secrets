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

// AuthKeyAAD binds the wrapped token-HMAC key to the auth domain, so it can
// never be confused with a project KEK or any other wrapped key.
func AuthKeyAAD() []byte {
	return []byte("janus:auth:token-hmac")
}

// OIDCClientSecretAAD binds a wrapped OIDC client secret to the auth domain so
// it can never be confused with a project KEK, the token-HMAC key, or any other
// wrapped value.
func OIDCClientSecretAAD() []byte {
	return []byte("janus:auth:oidc-client-secret")
}

// RotationConfigAAD binds a rotation policy's encrypted rotator-config blob
// (admin DSN, webhook HMAC key) to its policy. A blob copied onto another
// policy's row fails to decrypt. Mirrors DEKAAD's length-prefixed encoding.
func RotationConfigAAD(policyID string) []byte {
	return appendField([]byte("janus:rotation:config"), policyID)
}

// RotationPendingAAD binds a rotation policy's encrypted pending value (the
// generated-but-not-yet-committed new secret value) to its policy, in a domain
// distinct from RotationConfigAAD so the two ciphertext slots can never be
// swapped.
func RotationPendingAAD(policyID string) []byte {
	return appendField([]byte("janus:rotation:pending"), policyID)
}

// SyncCredsAAD binds a sync target's encrypted credentials blob (GitHub PAT or
// k8s token/CA) to its target, in a domain distinct from rotation AADs.
func SyncCredsAAD(targetID string) []byte {
	return appendField([]byte("janus:sync:creds"), targetID)
}

// DynamicConfigAAD binds a dynamic role's encrypted RoleConfig blob (admin DSN,
// creation/revocation/renew SQL) to its role id, in a domain distinct from the
// rotation and sync AADs.
func DynamicConfigAAD(roleID string) []byte {
	return appendField([]byte("janus:dynamic:config"), roleID)
}

// NotificationChannelAAD binds a notification channel's master-wrapped config
// blob (destination URL + optional HMAC signing key) to its channel id, in a
// domain distinct from every other AAD, so a config row copied to another
// channel fails to unwrap.
func NotificationChannelAAD(channelID string) []byte {
	return appendField([]byte("janus:notification:channel"), channelID)
}

// TOTPSecretAAD binds a user's master-wrapped TOTP shared secret to their user
// id, in a domain distinct from every other AAD, so a secret row copied to
// another user fails to unwrap.
func TOTPSecretAAD(userID string) []byte {
	return appendField([]byte("janus:auth:totp-secret"), userID)
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
