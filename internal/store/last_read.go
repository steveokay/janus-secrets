package store

import (
	"context"
	"strings"
	"time"
)

// LastReadRepo derives, per secret key, the timestamp of the most recent
// per-key reveal from audit_events. Stateless and value-free: it reads only
// resource paths (which carry key NAMES, never values) and timestamps.
//
// Per-key reveals — including the UI's "Reveal all", which fans out to the
// per-key reveal endpoint — record the resource `configs/{cid}/secrets/{key}`.
// Server-side BULK raw reads record the aggregate resource
// `configs/{cid}/secrets` (no trailing key) and are therefore NOT per-key
// attributable; they do not count toward a key's last-read timestamp.
type LastReadRepo struct{ s *Store }

// NewLastReadRepo returns a last-read repository.
func NewLastReadRepo(s *Store) *LastReadRepo { return &LastReadRepo{s: s} }

// LastReadByKey returns, for the given config, a map from secret key to the most
// recent successful per-key reveal timestamp. Keys never revealed per-key are
// absent from the map (the caller treats absence as "never read"). One grouped
// query; never N+1.
func (r *LastReadRepo) LastReadByKey(ctx context.Context, configID string) (map[string]time.Time, error) {
	// The reveal resource for a per-key read is exactly `configs/{cid}/secrets/{key}`.
	// Match that prefix, group by full resource, take MAX(occurred_at); the trailing
	// key is parsed out in Go. The prefix is built from the parameter (never
	// interpolated) so the query stays fully parameterized.
	prefix := "configs/" + configID + "/secrets/"
	rows, err := r.s.pool.Query(ctx,
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
	defer rows.Close()
	out := map[string]time.Time{}
	for rows.Next() {
		var resource string
		var at time.Time
		if err := rows.Scan(&resource, &at); err != nil {
			return nil, mapError(err)
		}
		// resource == prefix + key. A bare key (no slashes) is the per-key case;
		// anything with a further slash is not a single-key reveal and is skipped.
		key := strings.TrimPrefix(resource, prefix)
		if key == "" || strings.Contains(key, "/") {
			continue
		}
		// Keep the latest if the same key somehow appears twice (defensive).
		if prev, ok := out[key]; !ok || at.After(prev) {
			out[key] = at
		}
	}
	return out, mapError(rows.Err())
}
