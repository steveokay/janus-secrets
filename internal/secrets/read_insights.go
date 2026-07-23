package secrets

import (
	"context"
	"time"

	"github.com/steveokay/janus-secrets/internal/store"
)

// KeyReadInsight is one editable key's advisory read activity: its most recent
// per-key reveal timestamp (nil = never read per-key) and a fixed-length daily
// reveal-count sparkline over the trailing store.ReadInsightsDays window
// (oldest bucket first; the last bucket is today). Value-free — counts and
// timestamps only, never any secret value.
type KeyReadInsight struct {
	LastReadAt *time.Time
	Daily      []int
}

// ReadInsights returns, for the requested (leaf) config, a per-key read-activity
// map derived entirely from audit_events reveal metadata. Metadata only — no
// decryption, no audit event of its own (like the masked list). Keys never
// revealed per-key are absent (the caller treats absence as "never read").
//
// Attributed to the requested config only; inherited keys have no per-key
// reveals recorded against this config and so read as "never read" here — the
// same attribution rule as advisory unused-secret detection.
func (s *Service) ReadInsights(ctx context.Context, configID string) (map[string]KeyReadInsight, error) {
	// Resolve the config first so a missing/soft-deleted config yields the usual
	// not-found rather than an empty map (parity with ListSecretsMerged).
	if _, err := s.configs.Get(ctx, configID); err != nil {
		return nil, mapStoreErr(err)
	}
	raw, err := s.readIns.ReadInsightsByKey(ctx, configID)
	if err != nil {
		return nil, mapStoreErr(err)
	}
	out := make(map[string]KeyReadInsight, len(raw))
	for k, ki := range raw {
		out[k] = KeyReadInsight{LastReadAt: ki.LastReadAt, Daily: ki.Daily}
	}
	return out, nil
}

// ReadInsightsWindowDays exposes the fixed sparkline window length for callers
// (API/UI) that need to size the daily slice.
func (s *Service) ReadInsightsWindowDays() int { return store.ReadInsightsDays }
