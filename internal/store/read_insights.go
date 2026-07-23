package store

import (
	"context"
	"strings"
	"time"
)

// ReadInsightsDays is the fixed window (in days) for per-key read insights: the
// daily slice always carries exactly this many buckets, oldest→newest, with
// index len-1 == today (see ReadInsightsByKey for the precise bucketing).
const ReadInsightsDays = 30

// KeyReadInsight is one key's read activity over the trailing ReadInsightsDays
// window. Value-free: derived solely from audit_events reveal resource paths
// (key NAMES) and timestamps — never any secret value.
//
//   - LastReadAt: timestamp of the most recent successful per-key reveal, or nil
//     when the key was never revealed per-key in recorded history (the last-read
//     probe is NOT restricted to the 30-day window).
//   - Daily: exactly ReadInsightsDays counts of successful per-key reveals,
//     oldest bucket first. Bucket i covers the UTC calendar day
//     (today - (ReadInsightsDays-1-i)); index ReadInsightsDays-1 is today.
type KeyReadInsight struct {
	LastReadAt *time.Time
	Daily      []int
}

// ReadInsightsRepo derives per-key read insights on demand from audit_events.
// Stateless and value-free; construct one per request from the shared *Store.
//
// Like LastReadRepo it attributes ONLY per-key reveals — resource
// `configs/{cid}/secrets/{key}` (a bare key, no further slash). Server-side bulk
// raw reads record the aggregate resource `configs/{cid}/secrets` (no trailing
// key) and are therefore not per-key attributable; they never count here.
//
// Reuses migration 000029's partial index
// `audit_events_reveal_resource_idx ON (resource, occurred_at) WHERE
// action='secret.reveal'`: the LIKE-prefix range scan on `resource` plus the
// `occurred_at` filter/aggregation are both served from that index.
type ReadInsightsRepo struct{ s *Store }

// NewReadInsightsRepo returns a read-insights repository.
func NewReadInsightsRepo(s *Store) *ReadInsightsRepo { return &ReadInsightsRepo{s: s} }

// ReadInsightsByKey returns, for the given config, a map from secret key to its
// KeyReadInsight over the trailing ReadInsightsDays window. Keys never revealed
// per-key are absent from the map (the caller treats absence as "never read").
// Two grouped queries (last-read + per-day counts); never N+1.
func (r *ReadInsightsRepo) ReadInsightsByKey(ctx context.Context, configID string) (map[string]KeyReadInsight, error) {
	// The reveal resource for a per-key read is exactly
	// `configs/{cid}/secrets/{key}`. The prefix is built from the parameter
	// (never interpolated) so the query stays fully parameterized.
	prefix := "configs/" + configID + "/secrets/"

	// today (UTC calendar day) as computed by the DB, so bucketing agrees with
	// the DB clock used for occurred_at. day0 = the oldest bucket start.
	var today time.Time
	if err := r.s.pool.QueryRow(ctx, `SELECT date_trunc('day', now() AT TIME ZONE 'UTC')`).Scan(&today); err != nil {
		return nil, mapError(err)
	}
	today = today.UTC()
	day0 := today.AddDate(0, 0, -(ReadInsightsDays - 1))

	out := map[string]KeyReadInsight{}
	ensure := func(key string) KeyReadInsight {
		ki, ok := out[key]
		if !ok {
			ki = KeyReadInsight{Daily: make([]int, ReadInsightsDays)}
		}
		return ki
	}

	// --- last-read (whole history, not just the window) ---
	lrRows, err := r.s.pool.Query(ctx,
		`SELECT resource, MAX(occurred_at)
		   FROM audit_events
		  WHERE action = 'secret.reveal'
		    AND result = 'success'
		    AND resource LIKE $1
		  GROUP BY resource`,
		prefix+"%")
	if err != nil {
		return nil, mapError(err)
	}
	for lrRows.Next() {
		var resource string
		var at time.Time
		if err := lrRows.Scan(&resource, &at); err != nil {
			lrRows.Close()
			return nil, mapError(err)
		}
		key := strings.TrimPrefix(resource, prefix)
		if key == "" || strings.Contains(key, "/") {
			continue // not a single-key reveal
		}
		ki := ensure(key)
		v := at
		ki.LastReadAt = &v
		out[key] = ki
	}
	lrRows.Close()
	if err := lrRows.Err(); err != nil {
		return nil, mapError(err)
	}

	// --- per-day counts within the window ---
	// Bucket each reveal into its UTC calendar day and count. Rows older than
	// day0 are excluded by the occurred_at lower bound. The day index is
	// computed in Go from the truncated day to keep the SQL simple and the
	// partial index usable (range scan on resource + occurred_at).
	cntRows, err := r.s.pool.Query(ctx,
		`SELECT resource, date_trunc('day', occurred_at AT TIME ZONE 'UTC') AS day, count(*)
		   FROM audit_events
		  WHERE action = 'secret.reveal'
		    AND result = 'success'
		    AND resource LIKE $1
		    AND occurred_at >= $2
		  GROUP BY resource, day`,
		prefix+"%", day0)
	if err != nil {
		return nil, mapError(err)
	}
	defer cntRows.Close()
	for cntRows.Next() {
		var resource string
		var day time.Time
		var n int
		if err := cntRows.Scan(&resource, &day, &n); err != nil {
			return nil, mapError(err)
		}
		key := strings.TrimPrefix(resource, prefix)
		if key == "" || strings.Contains(key, "/") {
			continue
		}
		idx := int(day.UTC().Sub(day0).Hours() / 24)
		if idx < 0 || idx >= ReadInsightsDays {
			continue // defensive: outside the window
		}
		ki := ensure(key)
		ki.Daily[idx] += n
		out[key] = ki
	}
	return out, mapError(cntRows.Err())
}
