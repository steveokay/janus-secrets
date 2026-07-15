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
// rejected by Init (via shamir.Split), never persisted. 1-of-1 is a supported
// special case whose single share must be protected as the master key itself.
func NewShamirUnsealer(store SealConfigStore, shares, threshold int) *ShamirUnsealer {
	if shares == 0 && threshold == 0 {
		shares, threshold = DefaultShamirShares, DefaultShamirThreshold
	}
	return &ShamirUnsealer{store: store, shares: shares, threshold: threshold}
}

// Init generates the master key, splits it into shares, persists the seal
// config, and returns the shares exactly once. It assumes a single unsealer
// instance per store (a one-time bootstrap): concurrent Init across separate
// instances sharing one store is not guarded and last-write-wins.
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

	var parts [][]byte
	if s.shares == 1 && s.threshold == 1 {
		// Single-share seal (dev/simple deployments): the vendored shamir
		// library requires threshold >= 2, so — like Vault — the one share is
		// the master key itself. The KCV still rejects a wrong share at unseal.
		parts = [][]byte{append([]byte(nil), master...)}
	} else {
		parts, err = shamir.Split(master, s.shares, s.threshold)
		if err != nil {
			return nil, err
		}
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
// and cleared. On failure the shares are retained so the operator can submit
// more — but Combine consumes ALL submitted shares, so a single bad share
// poisons every attempt; call Reset to discard the accumulated shares and
// start over.
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
	var master []byte
	if cfg.Threshold == 1 {
		// Single-share seal: the share is the master-key candidate directly
		// (Combine requires >= 2 parts). More than one submitted share is
		// ambiguous — map order would make the outcome nondeterministic — so
		// fail closed; the operator resets and submits exactly one.
		if len(s.submitted) > 1 {
			return nil, ErrInvalidShare
		}
		master = append([]byte(nil), parts[0]...)
	} else {
		var cErr error
		master, cErr = shamir.Combine(parts)
		if cErr != nil {
			return nil, ErrInvalidShare
		}
	}
	// Redundant with verifyKCV (Decrypt rejects a non-KeySize key), but an
	// explicit, cheap fast path for a wrong-length reconstruction.
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

// SubmittedShares reports the count of accepted shares so far. Read-only,
// for status endpoints.
func (s *ShamirUnsealer) SubmittedShares() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.submitted)
}

// Reset discards all submitted shares, zeroizing the copies. Use it to
// recover from a mistyped or tampered share: because Unseal's Combine
// consumes every submitted share, one bad share otherwise fails every
// subsequent attempt. After Reset the operator resubmits from scratch.
func (s *ShamirUnsealer) Reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, p := range s.submitted {
		zero(p)
	}
	s.submitted = nil
}

// Reseal splits newMaster into fresh shares using the CURRENTLY STORED shape
// (threshold/shares from seal_config, not the constructor defaults) and builds
// a new KCV. It does not persist; the caller writes the returned config.
func (s *ShamirUnsealer) Reseal(ctx context.Context, newMaster []byte) (*SealConfig, [][]byte, error) {
	cfg, err := s.loadConfig(ctx)
	if err != nil {
		return nil, nil, err
	}
	if len(newMaster) != KeySize {
		return nil, nil, ErrInvalidKeySize
	}
	var parts [][]byte
	if cfg.Shares == 1 && cfg.Threshold == 1 {
		parts = [][]byte{append([]byte(nil), newMaster...)}
	} else {
		parts, err = shamir.Split(newMaster, cfg.Shares, cfg.Threshold)
		if err != nil {
			return nil, nil, err
		}
	}
	kcv, err := makeKCV(newMaster)
	if err != nil {
		return nil, nil, err
	}
	return &SealConfig{
		Type:          SealTypeShamir,
		Threshold:     cfg.Threshold,
		Shares:        cfg.Shares,
		KeyCheckValue: kcv,
	}, parts, nil
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
