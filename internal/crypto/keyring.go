package crypto

import (
	"crypto/hmac"
	"crypto/sha256"
	"sync"
)

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
// Master-key rotation (see Keyring.RotateMaster) re-wraps eagerly and
// atomically, so ciphertext carries no master-key version (KeyVersion == 0).
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

// RotateMaster swaps the master key to newMaster, re-wrapping caller-supplied
// blobs from the old key to the new one. It holds the write lock for the whole
// operation so no concurrent unwrap observes a half-rotated master.
//
// rewrap receives two closures bound to the old (unwrap) and new (wrap) master:
// it must re-encrypt every master-wrapped blob and stage the new ciphertext for
// persist. persist then commits those new ciphertexts plus the re-seal metadata
// in a single DB transaction. Only if BOTH succeed is the in-memory master
// swapped and the old key zeroized; if either fails the old master is retained
// unchanged. newMaster is copied; the caller zeroes its copy.
//
// unwrap/wrap use Encrypt/Decrypt directly so they handle both 32-byte keys and
// arbitrary-length blobs (e.g. OIDC client secrets). AAD must be byte-identical
// to each blob's read path.
func (k *Keyring) RotateMaster(
	newMaster []byte,
	rewrap func(unwrap func(oldCT, aad []byte) (plain []byte, err error),
		wrap func(plain, aad []byte) (newCT []byte, err error)) error,
	persist func() error,
) error {
	if len(newMaster) != KeySize {
		return ErrInvalidKeySize
	}
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.master == nil {
		return ErrSealed
	}
	nm := append([]byte(nil), newMaster...)

	unwrap := func(oldCT, aad []byte) ([]byte, error) {
		ct, err := ParseCiphertext(oldCT)
		if err != nil {
			return nil, ErrDecryptFailed
		}
		return Decrypt(k.master, ct, aad)
	}
	wrap := func(plain, aad []byte) ([]byte, error) {
		ct, err := Encrypt(nm, plain, aad)
		if err != nil {
			return nil, err
		}
		return ct.Marshal(), nil
	}
	if err := rewrap(unwrap, wrap); err != nil {
		zero(nm)
		return err
	}
	if err := persist(); err != nil {
		zero(nm)
		return err
	}
	zero(k.master)
	k.master = nm
	return nil
}

// SyncFingerprint returns HMAC-SHA256 over data, keyed by a subkey derived from
// the master key with a fixed domain label, so the value stored for sync change
// detection is not a reversible hash of secret material. Returns nil while
// sealed (no master in memory).
func (k *Keyring) SyncFingerprint(data []byte) []byte {
	k.mu.RLock()
	defer k.mu.RUnlock()
	if k.master == nil {
		return nil
	}
	sub := hmac.New(sha256.New, k.master)
	sub.Write([]byte("janus:sync:fingerprint-key"))
	mac := hmac.New(sha256.New, sub.Sum(nil))
	mac.Write(data)
	return mac.Sum(nil)
}
