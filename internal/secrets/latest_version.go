package secrets

import (
	"context"
	"errors"

	"github.com/steveokay/janus-secrets/internal/store"
)

// LatestVersion returns the config's current config-version number without
// decrypting any values (used by the resolved reveal, which gets values from the
// resolver and only needs the version for the response envelope). A version-less
// config (one that inherits but has never been written to) has version 0 — not
// an error — so its resolved reveal reports its own version honestly while its
// values come from the inheritance base.
func (s *Service) LatestVersion(ctx context.Context, configID string) (int, error) {
	cv, _, err := s.secrets.GetLatest(ctx, configID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return 0, nil
		}
		return 0, mapStoreErr(err)
	}
	return cv.Version, nil
}
