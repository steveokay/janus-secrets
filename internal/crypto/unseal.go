package crypto

import (
	"context"
	"crypto/subtle"
)

// Seal types recorded in SealConfig.
const (
	SealTypeShamir = "shamir"
	SealTypeAWSKMS = "awskms"
)

// Unsealer recovers the master key at startup. Implementations: Shamir
// (interactive share submission via the concrete *ShamirUnsealer) and
// AWS KMS (fully automatic).
type Unsealer interface {
	// Init generates a new master key on first boot and persists seal
	// metadata. Returns ErrAlreadyInitialized if seal config already exists.
	Init(ctx context.Context) (*InitResult, error)
	// Unseal recovers and verifies the master key. The caller feeds it to
	// Keyring.Unseal and then zeroes the returned slice.
	Unseal(ctx context.Context) ([]byte, error)
}

// InitResult is what Init hands back to the operator exactly once.
type InitResult struct {
	// Shares are the Shamir key shares. Nil for KMS-based seals.
	Shares [][]byte
}

// SealConfig is the persisted, non-secret seal metadata. The key check
// value and wrapped master key are ciphertexts, safe at rest.
type SealConfig struct {
	Type             string `json:"type"`
	Threshold        int    `json:"threshold,omitempty"`
	Shares           int    `json:"shares,omitempty"`
	KeyCheckValue    []byte `json:"key_check_value"`
	WrappedMasterKey []byte `json:"wrapped_master_key,omitempty"`
}

// SealConfigStore persists SealConfig. Get returns ErrNoSealConfig when
// nothing has been initialized yet.
type SealConfigStore interface {
	Get(ctx context.Context) (*SealConfig, error)
	Put(ctx context.Context, cfg *SealConfig) error
}

// The key check value is a known constant encrypted under the master key at
// Init. Verifying it on unseal rejects a wrong-but-well-formed master key
// (e.g. a Shamir reconstruction from a wrong share) before it is used.
var (
	kcvPlaintext = []byte("keyhaven-key-check-v1")
	kcvAAD       = []byte("keyhaven:kcv")
)

func makeKCV(master []byte) ([]byte, error) {
	ct, err := Encrypt(master, kcvPlaintext, kcvAAD)
	if err != nil {
		return nil, err
	}
	return ct.Marshal(), nil
}

func verifyKCV(master, kcv []byte) error {
	ct, err := ParseCiphertext(kcv)
	if err != nil {
		return ErrKeyCheckFailed
	}
	got, err := Decrypt(master, ct, kcvAAD)
	if err != nil {
		return ErrKeyCheckFailed
	}
	if subtle.ConstantTimeCompare(got, kcvPlaintext) != 1 {
		return ErrKeyCheckFailed
	}
	return nil
}
