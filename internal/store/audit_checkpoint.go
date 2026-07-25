package store

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

// AuditCheckpointRepo persists signed anchors over the audit hash chain and the
// prune operation that hard-deletes a verified prefix. Both live here so the
// audit engine never imports pgx directly.
type AuditCheckpointRepo struct{ s *Store }

// NewAuditCheckpointRepo constructs the repo over a store.
func NewAuditCheckpointRepo(s *Store) *AuditCheckpointRepo { return &AuditCheckpointRepo{s: s} }

// AuditCheckpointRow is one persisted checkpoint. The engine owns computing the
// MAC; the store persists/reads it verbatim.
type AuditCheckpointRow struct {
	ThroughSeq  int64
	ThroughHash []byte
	EventCount  int64
	MAC         []byte
	CreatedAt   time.Time
}

// Head returns the current audit chain head (highest seq + its hash) and the
// total number of events. At genesis (empty table) it returns seq 0, nil hash,
// count 0. Used to capture a checkpoint.
func (r *AuditCheckpointRepo) Head(ctx context.Context) (seq int64, hash []byte, count int64, err error) {
	err = r.s.pool.QueryRow(ctx,
		`SELECT COALESCE(MAX(seq), 0), COUNT(*) FROM audit_events`).Scan(&seq, &count)
	if err != nil {
		return 0, nil, 0, mapError(err)
	}
	if seq == 0 {
		return 0, nil, 0, nil
	}
	if err = r.s.pool.QueryRow(ctx,
		`SELECT hash FROM audit_events WHERE seq = $1`, seq).Scan(&hash); err != nil {
		return 0, nil, 0, mapError(err)
	}
	return seq, hash, count, nil
}

// Insert persists a new checkpoint. The UNIQUE(through_seq) constraint makes a
// duplicate anchor at the same seq a store error (mapped to ErrConflict).
func (r *AuditCheckpointRepo) Insert(ctx context.Context, cp AuditCheckpointRow) error {
	_, err := r.s.pool.Exec(ctx,
		`INSERT INTO audit_checkpoints (through_seq, through_hash, event_count, mac)
		 VALUES ($1, $2, $3, $4)`,
		cp.ThroughSeq, cp.ThroughHash, cp.EventCount, cp.MAC)
	return mapError(err)
}

// Latest returns the highest-seq checkpoint, or (nil, nil) when none exist.
func (r *AuditCheckpointRepo) Latest(ctx context.Context) (*AuditCheckpointRow, error) {
	var cp AuditCheckpointRow
	err := r.s.pool.QueryRow(ctx,
		`SELECT through_seq, through_hash, event_count, mac, created_at
		 FROM audit_checkpoints ORDER BY through_seq DESC LIMIT 1`).
		Scan(&cp.ThroughSeq, &cp.ThroughHash, &cp.EventCount, &cp.MAC, &cp.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, mapError(err)
	}
	return &cp, nil
}

// PruneThrough hard-deletes every audit event with seq <= throughSeq and returns
// the number of rows removed. It never touches audit_checkpoints (the anchor
// rows survive; only the anchored-and-verified event prefix is removed). The
// caller is responsible for the retention guards (a valid checkpoint exists, the
// ship high-water mark is respected); this method only executes the delete.
func (r *AuditCheckpointRepo) PruneThrough(ctx context.Context, throughSeq int64) (int64, error) {
	tag, err := r.s.pool.Exec(ctx,
		`DELETE FROM audit_events WHERE seq <= $1`, throughSeq)
	if err != nil {
		return 0, mapError(err)
	}
	return tag.RowsAffected(), nil
}
