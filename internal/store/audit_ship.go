package store

import "context"

// AuditShipRepo persists the audit-shipper's durable high-water mark: the
// highest audit seq already shipped to the external SIEM destination. The mark
// advances only after a successful send, so a crash or a failed send resumes
// from the same seq (at-least-once, no gaps). The destination config itself is
// env-supplied and never stored here.
type AuditShipRepo struct{ s *Store }

// NewAuditShipRepo constructs the repo over a store.
func NewAuditShipRepo(s *Store) *AuditShipRepo { return &AuditShipRepo{s: s} }

// GetHighWater returns the highest audit seq already shipped. Seeded at
// migration time to the audit head, so enabling the shipper never replays
// history.
func (r *AuditShipRepo) GetHighWater(ctx context.Context) (int64, error) {
	var seq int64
	err := r.s.pool.QueryRow(ctx, `SELECT last_seq FROM audit_ship_state WHERE id = true`).Scan(&seq)
	if err != nil {
		return 0, mapError(err)
	}
	return seq, nil
}

// AdvanceHighWater moves the mark forward to newSeq. The monotonic guard
// (last_seq < $1) makes a concurrent or out-of-order advance a no-op rather than
// ever moving the mark backward (which would replay already-shipped events).
func (r *AuditShipRepo) AdvanceHighWater(ctx context.Context, newSeq int64) error {
	_, err := r.s.pool.Exec(ctx,
		`UPDATE audit_ship_state SET last_seq = $1, updated_at = now() WHERE id = true AND last_seq < $1`,
		newSeq)
	return mapError(err)
}
