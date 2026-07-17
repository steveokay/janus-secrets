package secrets

import (
	"context"
	"errors"

	"github.com/steveokay/janus-secrets/internal/crypto"
	"github.com/steveokay/janus-secrets/internal/store"
)

// SecretChange is one edit to apply in a batched SetSecrets call. Delete removes
// the key; otherwise Value (plaintext) is encrypted and stored.
//
// SetSecrets best-effort zeroizes each Value it actually encrypts, so callers
// must treat a Value slice as consumed after the call and not reuse it. Note the
// wipe is not guaranteed: a Value superseded within the same batch (e.g. a
// set-then-delete or set-then-set of the same key) is never encrypted and so is
// left intact, as is any Value whose encryption the save aborts before reaching.
// The caller still owns the slice's memory.
type SecretChange struct {
	Key    string
	Value  []byte
	Delete bool
	// Type is a display/handling hint (see allowedTypes); empty normalizes to
	// "string". Never a storage or crypto concern. Ignored for deletes.
	Type string
}

// Secret is a decrypted secret value returned by a reveal.
type Secret struct {
	Key          string
	Value        []byte
	ValueVersion int
	Type         string
}

// SetSecrets encrypts and saves a batch of edits as one new config version.
func (s *Service) SetSecrets(ctx context.Context, configID string, changes []SecretChange, message, actor string) (store.ConfigVersion, error) {
	for _, ch := range changes {
		if err := validateKey(ch.Key); err != nil {
			return store.ConfigVersion{}, err
		}
		if !ch.Delete {
			if err := validateType(ch.Type); err != nil {
				return store.ConfigVersion{}, err
			}
		}
	}
	cfg, proj, err := s.resolveProject(ctx, configID)
	if err != nil {
		return store.ConfigVersion{}, err
	}
	kek, err := s.unwrapProjectKEK(proj)
	if err != nil {
		return store.ConfigVersion{}, err
	}
	defer zeroize(kek)

	storeChanges := make([]store.Change, 0, len(changes))
	for _, ch := range changes {
		ch := ch // capture per iteration
		if ch.Delete {
			storeChanges = append(storeChanges, store.Change{Key: ch.Key})
			continue
		}
		storeChanges = append(storeChanges, store.Change{
			Key:  ch.Key,
			Type: normalizeType(ch.Type),
			Encrypt: func(valueVersion int) (*store.EncryptedValue, error) {
				// AAD is built from the resolved cfg.ID (canonical id::text), not
				// the caller-supplied configID string, so representation quirks
				// in the input (case, hyphenation) cannot skew the binding.
				aad, err := dekAAD(proj.ID, cfg.ID+"/"+ch.Key, valueVersion)
				if err != nil {
					return nil, err
				}
				dek, wrappedDEK, err := s.keyring.NewDEK(kek, aad)
				if err != nil {
					return nil, err
				}
				defer zeroize(dek)
				valCT, err := crypto.Encrypt(dek, ch.Value, aad)
				if err != nil {
					return nil, err
				}
				zeroize(ch.Value) // best-effort wipe of caller plaintext
				return &store.EncryptedValue{
					WrappedDEK:    wrappedDEK.Marshal(),
					Ciphertext:    valCT.Data,
					Nonce:         valCT.Nonce,
					DEKKeyVersion: proj.KEKVersion,
				}, nil
			},
		})
	}

	// The save can surface either a store sentinel (e.g. ErrConflict for a
	// soft-deleted config) or a crypto error propagated out of an Encrypt
	// closure (e.g. ErrSealed if the keyring seals mid-batch), so both mappers
	// compose here. Read paths don't need this: their crypto errors are already
	// mapped inside decryptValue/unwrapProjectKEK.
	cv, err := s.secrets.SaveConfigVersion(ctx, cfg.ID, storeChanges, message, actor)
	if err != nil {
		return store.ConfigVersion{}, mapCryptoErr(mapStoreErr(err))
	}
	return cv, nil
}

// GetSecret decrypts and returns the latest value for one key.
func (s *Service) GetSecret(ctx context.Context, configID, key string) (Secret, error) {
	if err := validateKey(key); err != nil {
		return Secret{}, err
	}
	cfg, proj, err := s.resolveProject(ctx, configID)
	if err != nil {
		return Secret{}, err
	}
	_, state, err := s.secrets.GetLatest(ctx, cfg.ID)
	if err != nil {
		return Secret{}, mapStoreErr(err)
	}
	sv, ok := state[key]
	if !ok {
		return Secret{}, ErrNotFound
	}
	res := s.newKEKResolver(proj)
	defer res.zero()
	pt, err := s.decryptValue(ctx, proj, cfg.ID, sv, res)
	if err != nil {
		return Secret{}, err
	}
	return Secret{Key: key, Value: pt, ValueVersion: sv.ValueVersion, Type: sv.Type}, nil
}

