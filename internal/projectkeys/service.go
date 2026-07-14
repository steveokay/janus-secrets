// Package projectkeys rotates a project's KEK and lazily re-wraps its DEKs;
// never decrypts a secret value; zeroes key material after use.
//
// A rotation is two independent operations:
//
//   - Rotate installs a fresh project KEK as a new version, preserving the old
//     wrapped KEK in project_kek_versions. Existing DEKs stay wrapped under the
//     old KEK; reads remain correct because the read path resolves each DEK's
//     own dek_key_version.
//   - Rewrap lazily walks every DEK still wrapped under a superseded KEK,
//     re-wrapping it under the latest KEK, then retires KEK versions no DEK
//     references anymore. It re-wraps 32-byte DEKs only and NEVER decrypts a
//     secret value: store.RewrapRow carries no ciphertext/nonce, and this
//     package never fetches them.
package projectkeys

import (
	"context"
	"fmt"

	"github.com/steveokay/janus-secrets/internal/crypto"
	"github.com/steveokay/janus-secrets/internal/store"
)

// Service orchestrates a project-KEK rotation over the keyring and store repos.
// The crypto (wrap/unwrap of KEKs and DEKs) lives here; the DB atomicity lives
// in the store repositories it drives.
type Service struct {
	kr      *crypto.Keyring
	proj    *store.ProjectRepo
	kekVers *store.ProjectKEKVersionRepo
	secrets *store.SecretRepo
}

// New builds a Service from an (already-unsealed at call time) keyring and the
// three store repositories it drives.
func New(kr *crypto.Keyring, proj *store.ProjectRepo, kekVers *store.ProjectKEKVersionRepo, secrets *store.SecretRepo) *Service {
	return &Service{kr: kr, proj: proj, kekVers: kekVers, secrets: secrets}
}

// rewrapBatchSize is the keyset page size for the DEK re-wrap sweep. Each batch
// commits its own transaction, so a crash mid-sweep loses at most one batch of
// in-flight work; a later Rewrap resumes from the persisted cursor.
const rewrapBatchSize = 200

// Rotate installs a fresh project KEK as the next version and returns that new
// version. The old wrapped KEK is preserved in project_kek_versions by the
// store (atomically), so DEKs wrapped under it stay decryptable until Rewrap
// re-wraps them. Propagates crypto.ErrSealed (keyring sealed) and
// store.ErrNotFound (missing/soft-deleted project); on either the store rolls
// back with no orphan version row.
func (s *Service) Rotate(ctx context.Context, projectID string) (int, error) {
	return s.proj.RotateKEK(ctx, projectID, func(oldVersion int) ([]byte, error) {
		kek, err := crypto.GenerateKey()
		if err != nil {
			return nil, err
		}
		defer zero(kek)
		ct, err := s.kr.WrapProjectKEK(kek, projectID)
		if err != nil {
			return nil, err
		}
		return ct.Marshal(), nil
	})
}

// RewrapResult reports the outcome of a Rewrap sweep.
type RewrapResult struct {
	Rewrapped int   // number of DEKs re-wrapped under the latest KEK
	Retired   []int // superseded KEK versions deleted (no DEK references them)
}

