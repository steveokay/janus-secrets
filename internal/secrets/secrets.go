package secrets

import (
	"context"

	"github.com/steveokay/janus-secrets/internal/crypto"
	"github.com/steveokay/janus-secrets/internal/store"
)

// SecretChange is one edit to apply in a batched SetSecrets call. Delete removes
// the key; otherwise Value (plaintext) is encrypted and stored.
//
// SetSecrets zeroizes each non-delete Value after encrypting it, so callers must
// not reuse a Value slice after the call.
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
	_, proj, err := s.resolveProject(ctx, configID)
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
				aad := crypto.DEKAAD(proj.ID, configID+"/"+ch.Key, uint64(valueVersion))
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

	cv, err := s.secrets.SaveConfigVersion(ctx, configID, storeChanges, message, actor)
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
	_, proj, err := s.resolveProject(ctx, configID)
	if err != nil {
		return Secret{}, err
	}
	_, state, err := s.secrets.GetLatest(ctx, configID)
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
	pt, err := s.decryptValue(proj, configID, sv, kek)
	if err != nil {
		return Secret{}, err
	}
	return Secret{Key: key, Value: pt, ValueVersion: sv.ValueVersion}, nil
}

// RevealConfig decrypts and returns every live secret in the latest version.
func (s *Service) RevealConfig(ctx context.Context, configID string) (store.ConfigVersion, map[string]Secret, error) {
	_, proj, err := s.resolveProject(ctx, configID)
	if err != nil {
		return store.ConfigVersion{}, nil, err
	}
	cv, state, err := s.secrets.GetLatest(ctx, configID)
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
		pt, err := s.decryptValue(proj, configID, sv, kek)
		if err != nil {
			return store.ConfigVersion{}, nil, err
		}
		out[key] = Secret{Key: key, Value: pt, ValueVersion: sv.ValueVersion}
	}
	return cv, out, nil
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
	aad := crypto.DEKAAD(proj.ID, configID+"/"+sv.Key, uint64(sv.ValueVersion))
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
