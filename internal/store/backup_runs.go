package store

import (
	"context"
	"time"
)

// BackupRunHistoryCap bounds retained backup_runs rows (newest-first). The
// scheduled S3 backup engine records one row per attempt and prunes to this cap.
const BackupRunHistoryCap = 100

// BackupRun is one recorded scheduled-backup attempt. Value-free: no key
// material, no ciphertext, no credentials. ObjectKey is the S3 object path only;
// Error is a sanitized category string.
type BackupRun struct {
	ID         int64
	StartedAt  time.Time
	FinishedAt time.Time
	Status     string // success | failure
	ObjectKey  *string
	SizeBytes  *int64
	Error      *string
	CreatedAt  time.Time
}

// BackupRunInput is the value recorded for one attempt.
type BackupRunInput struct {
	StartedAt  time.Time
	FinishedAt time.Time
	Status     string
	ObjectKey  *string
	SizeBytes  *int64
	Error      *string
}

// BackupRunRepo records + reads scheduled-backup run history.
type BackupRunRepo struct{ s *Store }

// NewBackupRunRepo returns a backup-run repository.
func NewBackupRunRepo(s *Store) *BackupRunRepo { return &BackupRunRepo{s: s} }

// insertBackupRunTx inserts one run then prunes to BackupRunHistoryCap, on the
// caller's executor (pool or tx).
func insertBackupRunTx(ctx context.Context, ex runExecer, in BackupRunInput) error {
	if _, err := ex.Exec(ctx,
		`INSERT INTO backup_runs (started_at, finished_at, status, object_key, size_bytes, error)
		 VALUES ($1, $2, $3, $4, $5, $6)`,
		in.StartedAt, in.FinishedAt, in.Status, in.ObjectKey, in.SizeBytes, in.Error); err != nil {
		return mapError(err)
	}
	_, err := ex.Exec(ctx,
		`DELETE FROM backup_runs WHERE id NOT IN (
		   SELECT id FROM backup_runs ORDER BY id DESC LIMIT $1)`,
		BackupRunHistoryCap)
	return mapError(err)
}

// InsertRun records a run in its own transaction, atomically pruning to the
// history cap. Recording each attempt (success or failure) is the audit trail
// the health surface reads.
func (r *BackupRunRepo) InsertRun(ctx context.Context, in BackupRunInput) error {
	tx, err := r.s.pool.Begin(ctx)
	if err != nil {
		return mapError(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := insertBackupRunTx(ctx, tx, in); err != nil {
		return err
	}
	return mapError(tx.Commit(ctx))
}

// ListRuns returns runs newest-first, keyset-paginated by id DESC. cursor=0
// starts at the newest; pass the last returned ID to page older.
func (r *BackupRunRepo) ListRuns(ctx context.Context, cursor int64, limit int) ([]BackupRun, error) {
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	rows, err := r.s.pool.Query(ctx,
		`SELECT id, started_at, finished_at, status, object_key, size_bytes, error, created_at
		   FROM backup_runs
		  WHERE ($1 = 0 OR id < $1)
		  ORDER BY id DESC LIMIT $2`, cursor, limit)
	if err != nil {
		return nil, mapError(err)
	}
	defer rows.Close()
	out := make([]BackupRun, 0, limit)
	for rows.Next() {
		var x BackupRun
		if err := rows.Scan(&x.ID, &x.StartedAt, &x.FinishedAt, &x.Status,
			&x.ObjectKey, &x.SizeBytes, &x.Error, &x.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, x)
	}
	return out, mapError(rows.Err())
}

// LatestRun returns the most recent backup run, or (nil, nil) when none exist.
// Feeds the value-free health snapshot ("last backup time/status").
func (r *BackupRunRepo) LatestRun(ctx context.Context) (*BackupRun, error) {
	runs, err := r.ListRuns(ctx, 0, 1)
	if err != nil {
		return nil, err
	}
	if len(runs) == 0 {
		return nil, nil
	}
	return &runs[0], nil
}
