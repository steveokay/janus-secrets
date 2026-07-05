package transit

import (
	"context"

	"github.com/steveokay/janus-secrets/internal/crypto"
)

// Sign signs input with the ed25519 key's latest version. The stored material is
// the 32-byte seed (WrapKey enforces KeySize=32), so signing goes through
// SignWithSeed, which reconstructs the private key.
func (s *Service) Sign(ctx context.Context, name string, input []byte) (string, error) {
	if s.kr.Sealed() {
		return "", ErrSealed
	}
	k, err := s.repo.GetByName(ctx, name)
	if err != nil {
		return "", mapStoreErr(err)
	}
	if k.KeyType != TypeEd25519 {
		return "", ErrWrongKeyType
	}
	seed, err := s.materialFor(k, k.LatestVersion)
	if err != nil {
		return "", err
	}
	defer zeroize(seed)
	sig, err := crypto.SignWithSeed(seed, input)
	if err != nil {
		return "", err
	}
	return formatEnvelope(k.LatestVersion, sig), nil
}

// Verify checks a janus:vN: signature against input using that version's stored
// public key. A bad signature returns (false, nil).
func (s *Service) Verify(ctx context.Context, name string, input []byte, signature string) (bool, error) {
	version, sig, err := parseEnvelope(signature)
	if err != nil {
		return false, err
	}
	k, err := s.repo.GetByName(ctx, name)
	if err != nil {
		return false, mapStoreErr(err)
	}
	if k.KeyType != TypeEd25519 {
		return false, ErrWrongKeyType
	}
	for _, v := range k.Versions {
		if v.Version == version {
			return crypto.Verify(v.PublicKey, input, sig), nil
		}
	}
	return false, ErrBadCiphertext
}

// DataKey generates a fresh 256-bit data key, returning it in plaintext plus a
// wrapped (encrypted-under-the-key) ciphertext. Aes keys only. The caller decides
// whether to expose the plaintext.
func (s *Service) DataKey(ctx context.Context, name string) (plaintext []byte, ciphertext string, err error) {
	if s.kr.Sealed() {
		return nil, "", ErrSealed
	}
	k, err := s.repo.GetByName(ctx, name)
	if err != nil {
		return nil, "", mapStoreErr(err)
	}
	if k.KeyType != TypeAES {
		return nil, "", ErrWrongKeyType
	}
	dk, err := crypto.GenerateKey()
	if err != nil {
		return nil, "", err
	}
	ct, err := s.Encrypt(ctx, name, dk, nil)
	if err != nil {
		zeroize(dk)
		return nil, "", err
	}
	return dk, ct, nil
}
