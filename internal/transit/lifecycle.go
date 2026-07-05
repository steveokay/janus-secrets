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

// Rewrap decrypts an old ciphertext and re-encrypts under the latest version,
// preserving the caller's associated_data binding on both sides. Plaintext is
// never returned. aad must match what was bound at encrypt time (nil if none),
// exactly as Decrypt requires — otherwise the AEAD fails and rewrap returns
// ErrBadCiphertext.
func (s *Service) Rewrap(ctx context.Context, name, ciphertext string, aad []byte) (string, error) {
	pt, err := s.Decrypt(ctx, name, ciphertext, aad)
	if err != nil {
		return "", err
	}
	defer zeroize(pt)
	return s.Encrypt(ctx, name, pt, aad)
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

// Get returns a key's metadata (no secret material).
func (s *Service) Get(ctx context.Context, name string) (KeyMeta, error) {
	return s.readMeta(ctx, name)
}

// List returns metadata for all transit keys, ordered by name.
func (s *Service) List(ctx context.Context) ([]KeyMeta, error) {
	ks, err := s.repo.List(ctx)
	if err != nil {
		return nil, mapStoreErr(err)
	}
	out := make([]KeyMeta, 0, len(ks))
	for _, k := range ks {
		out = append(out, metaOf(k))
	}
	return out, nil
}

// Delete permanently removes a key (and its versions). It refuses unless the
// key's deletion_allowed flag has been set via config.
func (s *Service) Delete(ctx context.Context, name string) error {
	k, err := s.repo.GetByName(ctx, name)
	if err != nil {
		return mapStoreErr(err)
	}
	if !k.DeletionAllowed {
		return ErrDeletionNotAllowed
	}
	return mapStoreErr(s.repo.Delete(ctx, k.ID))
}