// Rewrap re-wraps every DEK still wrapped under a superseded KEK version onto
// the project's latest KEK, then retires KEK versions no DEK references anymore.
//
// It unwraps each needed KEK version exactly once (cached for the sweep), and
// for each DEK: derives the read-path AAD, unwraps the 32-byte DEK under its
// old KEK, and re-wraps it under the latest KEK with a fresh random nonce. It
// never touches a secret value's ciphertext. All unwrapped KEKs and DEKs are
// zeroed after use. Errors carry only a row id / version number, never key
// material. Propagates crypto.ErrSealed and store.ErrNotFound.
func (s *Service) Rewrap(ctx context.Context, projectID string) (RewrapResult, error) {
	p, err := s.proj.Get(ctx, projectID)
	if err != nil {
		return RewrapResult{}, err
	}
	latest := p.KEKVersion

	// kekCache holds unwrapped KEKs by version for the lifetime of this sweep,
	// all zeroed on exit. The latest version is loaded from the project row; older
	// versions are loaded from project_kek_versions on first use.
	kekCache := map[int][]byte{}
	defer func() {
		for _, k := range kekCache {
			zero(k)
		}
	}()

	loadKEK := func(version int) ([]byte, error) {
		if k, ok := kekCache[version]; ok {
			return k, nil
		}
		var wrapped []byte
		if version == latest {
			wrapped = p.WrappedKEK
		} else {
			b, gerr := s.kekVers.GetWrapped(ctx, projectID, version)
			if gerr != nil {
				return nil, gerr
			}
			wrapped = b
		}
		ct, perr := crypto.ParseCiphertext(wrapped)
		if perr != nil {
			return nil, perr
		}
		kek, uerr := s.kr.UnwrapProjectKEK(ct, projectID)
		if uerr != nil {
			return nil, uerr
		}
		kekCache[version] = kek
		return kek, nil
	}

	// Unwrap the latest KEK once up front: this both primes the cache and makes
	// a sealed keyring fail fast (before any batch), matching the write path.
	latestKEK, err := loadKEK(latest)
	if err != nil {
		return RewrapResult{}, err
	}

	rewrapFn := func(row store.RewrapRow) ([]byte, error) {
		// AAD is byte-identical to the read path (secrets.decryptValue): it is
		// derived from projectID, configID+"/"+key, and value_version. It must
		// match for both unwrap-old and wrap-new or the re-wrapped DEK will not
		// decrypt.
		aad, aerr := dekAAD(projectID, row.ConfigID+"/"+row.Key, row.ValueVersion)
		if aerr != nil {
			return nil, fmt.Errorf("rewrap %s: %w", row.ID, aerr)
		}
		oldKEK, kerr := loadKEK(row.DEKKeyVersion)
		if kerr != nil {
			return nil, fmt.Errorf("rewrap %s: %w", row.ID, kerr)
		}
		dekCT, perr := crypto.ParseCiphertext(row.WrappedDEK)
		if perr != nil {
			return nil, fmt.Errorf("rewrap %s: %w", row.ID, perr)
		}
		dek, uerr := crypto.UnwrapKey(oldKEK, dekCT, aad)
		if uerr != nil {
			return nil, fmt.Errorf("rewrap %s: %w", row.ID, uerr)
		}
		defer zero(dek)
		newCT, werr := crypto.WrapKey(latestKEK, dek, aad)
		if werr != nil {
			return nil, fmt.Errorf("rewrap %s: %w", row.ID, werr)
		}
		return newCT.Marshal(), nil
	}

	rewrapped := 0
	cursor := ""
	for {
		processed, next, berr := s.secrets.RewrapBatch(ctx, projectID, latest, cursor, rewrapBatchSize, rewrapFn)
		rewrapped += processed
		if berr != nil {
			return RewrapResult{Rewrapped: rewrapped}, berr
		}
		if next == "" {
			break
		}
		cursor = next
	}

	retired, err := s.kekVers.DeleteEmpty(ctx, projectID)
	if err != nil {
		return RewrapResult{Rewrapped: rewrapped}, err
	}
	return RewrapResult{Rewrapped: rewrapped, Retired: retired}, nil
}

// Status is the current KEK version plus any superseded versions still awaiting
// re-wrap (with the count of DEKs pinned to each), oldest first.
type Status struct {
	CurrentVersion int
	Pending        []store.PendingVersion
}

// StatusFor reports the project's current KEK version and its pending
// (not-yet-retired) superseded versions.
func (s *Service) StatusFor(ctx context.Context, projectID string) (Status, error) {
	p, err := s.proj.Get(ctx, projectID)
	if err != nil {
		return Status{}, err
	}
	pending, err := s.kekVers.ListPending(ctx, projectID)
	if err != nil {
		return Status{}, err
	}
	return Status{CurrentVersion: p.KEKVersion, Pending: pending}, nil
}

// dekAAD builds the DEK AES-GCM additional-authenticated-data byte-identically
// to internal/secrets.dekAAD (the read path): value_version must be
// non-negative, and the AAD binds project, config/key path, and version.
func dekAAD(projectID, secretPath string, valueVersion int) ([]byte, error) {
	if valueVersion < 0 {
		return nil, fmt.Errorf("negative value version %d", valueVersion)
	}
	return crypto.DEKAAD(projectID, secretPath, uint64(valueVersion)), nil
}

// zero overwrites b with zeros. Best-effort defense-in-depth.
func zero(b []byte) {
	for i := range b {
		b[i] = 0
	}
}