// RevealConfig decrypts and returns every live secret in the latest version.
func (s *Service) RevealConfig(ctx context.Context, configID string) (store.ConfigVersion, map[string]Secret, error) {
	cfg, proj, err := s.resolveProject(ctx, configID)
	if err != nil {
		return store.ConfigVersion{}, nil, err
	}
	cv, state, err := s.secrets.GetLatest(ctx, cfg.ID)
	if err != nil {
		return store.ConfigVersion{}, nil, mapStoreErr(err)
	}
	res := s.newKEKResolver(proj)
	defer res.zero()
	out := make(map[string]Secret, len(state))
	for key, sv := range state {
		pt, err := s.decryptValue(ctx, proj, cfg.ID, sv, res)
		if err != nil {
			// Failing partway through: wipe the plaintexts already decrypted
			// into out before abandoning it, keeping the package's zeroization
			// discipline on error paths.
			for _, sec := range out {
				zeroize(sec.Value)
			}
			return store.ConfigVersion{}, nil, err
		}
		out[key] = Secret{Key: key, Value: pt, ValueVersion: sv.ValueVersion, Type: sv.Type}
	}
	return cv, out, nil
}

// GetSecretVersion decrypts and returns a specific historical value of a key.
func (s *Service) GetSecretVersion(ctx context.Context, configID, key string, valueVersion int) (Secret, error) {
	if err := validateKey(key); err != nil {
		return Secret{}, err
	}
	cfg, proj, err := s.resolveProject(ctx, configID)
	if err != nil {
		return Secret{}, err
	}
	hist, err := s.secrets.GetKeyHistory(ctx, cfg.ID, key)
	if err != nil {
		return Secret{}, mapStoreErr(err)
	}
	var found *store.SecretValue
	for i := range hist {
		if hist[i].ValueVersion == valueVersion {
			found = &hist[i]
			break
		}
	}
	if found == nil {
		return Secret{}, ErrNotFound
	}
	res := s.newKEKResolver(proj)
	defer res.zero()
	pt, err := s.decryptValue(ctx, proj, cfg.ID, *found, res)
	if err != nil {
		return Secret{}, err
	}
	return Secret{Key: key, Value: pt, ValueVersion: valueVersion, Type: found.Type}, nil
}

// resolveProject walks config → environment → project.
func (s *Service) resolveProject(ctx context.Context, configID string) (*store.Config, *store.Project, error) {
	cfg, err := s.configs.Get(ctx, configID)
	if err != nil {
		return nil, nil, mapStoreErr(err)
	}
	env, err := s.envs.Get(ctx, cfg.EnvironmentID)
	if err != nil {
		return nil, nil, mapStoreErr(err)
	}
	proj, err := s.projects.Get(ctx, env.ProjectID)
	if err != nil {
		return nil, nil, mapStoreErr(err)
	}
	return cfg, proj, nil
}

// unwrapProjectKEK parses and unwraps proj's LATEST stored KEK. The caller must
// zeroize the returned key. Used by the write path, which always encrypts under
// the current project KEK version. Read paths use kekResolver instead, so they
// can unwrap DEKs still wrapped under a superseded KEK after a rotation.
func (s *Service) unwrapProjectKEK(proj *store.Project) ([]byte, error) {
	ct, err := crypto.ParseCiphertext(proj.WrappedKEK)
	if err != nil {
		return nil, ErrDecrypt
	}
	kek, err := s.keyring.UnwrapProjectKEK(ct, proj.ID)
	if err != nil {
		return nil, mapCryptoErr(err)
	}
	return kek, nil
}

// kekResolver unwraps and caches a project's KEKs by version for the lifetime of
// one read. After a project-KEK rotation, different secret_values rows may be
// wrapped under different KEK versions (dek_key_version), so a single read may
// need more than one KEK: the latest comes from proj.WrappedKEK, older ones from
// project_kek_versions. Cached KEK bytes are zeroized on zero().
type kekResolver struct {
	s     *Service
	proj  *store.Project
	cache map[int][]byte
}

func (s *Service) newKEKResolver(proj *store.Project) *kekResolver {
	return &kekResolver{s: s, proj: proj, cache: map[int][]byte{}}
}

