package transit

import (
	"context"

	"github.com/steveokay/janus-secrets/internal/crypto"
	"github.com/steveokay/janus-secrets/internal/store"
)

// KeyMeta is the non-secret view of a transit key.
type KeyMeta struct {
	Name                 string
	Type                 string
	LatestVersion        int
	MinDecryptionVersion int
	DeletionAllowed      bool
	Versions             []int
}

func metaOf(k *store.TransitKey) KeyMeta {
	vs := make([]int, 0, len(k.Versions))
	for _, v := range k.Versions {
		vs = append(vs, v.Version)
	}
	return KeyMeta{Name: k.Name, Type: k.KeyType, LatestVersion: k.LatestVersion,
		MinDecryptionVersion: k.MinDecryptionVersion, DeletionAllowed: k.DeletionAllowed, Versions: vs}
}

// CreateKey creates a named key of the given type with version 1.
func (s *Service) CreateKey(ctx context.Context, name, keyType string) (KeyMeta, error) {
	if s.kr.Sealed() {
		return KeyMeta{}, ErrSealed
	}
	if !validKeyName(name) || (keyType != TypeAES && keyType != TypeEd25519) {
		return KeyMeta{}, ErrValidation
	}
	v, err := s.newVersion(ctx, name, 1, keyType)
	if err != nil {
		return KeyMeta{}, err
	}
	id, err := s.st.NewID(ctx)
	if err != nil {
		return KeyMeta{}, err
	}
	k, err := s.repo.Create(ctx, id, name, keyType, v)
	if err != nil {
		return KeyMeta{}, mapStoreErr(err)
	}
	return metaOf(k), nil
}

// newVersion generates fresh key material for (name, version) and wraps it.
func (s *Service) newVersion(ctx context.Context, name string, version int, keyType string) (*store.TransitKeyVersion, error) {
	id, err := s.st.NewID(ctx)
	if err != nil {
		return nil, err
	}
	v := &store.TransitKeyVersion{ID: id, Version: version}
	switch keyType {
	case TypeAES:
		material, err := crypto.GenerateKey()
		if err != nil {
			return nil, err
		}
		defer zeroize(material)
		wrapped, err := s.kr.WrapTransitKey(material, name, version)
		if err != nil {
			return nil, err
		}
		v.WrappedMaterial = wrapped.Marshal()
	case TypeEd25519:
		pub, priv, err := crypto.GenerateEd25519Key()
		if err != nil {
			return nil, err
		}
		defer zeroize(priv)
		// WrapTransitKey (via crypto.WrapKey) requires exactly KeySize (32-byte)
		// material. The ed25519 private key is 64 bytes (seed||public), so wrap the
		// 32-byte seed; the full private key is reconstructed on use via
		// ed25519.NewKeyFromSeed (Task 7 sign path).
		seed := priv[:32]
		wrapped, err := s.kr.WrapTransitKey(seed, name, version)
		if err != nil {
			return nil, err
		}
		v.WrappedMaterial = wrapped.Marshal()
		v.PublicKey = pub
	}
	return v, nil
}

// materialFor unwraps a specific version's key material (caller must zeroize it).
func (s *Service) materialFor(k *store.TransitKey, version int) ([]byte, error) {
	for _, v := range k.Versions {
		if v.Version == version {
			ct, err := crypto.ParseCiphertext(v.WrappedMaterial)
			if err != nil {
				return nil, err
			}
			return s.kr.UnwrapTransitKey(ct, k.Name, version)
		}
	}
	return nil, ErrBadCiphertext
}

// Encrypt encrypts plaintext under the key's latest version (aes only).
func (s *Service) Encrypt(ctx context.Context, name string, plaintext, aad []byte) (string, error) {
	if s.kr.Sealed() {
		return "", ErrSealed
	}
	k, err := s.repo.GetByName(ctx, name)
	if err != nil {
		return "", mapStoreErr(err)
	}
	if k.KeyType != TypeAES {
		return "", ErrWrongKeyType
	}
	material, err := s.materialFor(k, k.LatestVersion)
	if err != nil {
		return "", err
	}
	defer zeroize(material)
	ct, err := crypto.Encrypt(material, plaintext, aad)
	if err != nil {
		return "", err
	}
	return formatEnvelope(k.LatestVersion, ct.Marshal()), nil
}

// Decrypt decrypts a janus:vN: ciphertext (aes only), honoring min_decryption_version.
func (s *Service) Decrypt(ctx context.Context, name, ciphertext string, aad []byte) ([]byte, error) {
	if s.kr.Sealed() {
		return nil, ErrSealed
	}
	version, body, err := parseEnvelope(ciphertext)
	if err != nil {
		return nil, err
	}
	k, err := s.repo.GetByName(ctx, name)
	if err != nil {
		return nil, mapStoreErr(err)
	}
	if k.KeyType != TypeAES {
		return nil, ErrWrongKeyType
	}
	if version < k.MinDecryptionVersion {
		return nil, ErrVersionTooOld
	}
	material, err := s.materialFor(k, version)
	if err != nil {
		return nil, err
	}
	defer zeroize(material)
	ct, err := crypto.ParseCiphertext(body)
	if err != nil {
		return nil, ErrBadCiphertext
	}
	pt, err := crypto.Decrypt(material, ct, aad)
	if err != nil {
		return nil, ErrBadCiphertext // generic: never reveal which check failed
	}
	return pt, nil
}
