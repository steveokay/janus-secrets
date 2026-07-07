package crypto

import "sync"

// Keyring holds the master key in memory after unseal. It is the only
// stateful component in this package. All operations on a sealed keyring
// return ErrSealed (the API layer maps this to HTTP 503).
type Keyring struct {
	mu     sync.RWMutex
	master []byte // nil iff sealed
}

func NewKeyring() *Keyring { return &Keyring{} }

// Unseal installs the master key. The slice is copied; the caller should
// zero its copy afterwards.
func (k *Keyring) Unseal(master []byte) error {
	if len(master) != KeySize {
		return ErrInvalidKeySize
	}
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.master != nil {
		return ErrAlreadyUnsealed
	}
	k.master = append([]byte(nil), master...)
	return nil
}

// Seal zeroizes the master key (best-effort) and returns to the sealed state.
func (k *Keyring) Seal() {
	k.mu.Lock()
	defer k.mu.Unlock()
	zero(k.master)
	k.master = nil
}

func (k *Keyring) Sealed() bool {
	k.mu.RLock()
	defer k.mu.RUnlock()
	return k.master == nil
}

// WrapProjectKEK wraps a project KEK under the master key, bound to projectID.
//
// TODO(rotation): stamp the master-key version into the returned Ciphertext
// so master-key rotation can identify which version wrapped each KEK for
// lazy re-wrap. Encrypt currently leaves KeyVersion == 0.
func (k *Keyring) WrapProjectKEK(kek []byte, projectID string) (Ciphertext, error) {
	k.mu.RLock()
	defer k.mu.RUnlock()
	if k.master == nil {
		return Ciphertext{}, ErrSealed
	}
	return WrapKey(k.master, kek, ProjectKEKAAD(projectID))
}

// UnwrapProjectKEK unwraps a project KEK previously wrapped for projectID.
func (k *Keyring) UnwrapProjectKEK(ct Ciphertext, projectID string) ([]byte, error) {
	k.mu.RLock()
	defer k.mu.RUnlock()
	if k.master == nil {
		return nil, ErrSealed
	}
	return UnwrapKey(k.master, ct, ProjectKEKAAD(projectID))
}

// WrapAuthKey wraps the token-HMAC key under the master key.
func (k *Keyring) WrapAuthKey(key []byte) (Ciphertext, error) {
	k.mu.RLock()
	defer k.mu.RUnlock()
	if k.master == nil {
		return Ciphertext{}, ErrSealed
	}
	return WrapKey(k.master, key, AuthKeyAAD())
}

// UnwrapAuthKey unwraps the token-HMAC key previously wrapped by WrapAuthKey.
func (k *Keyring) UnwrapAuthKey(ct Ciphertext) ([]byte, error) {
	k.mu.RLock()
	defer k.mu.RUnlock()
	if k.master == nil {
		return nil, ErrSealed
	}
	return UnwrapKey(k.master, ct, AuthKeyAAD())
}

// WrapOIDCClientSecret encrypts an OIDC provider client secret (arbitrary
// length) under the master key. Unlike WrapAuthKey it does not require
// 32-byte input, so it calls Encrypt directly rather than WrapKey.
func (k *Keyring) WrapOIDCClientSecret(secret []byte) (Ciphertext, error) {
	k.mu.RLock()
	defer k.mu.RUnlock()
	if k.master == nil {
		return Ciphertext{}, ErrSealed
	}
	return Encrypt(k.master, secret, OIDCClientSecretAAD())
}

// UnwrapOIDCClientSecret decrypts a secret wrapped by WrapOIDCClientSecret.
func (k *Keyring) UnwrapOIDCClientSecret(ct Ciphertext) ([]byte, error) {
	k.mu.RLock()
	defer k.mu.RUnlock()
	if k.master == nil {
		return nil, ErrSealed
	}
	return Decrypt(k.master, ct, OIDCClientSecretAAD())
}

// NewDEK generates a fresh DEK and wraps it under projectKEK in one call,
// minimizing the plaintext DEK's lifetime. Refuses to run while sealed.
//
// The returned plaintext DEK is the caller's to zero after use.
func (k *Keyring) NewDEK(projectKEK, aad []byte) ([]byte, Ciphertext, error) {
	k.mu.RLock()
	defer k.mu.RUnlock()
	if k.master == nil {
		return nil, Ciphertext{}, ErrSealed
	}
	dek, err := GenerateKey()
	if err != nil {
		return nil, Ciphertext{}, err
	}
	wrapped, err := WrapKey(projectKEK, dek, aad)
	if err != nil {
		zero(dek)
		return nil, Ciphertext{}, err
	}
	return dek, wrapped, nil
}
