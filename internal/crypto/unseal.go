package crypto

import (
	"context"
	"crypto/subtle"

	"github.com/steveokay/janus-secrets/internal/crypto/shamir"
)

// Seal types recorded in SealConfig.
const (
	SealTypeShamir  = "shamir"
	SealTypeAWSKMS  = "awskms"
	SealTypeGCPKMS  = "gcpkms"
	SealTypeAzureKV = "azurekv"
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
	// Reseal produces seal metadata (fresh KCV) for newMaster without changing
	// the seal shape. Shamir returns new shares; KMS returns nil shares. It does
	// NOT persist — the caller writes the returned SealConfig transactionally.
	Reseal(ctx context.Context, newMaster []byte) (*SealConfig, [][]byte, error)
}

// ReconstructAndVerifyShamir rebuilds a master key from submitted shares and
// verifies it against cfg's key check value. Used by the rekey ceremony to
// prove possession of >= threshold current shares. Returns ErrNotEnoughShares
// below threshold and ErrKeyCheckFailed / ErrInvalidShare on a wrong share.
// The returned key is the caller's to zero.
func ReconstructAndVerifyShamir(cfg *SealConfig, shares [][]byte) ([]byte, error) {
	if cfg == nil || cfg.Type != SealTypeShamir {
		return nil, ErrInvalidSealConfig
	}
	if len(shares) < cfg.Threshold {
		return nil, ErrNotEnoughShares
	}
	var master []byte
	if cfg.Threshold == 1 {
		if len(shares) != 1 {
			return nil, ErrInvalidShare
		}
		master = append([]byte(nil), shares[0]...)
	} else {
		m, err := shamir.Combine(shares)
		if err != nil {
			return nil, ErrInvalidShare
		}
		master = m
	}
	if len(master) != KeySize {
		zero(master)
		return nil, ErrKeyCheckFailed
	}
	if err := verifyKCV(master, cfg.KeyCheckValue); err != nil {
		zero(master)
		return nil, err
	}
	return master, nil
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
	kcvPlaintext = []byte("janus-key-check-v1")
	kcvAAD       = []byte("janus:kcv")
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
	// The real key check is GCM authentication above: a wrong master fails
	// Decrypt. This compare is defense-in-depth against a well-formed KCV of
	// a different plaintext (e.g. a botched format migration). kcvPlaintext
	// is a public constant, so the constant-time compare is for consistency,
	// not secrecy.
	if subtle.ConstantTimeCompare(got, kcvPlaintext) != 1 {
		return ErrKeyCheckFailed
	}
	return nil
}
