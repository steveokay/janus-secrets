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
	"errors"
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
		zeroShares(newShares)
		return nil, 0, err
	}
	meta, err := s.repo.GetMasterKeyMeta(ctx)
	if err != nil {
		zeroShares(newShares)
		return nil, 0, err
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

// rekeyState is a placeholder filled by the Shamir rekey ceremony task.
type rekeyState struct{}

func (rekeyState) fill(*Status) {}
