package transit

import "context"

// Rotate appends a new version and makes it latest.
func (s *Service) Rotate(ctx context.Context, name string) (KeyMeta, error) {
	if s.kr.Sealed() {
		return KeyMeta{}, ErrSealed
	}
	k, err := s.repo.GetByName(ctx, name)
	if err != nil {
		return KeyMeta{}, mapStoreErr(err)
	}
	v, err := s.newVersion(ctx, name, k.LatestVersion+1, k.KeyType)
	if err != nil {
		return KeyMeta{}, err
	}
	if err := s.repo.AppendVersion(ctx, k.ID, v); err != nil {
		return KeyMeta{}, mapStoreErr(err)
	}
	return s.readMeta(ctx, name)
}

// UpdateConfig sets min_decryption_version (must be within [1, latest]) and/or
// deletion_allowed.
func (s *Service) UpdateConfig(ctx context.Context, name string, minDec *int, delAllowed *bool) error {
	k, err := s.repo.GetByName(ctx, name)
	if err != nil {
		return mapStoreErr(err)
	}
	if minDec != nil && (*minDec < 1 || *minDec > k.LatestVersion) {
		return ErrValidation
	}
	return mapStoreErr(s.repo.UpdateConfig(ctx, k.ID, minDec, delAllowed))
}

// Rewrap decrypts an old ciphertext and re-encrypts under the latest version.
// Plaintext is never returned.
func (s *Service) Rewrap(ctx context.Context, name, ciphertext string) (string, error) {
	pt, err := s.Decrypt(ctx, name, ciphertext, nil)
	if err != nil {
		return "", err
	}
	defer zeroize(pt)
	return s.Encrypt(ctx, name, pt, nil)
}

// Trim permanently deletes versions below minAvailable (<= min_decryption_version).
func (s *Service) Trim(ctx context.Context, name string, minAvailable int) error {
	k, err := s.repo.GetByName(ctx, name)
	if err != nil {
		return mapStoreErr(err)
	}
	if minAvailable < 1 || minAvailable > k.MinDecryptionVersion {
		return ErrValidation
	}
	return s.repo.TrimBelow(ctx, k.ID, minAvailable)
}

// readMeta reloads a key as KeyMeta.
func (s *Service) readMeta(ctx context.Context, name string) (KeyMeta, error) {
	k, err := s.repo.GetByName(ctx, name)
	if err != nil {
		return KeyMeta{}, mapStoreErr(err)
	}
	return metaOf(k), nil
}