// forVersion returns the (cached) unwrapped project KEK for the given version.
// The returned slice is owned by the resolver; the caller must NOT zeroize it —
// zero() wipes every cached KEK when the read completes.
func (kr *kekResolver) forVersion(ctx context.Context, version int) ([]byte, error) {
	if k, ok := kr.cache[version]; ok {
		return k, nil
	}
	var wrapped []byte
	if version == kr.proj.KEKVersion {
		wrapped = kr.proj.WrappedKEK
	} else {
		b, err := kr.s.kekVers.GetWrapped(ctx, kr.proj.ID, version)
		if err != nil {
			// A superseded version that no longer exists means a concurrent KEK
			// rewrap retired it (all its DEKs were re-wrapped) after we snapshotted
			// this row — signal a retire race so the caller can re-read and retry
			// rather than surfacing a spurious failure.
			if errors.Is(err, store.ErrNotFound) {
				return nil, errKEKVersionRetired
			}
			return nil, mapStoreErr(err)
		}
		wrapped = b
	}
	ct, err := crypto.ParseCiphertext(wrapped)
	if err != nil {
		return nil, ErrDecrypt
	}
	kek, err := kr.s.keyring.UnwrapProjectKEK(ct, kr.proj.ID)
	if err != nil {
		return nil, mapCryptoErr(err)
	}
	kr.cache[version] = kek
	return kek, nil
}

// zero wipes every cached KEK. Call on defer once the read completes.
func (kr *kekResolver) zero() {
	for _, k := range kr.cache {
		zeroize(k)
	}
}

// decryptValue decrypts one stored SecretValue, resolving the project KEK for
// the value's own DEKKeyVersion (rotation-aware). If a concurrent KEK rewrap
// retired the version this row's snapshot referenced (a narrow TOCTOU between
// the state snapshot and KEK resolution), it re-reads the row and project once
// and retries against fresh, consistent state — the row is then at a live
// version. Bounded to a single retry, so genuinely corrupt data still fails
// with ErrDecrypt rather than looping.
func (s *Service) decryptValue(ctx context.Context, proj *store.Project, configID string, sv store.SecretValue, res *kekResolver) ([]byte, error) {
	pt, err := s.decryptValueOnce(ctx, proj, configID, sv, res)
	if !errors.Is(err, errKEKVersionRetired) {
		return pt, err
	}
	// Retire race: re-read the row (fresh wrapped_dek/dek_key_version) and the
	// project (fresh KEK) and retry once under a fresh resolver.
	fresh, ferr := s.secrets.GetValueByID(ctx, sv.ID)
	if ferr != nil {
		return nil, ErrDecrypt
	}
	_, freshProj, ferr := s.resolveProject(ctx, configID)
	if ferr != nil {
		return nil, ErrDecrypt
	}
	res2 := s.newKEKResolver(freshProj)
	defer res2.zero()
	pt, err = s.decryptValueOnce(ctx, freshProj, configID, fresh, res2)
	if errors.Is(err, errKEKVersionRetired) {
		// Another retire landed in the microscopic re-read window; give up rather
		// than loop. Never surface the internal sentinel to callers.
		return nil, ErrDecrypt
	}
	return pt, err
}

// decryptValueOnce is a single decrypt attempt against the given snapshot. Only
// the KEK source differs from the write path; AAD derivation, DEK unwrap, and
// Decrypt are unchanged.
func (s *Service) decryptValueOnce(ctx context.Context, proj *store.Project, configID string, sv store.SecretValue, res *kekResolver) ([]byte, error) {
	aad, err := dekAAD(proj.ID, configID+"/"+sv.Key, sv.ValueVersion)
	if err != nil {
		return nil, err
	}
	kek, err := res.forVersion(ctx, sv.DEKKeyVersion)
	if err != nil {
		return nil, err
	}
	dekCT, err := crypto.ParseCiphertext(sv.WrappedDEK)
	if err != nil {
		return nil, ErrDecrypt
	}
	dek, err := crypto.UnwrapKey(kek, dekCT, aad)
	if err != nil {
		return nil, mapCryptoErr(err)
	}
	defer zeroize(dek)
	pt, err := crypto.Decrypt(dek, crypto.Ciphertext{Nonce: sv.Nonce, Data: sv.Ciphertext}, aad)
	if err != nil {
		return nil, mapCryptoErr(err)
	}
	return pt, nil
}

// dekAAD builds the AES-GCM additional-authenticated-data that binds a DEK to
// its exact slot: project, config/key path, and value version. It is the single
// construction point shared by the set and read paths, so the two cannot drift.
// value_version is a positive, monotonic counter; a negative value would signal
// corrupt data, so we fail closed rather than wrap it into a large uint64.
func dekAAD(projectID, secretPath string, valueVersion int) ([]byte, error) {
	if valueVersion < 0 {
		return nil, ErrDecrypt
	}
	return crypto.DEKAAD(projectID, secretPath, uint64(valueVersion)), nil
}
