// Package crypto implements Keyhaven's envelope encryption: AES-256-GCM
// primitives, key wrapping with AAD binding, the in-memory keyring, and
// the Shamir and AWS KMS unseal mechanisms.
//
// Error discipline: no key material, plaintext, or share bytes ever appear
// in an error message. Callers get one of the sentinel errors below.
package crypto

import "errors"

var (
	ErrSealed             = errors.New("keyring is sealed")
	ErrAlreadyUnsealed    = errors.New("keyring is already unsealed")
	ErrInvalidKeySize     = errors.New("invalid key size")
	ErrDecryptFailed      = errors.New("decryption failed")
	ErrInvalidShare       = errors.New("invalid share")
	ErrDuplicateShare     = errors.New("duplicate share")
	ErrNotEnoughShares    = errors.New("not enough shares")
	ErrKeyCheckFailed     = errors.New("key check value mismatch")
	ErrNoSealConfig       = errors.New("seal configuration not found")
	ErrAlreadyInitialized = errors.New("seal already initialized")
	ErrInvalidSealConfig  = errors.New("seal configuration type mismatch")
)
