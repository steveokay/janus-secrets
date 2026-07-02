package crypto

import (
	"context"
	"encoding/hex"
	"errors"
	"sync"

	"github.com/steveokay/janus-secrets/internal/crypto/shamir"
)

const (
	DefaultShamirShares    = 5
	DefaultShamirThreshold = 3
)

// ShamirUnsealer implements Unsealer with k-of-n secret sharing. Share
// submission is interactive: callers hold the concrete type and call
// SubmitShare until the threshold is reached, then Unseal.
type ShamirUnsealer struct {
	store     SealConfigStore
	shares    int
	threshold int

	mu        sync.Mutex
	submitted map[string][]byte
}

// Progress reports how many shares have been accepted so far.
type Progress struct {
	Submitted int
	Required  int
}

// NewShamirUnsealer creates a Shamir unsealer. shares/threshold are used by
// Init; passing 0, 0 selects the 3-of-5 default. Invalid combinations are
// rejected by Init (via shamir.Split), never persisted.
func NewShamirUnsealer(store SealConfigStore, shares, threshold int) *ShamirUnsealer {
	if shares == 0 && threshold == 0 {
		shares, threshold = DefaultShamirShares, DefaultShamirThreshold
	}
	return &ShamirUnsealer{store: store, shares: shares, threshold: threshold}
}

func (s *ShamirUnsealer) Init(ctx context.Context) (*InitResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	_, err := s.store.Get(ctx)
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

	parts, err := shamir.Split(master, s.shares, s.threshold)
	if err != nil {
		return nil, err
	}
	kcv, err := makeKCV(master)
	if err != nil {
		return nil, err
	}
	cfg := &SealConfig{
		Type:          SealTypeShamir,
		Threshold:     s.threshold,
		Shares:        s.shares,
		KeyCheckValue: kcv,
	}
	if err := s.store.Put(ctx, cfg); err != nil {
		return nil, err
	}
	return &InitResult{Shares: parts}, nil
}

// SubmitShare accepts one share and reports progress toward the threshold.
func (s *ShamirUnsealer) SubmitShare(ctx context.Context, share []byte) (Progress, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	cfg, err := s.loadConfig(ctx)
	if err != nil {
		return Progress{}, err
	}
	if len(share) < 2 {
		return Progress{}, ErrInvalidShare
	}
	key := hex.EncodeToString(share)
	if s.submitted == nil {
		s.submitted = make(map[string][]byte)
	}
	if _, ok := s.submitted[key]; ok {
		return Progress{Submitted: len(s.submitted), Required: cfg.Threshold}, ErrDuplicateShare
	}
	s.submitted[key] = append([]byte(nil), share...)
	return Progress{Submitted: len(s.submitted), Required: cfg.Threshold}, nil
}

// Unseal reconstructs the master key from submitted shares and verifies it
// against the key check value. On success the submitted shares are zeroized
// and cleared.
func (s *ShamirUnsealer) Unseal(ctx context.Context) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	cfg, err := s.loadConfig(ctx)
	if err != nil {
		return nil, err
	}
	if len(s.submitted) < cfg.Threshold {
		return nil, ErrNotEnoughShares
	}
	parts := make([][]byte, 0, len(s.submitted))
	for _, p := range s.submitted {
		parts = append(parts, p)
	}
	master, err := shamir.Combine(parts)
	if err != nil {
		return nil, ErrInvalidShare
	}
	if len(master) != KeySize {
		zero(master)
		return nil, ErrKeyCheckFailed
	}
	if err := verifyKCV(master, cfg.KeyCheckValue); err != nil {
		zero(master)
		return nil, err
	}
	for _, p := range s.submitted {
		zero(p)
	}
	s.submitted = nil
	return master, nil
}

func (s *ShamirUnsealer) loadConfig(ctx context.Context) (*SealConfig, error) {
	cfg, err := s.store.Get(ctx)
	if err != nil {
		return nil, err
	}
	if cfg.Type != SealTypeShamir {
		return nil, ErrInvalidSealConfig
	}
	return cfg, nil
}
