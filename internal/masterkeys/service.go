// Package masterkeys orchestrates rotation of the root master key. Rotation
// mints a fresh master, re-wraps every master-wrapped blob (project KEKs,
// superseded KEK versions, the auth token-HMAC key, OIDC client secrets, and
// transit key material) under it, and re-seals — all in one DB transaction —
// then swaps the in-memory master. It NEVER decrypts a secret value: only
// 32-byte KEKs and other master-wrapped key material pass through.
//
// KMS-unsealed instances rotate in a single call (Rotate). Shamir instances
// require an interactive rekey ceremony (a separate task); Rotate rejects them
// with ErrShamirCeremonyRequired.
package masterkeys

import (
	"context"
	"encoding/hex"
	"errors"
	"sync"
	"time"

	"github.com/steveokay/janus-secrets/internal/crypto"
	"github.com/steveokay/janus-secrets/internal/store"
)

// Unsealer is the subset of crypto.Unsealer the service needs. Both
// *crypto.ShamirUnsealer and *crypto.KMSUnsealer satisfy it.
type Unsealer interface {
	Reseal(ctx context.Context, newMaster []byte) (*crypto.SealConfig, [][]byte, error)
}

type Service struct {
	kr       *crypto.Keyring
	unsealer Unsealer
	repo     *store.MasterKeyRepo
	seals    crypto.SealConfigStore
	rekey    rekeyState // placeholder; filled by the ceremony task
}

func NewService(kr *crypto.Keyring, u Unsealer, repo *store.MasterKeyRepo, seals crypto.SealConfigStore) *Service {
	return &Service{kr: kr, unsealer: u, repo: repo, seals: seals}
}

var ErrShamirCeremonyRequired = errors.New("shamir seal requires a rekey ceremony")

type Status struct {
	UnsealType  string
	Version     int
	RotatedAt   *time.Time
	RekeyInProg bool
	Submitted   int
	Required    int
}

func zero(b []byte) {
	for i := range b {
		b[i] = 0
	}
}

func zeroShares(ss [][]byte) {
	for _, s := range ss {
		zero(s)
	}
}

// performRotation is the shared core for both KMS and Shamir. It mints a fresh
// master, re-wraps every master-wrapped blob and writes the new seal config
// atomically (one DB transaction inside RotateMaster's persist), then swaps the
// in-memory master. Returns new shares (Shamir) or nil (KMS) and the new
// master-key version.
func (s *Service) performRotation(ctx context.Context) ([][]byte, int, error) {
	if s.kr.Sealed() {
		return nil, 0, crypto.ErrSealed
	}
	m2, err := crypto.GenerateKey()
	if err != nil {
		return nil, 0, err
	}
	defer zero(m2)

	var unwrapFn func(old, aad []byte) ([]byte, error)
	var wrapFn func(plain, aad []byte) ([]byte, error)
	var newShares [][]byte

	err = s.kr.RotateMaster(m2,
		func(unwrap func(old, aad []byte) ([]byte, error), wrap func(plain, aad []byte) ([]byte, error)) error {
			unwrapFn, wrapFn = unwrap, wrap
			return nil
		},
		func() error {
			return s.repo.RewrapAllUnderNewMaster(ctx,
				func(old, aad []byte) ([]byte, error) {
					pt, e := unwrapFn(old, aad)
					if e != nil {
						return nil, e
					}
					defer zero(pt)
					return wrapFn(pt, aad)
				},
				func() (*crypto.SealConfig, error) {
					cfg, sh, e := s.unsealer.Reseal(ctx, m2)
					if e != nil {
						return nil, e
					}
					newShares = sh
					return cfg, nil
				})
		},
	)
	if err != nil {
		// RotateMaster failed: rotation rolled back, in-memory master unchanged.
		// The new shares are meaningless — discard them.
		zeroShares(newShares)
		return nil, 0, err
	}
	// Rotation committed and the in-memory master is swapped. The shares are now
	// the operator's only way back in, so they MUST survive even if the
	// display-only version read below fails — never discard them past this point.
	meta, merr := s.repo.GetMasterKeyMeta(ctx)
	if merr != nil {
		return newShares, 0, nil
	}
	return newShares, meta.Version, nil
}

// Rotate performs a single-call rotation for KMS-unsealed instances. Shamir
// instances must use the rekey ceremony (T6) and Rotate returns
// ErrShamirCeremonyRequired.
func (s *Service) Rotate(ctx context.Context) (int, error) {
	cfg, err := s.seals.Get(ctx)
	if err != nil {
		return 0, err
	}
	if cfg.Type != crypto.SealTypeAWSKMS {
		return 0, ErrShamirCeremonyRequired
	}
	_, version, err := s.performRotation(ctx)
	return version, err
}

// Status reports the current seal type + master-key version/rotated_at + any
// in-progress ceremony progress (zero for KMS).
func (s *Service) Status(ctx context.Context) (Status, error) {
	meta, err := s.repo.GetMasterKeyMeta(ctx)
	if err != nil {
		return Status{}, err
	}
	st := Status{UnsealType: meta.SealType, Version: meta.Version, RotatedAt: meta.RotatedAt}
	s.rekey.fill(&st)
	return st, nil
}

