package secrets

import (
	"context"

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
}

// Secret is a decrypted secret value returned by a reveal.
type Secret struct {
	Key          string
	Value        []byte
	ValueVersion int
}

// SetSecrets encrypts and saves a batch of edits as one new config version.
func (s *Service) SetSecrets(ctx context.Context, configID string, changes []SecretChange, message, actor string) (store.ConfigVersion, error) {
	for _, ch := range changes {
		if err := validateKey(ch.Key); err != nil {
			return store.ConfigVersion{}, err
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
			Key: ch.Key,
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
	kek, err := s.unwrapProjectKEK(proj)
	if err != nil {
		return Secret{}, err
	}
	defer zeroize(kek)
	pt, err := s.decryptValue(proj, cfg.ID, sv, kek)
	if err != nil {
		return Secret{}, err
	}
	return Secret{Key: key, Value: pt, ValueVersion: sv.ValueVersion}, nil
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
	kek, err := s.unwrapProjectKEK(proj)
	if err != nil {
		return store.ConfigVersion{}, nil, err
	}
	defer zeroize(kek)
	out := make(map[string]Secret, len(state))
	for key, sv := range state {
		pt, err := s.decryptValue(proj, cfg.ID, sv, kek)
		if err != nil {
			// Failing partway through: wipe the plaintexts already decrypted
			// into out before abandoning it, keeping the package's zeroization
			// discipline on error paths.
			for _, sec := range out {
				zeroize(sec.Value)
			}
			return store.ConfigVersion{}, nil, err
		}
		out[key] = Secret{Key: key, Value: pt, ValueVersion: sv.ValueVersion}
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
	kek, err := s.unwrapProjectKEK(proj)
	if err != nil {
		return Secret{}, err
	}
	defer zeroize(kek)
	pt, err := s.decryptValue(proj, cfg.ID, *found, kek)
	if err != nil {
		return Secret{}, err
	}
	return Secret{Key: key, Value: pt, ValueVersion: valueVersion}, nil
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

// unwrapProjectKEK parses and unwraps proj's stored KEK. The caller must
// zeroize the returned key.
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

// decryptValue decrypts one stored SecretValue using an already-unwrapped kek.
func (s *Service) decryptValue(proj *store.Project, configID string, sv store.SecretValue, kek []byte) ([]byte, error) {
	aad, err := dekAAD(proj.ID, configID+"/"+sv.Key, sv.ValueVersion)
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
