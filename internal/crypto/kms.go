package crypto

import (
	"context"
	"errors"
)

// KMSClient is the minimal contract for a cloud KMS used for auto-unseal.
// The production implementation is AWSKMSClient; tests use a fake.
//
// Decrypt must return freshly allocated plaintext that the caller owns and
// may zeroize; it must not alias the ciphertext argument or any long-lived
// buffer (callers wipe the returned key material after use).
type KMSClient interface {
	Encrypt(ctx context.Context, plaintext []byte) ([]byte, error)
	Decrypt(ctx context.Context, ciphertext []byte) ([]byte, error)
}

// KMSUnsealer implements Unsealer via a cloud KMS: the master key is
// generated locally, stored wrapped by the KMS key, and recovered with a
// single Decrypt call at startup — no operator interaction.
type KMSUnsealer struct {
	store  SealConfigStore
	client KMSClient
}

func NewKMSUnsealer(store SealConfigStore, client KMSClient) *KMSUnsealer {
	return &KMSUnsealer{store: store, client: client}
}

// Init generates the master key, wraps it via the KMS, and persists the seal
// config. Unlike ShamirUnsealer.Init it holds no mutex: KMSUnsealer carries no
// mutable in-struct state (no accumulated shares), so concurrency is delegated
// to the store, and Init is a one-time bootstrap. Same single-instance
// assumption applies.
func (u *KMSUnsealer) Init(ctx context.Context) (*InitResult, error) {
	_, err := u.store.Get(ctx)
	if err == nil {
		return nil, ErrAlreadyInitialized
	}
	if !errors.Is(err, ErrNoSealConfig) {
		return nil, err
	}

	master, err := GenerateKey()
	if err != nil {
		return nil, err
	}
	defer zero(master)

	wrapped, err := u.client.Encrypt(ctx, master)
	if err != nil {
		return nil, err
	}
	kcv, err := makeKCV(master)
	if err != nil {
		return nil, err
	}
	cfg := &SealConfig{
		Type:             SealTypeAWSKMS,
		KeyCheckValue:    kcv,
		WrappedMasterKey: wrapped,
	}
	if err := u.store.Put(ctx, cfg); err != nil {
		return nil, err
	}
	return &InitResult{}, nil
}

// Reseal wraps newMaster under KMS and builds a new KCV. No operator shares.
func (u *KMSUnsealer) Reseal(ctx context.Context, newMaster []byte) (*SealConfig, [][]byte, error) {
	if len(newMaster) != KeySize {
		return nil, nil, ErrInvalidKeySize
	}
	wrapped, err := u.client.Encrypt(ctx, newMaster)
	if err != nil {
		return nil, nil, err
	}
	kcv, err := makeKCV(newMaster)
	if err != nil {
		return nil, nil, err
	}
	return &SealConfig{
		Type:             SealTypeAWSKMS,
		KeyCheckValue:    kcv,
		WrappedMasterKey: wrapped,
	}, nil, nil
}

func (u *KMSUnsealer) Unseal(ctx context.Context) ([]byte, error) {
	cfg, err := u.store.Get(ctx)
	if err != nil {
		return nil, err
	}
	if cfg.Type != SealTypeAWSKMS {
		return nil, ErrInvalidSealConfig
	}
	master, err := u.client.Decrypt(ctx, cfg.WrappedMasterKey)
	if err != nil {
		return nil, err
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