// rekeyState holds the single in-progress Shamir rekey ceremony. Only one
// ceremony may be open at a time; submitted shares accumulate (deduped) until
// the threshold is reached, at which point possession is verified and the
// rotation runs exactly once.
type rekeyState struct {
	mu        sync.Mutex
	active    bool
	nonce     string
	required  int
	submitted map[string][]byte
}

func (r *rekeyState) fill(st *Status) {
	r.mu.Lock()
	defer r.mu.Unlock()
	st.RekeyInProg = r.active
	st.Submitted = len(r.submitted)
	if r.active {
		st.Required = r.required
	}
}

var (
	ErrRekeyInProgress = errors.New("a rekey ceremony is already in progress")
	ErrNoRekey         = errors.New("no rekey ceremony in progress")
	ErrKMSNoCeremony   = errors.New("kms seal does not use a rekey ceremony")
	ErrRekeyNonce      = errors.New("rekey nonce mismatch")
)

// RekeyInit opens the single Shamir rekey ceremony and returns a nonce + the
// number of current shares required. Owner-gated at the API layer.
func (s *Service) RekeyInit(ctx context.Context) (nonce string, required int, err error) {
	if s.kr.Sealed() {
		return "", 0, crypto.ErrSealed
	}
	cfg, err := s.seals.Get(ctx)
	if err != nil {
		return "", 0, err
	}
	if cfg.Type != crypto.SealTypeShamir {
		return "", 0, ErrKMSNoCeremony
	}
	s.rekey.mu.Lock()
	defer s.rekey.mu.Unlock()
	if s.rekey.active {
		return "", 0, ErrRekeyInProgress
	}
	n, err := crypto.GenerateKey()
	if err != nil {
		return "", 0, err
	}
	s.rekey.active = true
	s.rekey.nonce = hex.EncodeToString(n)
	s.rekey.required = cfg.Threshold
	s.rekey.submitted = make(map[string][]byte)
	return s.rekey.nonce, cfg.Threshold, nil
}

// RekeySubmit accepts one current share. When >= threshold distinct shares are
// held it verifies possession (reconstruct + KCV), performs the rotation, and
// returns the new shares exactly once with complete=true. Below threshold it
// returns complete=false and progress.
func (s *Service) RekeySubmit(ctx context.Context, nonce string, share []byte) (complete bool, newShares [][]byte, version, submitted, required int, err error) {
	s.rekey.mu.Lock()
	if !s.rekey.active {
		s.rekey.mu.Unlock()
		return false, nil, 0, 0, 0, ErrNoRekey
	}
	if nonce != s.rekey.nonce {
		s.rekey.mu.Unlock()
		return false, nil, 0, 0, 0, ErrRekeyNonce
	}
	if len(share) < 2 {
		submitted, required = len(s.rekey.submitted), s.rekey.required
		s.rekey.mu.Unlock()
		return false, nil, 0, submitted, required, crypto.ErrInvalidShare
	}
	key := hex.EncodeToString(share)
	if _, dup := s.rekey.submitted[key]; !dup {
		s.rekey.submitted[key] = append([]byte(nil), share...)
	}
	if len(s.rekey.submitted) < s.rekey.required {
		submitted, required = len(s.rekey.submitted), s.rekey.required
		s.rekey.mu.Unlock()
		return false, nil, 0, submitted, required, nil
	}
	// Threshold reached. Gather shares and mark the ceremony consumed BEFORE
	// releasing the lock, so a concurrent submit can't trigger a second
	// rotation.
	parts := make([][]byte, 0, len(s.rekey.submitted))
	for _, p := range s.rekey.submitted {
		parts = append(parts, p)
	}
	required = s.rekey.required
	s.rekey.active = false // consumed; concurrent submits now see ErrNoRekey
	s.rekey.mu.Unlock()

	cfg, cerr := s.seals.Get(ctx)
	if cerr != nil {
		s.closeRekey()
		return false, nil, 0, 0, 0, cerr
	}
	// Proof of possession: reconstruct + verify against the CURRENT KCV.
	candidate, verr := crypto.ReconstructAndVerifyShamir(cfg, parts)
	if verr != nil {
		s.closeRekey()
		return false, nil, 0, 0, 0, verr
	}
	zero(candidate) // proof only; the keyring already holds the master

	shares, ver, rerr := s.performRotation(ctx)
	s.closeRekey()
	if rerr != nil {
		zeroShares(shares)
		return false, nil, 0, 0, 0, rerr
	}
	return true, shares, ver, required, required, nil
}

// RekeyCancel drops the ceremony and zeroizes accumulated shares.
func (s *Service) RekeyCancel() error {
	s.rekey.mu.Lock()
	defer s.rekey.mu.Unlock()
	if !s.rekey.active {
		return ErrNoRekey
	}
	s.clearLocked()
	return nil
}

func (s *Service) closeRekey() {
	s.rekey.mu.Lock()
	s.clearLocked()
	s.rekey.mu.Unlock()
}

func (s *Service) clearLocked() {
	for _, p := range s.rekey.submitted {
		zero(p)
	}
	s.rekey.submitted = nil
	s.rekey.active = false
	s.rekey.nonce = ""
	s.rekey.required = 0
}
