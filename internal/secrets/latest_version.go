package secrets

import "context"

// LatestVersion returns the config's current config-version number without
// decrypting any values (used by the resolved reveal, which gets values from the
// resolver and only needs the version for the response envelope).
func (s *Service) LatestVersion(ctx context.Context, configID string) (int, error) {
	cv, _, err := s.secrets.GetLatest(ctx, configID)
	if err != nil {
		return 0, mapStoreErr(err)
	}
	return cv.Version, nil
}
