package secrets

import (
	"context"

	"github.com/steveokay/janus-secrets/internal/crypto"
)

// EditRequestBlob is one envelope-encrypted blob: the proposed-changes JSON
// encrypted under a fresh DEK, which is itself wrapped by the config's project
// KEK. All three fields together are needed to decrypt; none reveals plaintext.
type EditRequestBlob struct {
	Ciphertext    []byte
	WrappedDEK    []byte
	Nonce         []byte
	DEKKeyVersion int
}

// EncryptConfigBlob envelope-encrypts plaintext for a config, mirroring the
// secret-value write path: a fresh DEK (wrapped by the config's LATEST project
// KEK) encrypts the blob under a domain-separated AAD bound to project+config.
// Used to store a config-edit-request's proposed []SecretChange without ever
// persisting the proposed secret values in plaintext. plaintext is best-effort
// zeroized after encryption; the caller must treat it as consumed.
func (s *Service) EncryptConfigBlob(ctx context.Context, configID string, plaintext []byte) (EditRequestBlob, error) {
	cfg, proj, err := s.resolveProject(ctx, configID)
	if err != nil {
		return EditRequestBlob{}, err
	}
	kek, err := s.unwrapProjectKEK(proj)
	if err != nil {
		return EditRequestBlob{}, err
	}
	defer zeroize(kek)

	aad := crypto.ConfigEditRequestAAD(proj.ID, cfg.ID)
	dek, wrappedDEK, err := s.keyring.NewDEK(kek, aad)
	if err != nil {
		return EditRequestBlob{}, mapCryptoErr(err)
	}
	defer zeroize(dek)
	ct, err := crypto.Encrypt(dek, plaintext, aad)
	if err != nil {
		return EditRequestBlob{}, mapCryptoErr(err)
	}
	zeroize(plaintext)
	return EditRequestBlob{
		Ciphertext:    ct.Data,
		WrappedDEK:    wrappedDEK.Marshal(),
		Nonce:         ct.Nonce,
		DEKKeyVersion: proj.KEKVersion,
	}, nil
}

// DecryptConfigBlob reverses EncryptConfigBlob, resolving the project KEK for
// the blob's own DEKKeyVersion (rotation-aware, like the secret read path). The
// returned plaintext is the caller's to zeroize after use.
func (s *Service) DecryptConfigBlob(ctx context.Context, configID string, blob EditRequestBlob) ([]byte, error) {
	cfg, proj, err := s.resolveProject(ctx, configID)
	if err != nil {
		return nil, err
	}
	res := s.newKEKResolver(proj)
	defer res.zero()
	kek, err := res.forVersion(ctx, blob.DEKKeyVersion)
	if err != nil {
		return nil, mapCryptoErr(err)
	}
	aad := crypto.ConfigEditRequestAAD(proj.ID, cfg.ID)
	dekCT, err := crypto.ParseCiphertext(blob.WrappedDEK)
	if err != nil {
		return nil, ErrDecrypt
	}
	dek, err := crypto.UnwrapKey(kek, dekCT, aad)
	if err != nil {
		return nil, mapCryptoErr(err)
	}
	defer zeroize(dek)
	pt, err := crypto.Decrypt(dek, crypto.Ciphertext{Nonce: blob.Nonce, Data: blob.Ciphertext}, aad)
	if err != nil {
		return nil, mapCryptoErr(err)
	}
	return pt, nil
}
