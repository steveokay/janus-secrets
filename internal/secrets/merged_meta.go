package secrets

import (
	"context"
	"errors"
	"time"

	"github.com/steveokay/janus-secrets/internal/store"
)

// MergedMeta is one key in the inheritance-merged masked view. Origin is
// "own" (defined only here), "inherited" (only from a base), or "overridden"
// (defined here and also in a base).
type MergedMeta struct {
	Key          string
	ValueVersion int
	CreatedAt    time.Time
	Origin       string
}

type storeMetaEntry struct {
	vv int
	at time.Time
}

// ListSecretsMerged returns the masked, inheritance-merged key set for configID.
// Metadata only — no decryption, no audit. Child metadata wins; a key present in
// both this config and an ancestor is "overridden".
func (s *Service) ListSecretsMerged(ctx context.Context, configID string) ([]MergedMeta, error) {
	seen := map[string]bool{}
	var chainCV []map[string]storeMetaEntry
	own := map[string]bool{}
	id := configID
	first := true
	for id != "" {
		if seen[id] {
			return nil, ErrConflict // inheritance cycle → 409
		}
		seen[id] = true
		cfg, err := s.configs.Get(ctx, id)
		if err != nil {
			if first {
				return nil, mapStoreErr(err)
			}
			return nil, ErrConflict // broken base → 409
		}
		// A config with no version of its own contributes an empty own-key set
		// but still inherits from its base (a branch that only overrides/adds).
		_, state, err := s.secrets.GetLatest(ctx, id)
		if err != nil && !errors.Is(err, store.ErrNotFound) {
			return nil, mapStoreErr(err)
		}
		lvl := map[string]storeMetaEntry{}
		for k, sv := range state {
			lvl[k] = storeMetaEntry{vv: sv.ValueVersion, at: sv.CreatedAt}
			if first {
				own[k] = true
			}
		}
		chainCV = append(chainCV, lvl)
		first = false
		if cfg.InheritsFrom != nil {
			id = *cfg.InheritsFrom
		} else {
			id = ""
		}
	}
	// Merge ancestor→child (index len-1 .. 0); child wins for value_version/created_at.
	merged := map[string]storeMetaEntry{}
	presentAtMultiple := map[string]int{}
	for i := len(chainCV) - 1; i >= 0; i-- {
		for k, e := range chainCV[i] {
			merged[k] = e
			presentAtMultiple[k]++
		}
	}
	out := make([]MergedMeta, 0, len(merged))
	for k, e := range merged {
		origin := "inherited"
		if own[k] {
			if presentAtMultiple[k] > 1 {
				origin = "overridden"
			} else {
				origin = "own"
			}
		}
		out = append(out, MergedMeta{Key: k, ValueVersion: e.vv, CreatedAt: e.at, Origin: origin})
	}
	return out, nil
}
