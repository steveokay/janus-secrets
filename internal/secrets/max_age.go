package secrets

import (
	"context"
	"fmt"

	"github.com/steveokay/janus-secrets/internal/store"
)

// MaxAgePolicy is one advisory max-age entry: a per-key override (Key is a real
// secret key) or the config default (Key == "").
type MaxAgePolicy struct {
	Key           string
	MaxAgeSeconds int64
}

// SetConfigMaxAge sets the config's DEFAULT advisory max-age (applies to any key
// without a per-key override). seconds must be > 0.
func (s *Service) SetConfigMaxAge(ctx context.Context, configID string, seconds int64, actor string) error {
	return s.setMaxAge(ctx, configID, store.MaxAgeSentinel, seconds, actor)
}

// ClearConfigMaxAge removes the config's default advisory max-age.
func (s *Service) ClearConfigMaxAge(ctx context.Context, configID string) error {
	return s.clearMaxAge(ctx, configID, store.MaxAgeSentinel)
}

// SetKeyMaxAge sets a per-key advisory max-age override. seconds must be > 0.
// key must be a valid secret key (the sentinel "" is not accepted here).
func (s *Service) SetKeyMaxAge(ctx context.Context, configID, key string, seconds int64, actor string) error {
	if err := validateKey(key); err != nil {
		return err
	}
	return s.setMaxAge(ctx, configID, key, seconds, actor)
}

// ClearKeyMaxAge removes a per-key advisory max-age override.
func (s *Service) ClearKeyMaxAge(ctx context.Context, configID, key string) error {
	if err := validateKey(key); err != nil {
		return err
	}
	return s.clearMaxAge(ctx, configID, key)
}

func (s *Service) setMaxAge(ctx context.Context, configID, key string, seconds int64, actor string) error {
	if seconds <= 0 {
		return fmt.Errorf("%w: max_age_seconds must be positive", ErrValidation)
	}
	if _, err := s.configs.Get(ctx, configID); err != nil {
		return mapStoreErr(err)
	}
	return mapStoreErr(s.maxAge.Set(ctx, configID, key, seconds, actor))
}

func (s *Service) clearMaxAge(ctx context.Context, configID, key string) error {
	if _, err := s.configs.Get(ctx, configID); err != nil {
		return mapStoreErr(err)
	}
	return mapStoreErr(s.maxAge.Clear(ctx, configID, key))
}

// ListMaxAge returns a config's advisory max-age policies: per-key overrides and,
// if set, the config default under the sentinel key "".
func (s *Service) ListMaxAge(ctx context.Context, configID string) ([]MaxAgePolicy, error) {
	if _, err := s.configs.Get(ctx, configID); err != nil {
		return nil, mapStoreErr(err)
	}
	entries, err := s.maxAge.List(ctx, configID)
	if err != nil {
		return nil, mapStoreErr(err)
	}
	out := make([]MaxAgePolicy, 0, len(entries))
	for _, e := range entries {
		out = append(out, MaxAgePolicy{Key: e.Key, MaxAgeSeconds: e.MaxAgeSeconds})
	}
	return out, nil
}

// CountStaleKeys returns the number of keys in the config's merged masked view
// that are past their effective advisory max-age. Value-free (metadata only).
func (s *Service) CountStaleKeys(ctx context.Context, configID string) (int, error) {
	metas, err := s.ListSecretsMerged(ctx, configID)
	if err != nil {
		return 0, err
	}
	n := 0
	for _, m := range metas {
		if m.Stale {
			n++
		}
	}
	return n, nil
}

// CountUnusedKeys returns the number of keys in the config's merged masked view
// that have not been read per-key within the advisory unused-secret window
// (never read, or last read older than the threshold). Value-free (metadata only).
func (s *Service) CountUnusedKeys(ctx context.Context, configID string) (int, error) {
	metas, err := s.ListSecretsMerged(ctx, configID)
	if err != nil {
		return 0, err
	}
	n := 0
	for _, m := range metas {
		if m.Unused {
			n++
		}
	}
	return n, nil
}
