package secrets

import (
	"errors"

	"github.com/steveokay/janus-secrets/internal/crypto"
	"github.com/steveokay/janus-secrets/internal/store"
)

var (
	// ErrSealed is returned when the keyring is sealed. The API layer maps this
	// to HTTP 503.
	ErrSealed = errors.New("secrets: server is sealed")
	// ErrNotFound is returned when a config, key, or version does not exist.
	ErrNotFound = errors.New("secrets: not found")
	// ErrConflict is returned when a write targets an absent/soft-deleted config.
	ErrConflict = errors.New("secrets: conflict")
	// ErrValidation is returned for invalid input (bad key name or slug).
	ErrValidation = errors.New("secrets: invalid input")
	// ErrDecrypt is returned when stored data is found but fails to decrypt —
	// an integrity signal (tampered/relocated ciphertext), never a missing key.
	// Its message carries no plaintext or key material.
	ErrDecrypt = errors.New("secrets: decryption failed")
)

// mapCryptoErr translates crypto sentinels into service sentinels.
func mapCryptoErr(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, crypto.ErrSealed):
		return ErrSealed
	case errors.Is(err, crypto.ErrDecryptFailed):
		return ErrDecrypt
	default:
		return err
	}
}

// mapStoreErr translates store sentinels into service sentinels.
func mapStoreErr(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, store.ErrNotFound):
		return ErrNotFound
	case errors.Is(err, store.ErrConflict):
		return ErrConflict
	default:
		return err
	}
}
