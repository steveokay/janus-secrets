package store

import "context"

// AuditRetentionRepo answers the two read-only questions that translate an
// operator-configured minimum-retention floor ("keep the last N days", "keep the
// newest N events") into a ceiling on the audit-prune point.
//
// It is deliberately read-only and value-free: it reads nothing but `seq` and
// `occurred_at` from audit_events. The delete itself stays on
// AuditCheckpointRepo.PruneThrough, and the policy decision stays in the API
// handler — this repo only converts a policy into a seq.
type AuditRetentionRepo struct{ s *Store }

// NewAuditRetentionRepo constructs the repo over a store.
func NewAuditRetentionRepo(s *Store) *AuditRetentionRepo { return &AuditRetentionRepo{s: s} }

// SeqOlderThanDays returns the highest audit seq whose occurred_at is strictly
// older than now() - days, i.e. the largest seq that may be pruned while still
// retaining every event younger than the floor.
//
// Returns 0 when every event is younger than the floor (nothing may be pruned).
// days must be positive; the caller checks that before calling.
func (r *AuditRetentionRepo) SeqOlderThanDays(ctx context.Context, days int) (int64, error) {
	var seq int64
	err := r.s.pool.QueryRow(ctx,
		`SELECT COALESCE(MAX(seq), 0) FROM audit_events
		 WHERE occurred_at < now() - make_interval(days => $1::int)`, days).Scan(&seq)
	if err != nil {
		return 0, mapError(err)
	}
	return seq, nil
}

// SeqRetainingNewest returns the highest audit seq that may be pruned while
// still retaining the newest n events.
//
// The newest n events are the n highest seqs; the ceiling is one below the
// lowest of them. Returns 0 when the table holds n or fewer events (nothing may
// be pruned). Computed over seq rather than a count offset from the head so it
// stays correct across the seq gaps a previous prune leaves behind. n must be
// positive; the caller checks that before calling.
func (r *AuditRetentionRepo) SeqRetainingNewest(ctx context.Context, n int64) (int64, error) {
	var oldestRetained int64
	err := r.s.pool.QueryRow(ctx,
		`SELECT COALESCE(MIN(seq), 0) FROM (
		     SELECT seq FROM audit_events ORDER BY seq DESC LIMIT $1
		 ) newest`, n).Scan(&oldestRetained)
	if err != nil {
		return 0, mapError(err)
	}
	// 0 = empty table; 1 = the oldest surviving event is already retained, so
	// there is nothing below it to prune.
	if oldestRetained <= 1 {
		return 0, nil
	}
	return oldestRetained - 1, nil
}
