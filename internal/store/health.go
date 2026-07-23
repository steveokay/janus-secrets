package store

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"
)

// HealthRepo runs cheap value-free aggregate COUNTs for the /metrics and
// /v1/sys/status health surfaces. Every query is a single indexed COUNT and
// callers pass a bounded context so a rapid scrape cadence can't hammer
// Postgres (the API layer also caches these ~5s).
type HealthRepo struct{ s *Store }

// NewHealthRepo returns a health repository.
func NewHealthRepo(s *Store) *HealthRepo { return &HealthRepo{s: s} }

// RotationRunsFailed counts recorded rotation_runs with status='failure'.
func (r *HealthRepo) RotationRunsFailed(ctx context.Context) (int64, error) {
	var n int64
	err := r.s.pool.QueryRow(ctx,
		`SELECT count(*) FROM rotation_runs WHERE status = 'failure'`).Scan(&n)
	return n, mapError(err)
}

// SyncRunsFailed counts recorded sync_runs with status='failure'.
func (r *HealthRepo) SyncRunsFailed(ctx context.Context) (int64, error) {
	var n int64
	err := r.s.pool.QueryRow(ctx,
		`SELECT count(*) FROM sync_runs WHERE status = 'failure'`).Scan(&n)
	return n, mapError(err)
}

// DynamicLeasesActive counts dynamic_leases with status='active'.
func (r *HealthRepo) DynamicLeasesActive(ctx context.Context) (int64, error) {
	var n int64
	err := r.s.pool.QueryRow(ctx,
		`SELECT count(*) FROM dynamic_leases WHERE status = 'active'`).Scan(&n)
	return n, mapError(err)
}

// AuditHeadSeq returns the highest audit-event seq (the chain head), or 0 when
// the log is empty. Cheap: a single ORDER BY seq DESC LIMIT 1 on the PK.
func (r *HealthRepo) AuditHeadSeq(ctx context.Context) (int64, error) {
	var seq int64
	err := r.s.pool.QueryRow(ctx,
		`SELECT coalesce(max(seq), 0) FROM audit_events`).Scan(&seq)
	return seq, mapError(err)
}

// AuditEventCount returns the total number of audit events.
func (r *HealthRepo) AuditEventCount(ctx context.Context) (int64, error) {
	var n int64
	err := r.s.pool.QueryRow(ctx,
		`SELECT count(*) FROM audit_events`).Scan(&n)
	return n, mapError(err)
}

// LatestBackupRun returns the most recent scheduled-backup run for the health
// snapshot (last backup time/status), or (nil, nil) when none exist. Value-free:
// timestamp, status, and object path only.
func (r *HealthRepo) LatestBackupRun(ctx context.Context) (*BackupRun, error) {
	return NewBackupRunRepo(r.s).LatestRun(ctx)
}

// PoolStat exposes the pgx pool statistics for the DB-pool metrics + the health
// snapshot. Value-free: connection counts only.
func (s *Store) PoolStat() *pgxpool.Stat { return s.pool.Stat() }
