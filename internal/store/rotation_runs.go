package store

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
)

// RunHistoryCap bounds retained rotation_runs rows per policy (newest-first).
const RunHistoryCap = 100

// RotationRun is one recorded rotation attempt (value-free: no secret material).
type RotationRun struct {
	ID            int64
	PolicyID      string
	StartedAt     time.Time
	EndedAt       time.Time
	Status        string // success | failure
	Error         *string
	ConfigVersion *int
	AttemptNum    int
	CreatedAt     time.Time
}

// RotationRunInput is the value recorded for one attempt.
type RotationRunInput struct {
	PolicyID      string
	StartedAt     time.Time
	EndedAt       time.Time
	Status        string
	Error         *string
	ConfigVersion *int
	AttemptNum    int
}

// runExecer is satisfied by both *pgxpool.Pool and pgx.Tx.
type runExecer interface {
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
}

// insertRotationRunTx inserts one run then prunes to RunHistoryCap for the
// policy, on the caller's executor (pool or tx).
func insertRotationRunTx(ctx context.Context, ex runExecer, in RotationRunInput) error {
	if _, err := ex.Exec(ctx,
		`INSERT INTO rotation_runs (policy_id, started_at, ended_at, status, error, config_version, attempt_num)
		 VALUES ($1::uuid, $2, $3, $4, $5, $6, $7)`,
		in.PolicyID, in.StartedAt, in.EndedAt, in.Status, in.Error, in.ConfigVersion, in.AttemptNum); err != nil {
		return mapError(err)
	}
	_, err := ex.Exec(ctx,
		`DELETE FROM rotation_runs WHERE policy_id=$1::uuid AND id NOT IN (
		   SELECT id FROM rotation_runs WHERE policy_id=$1::uuid ORDER BY id DESC LIMIT $2)`,
		in.PolicyID, RunHistoryCap)
	return mapError(err)
}

// InsertRun records a run in its own transaction (used by tests + any
// standalone caller). The mark-path uses insertRotationRunTx on its own tx.
func (r *RotationRepo) InsertRun(ctx context.Context, in RotationRunInput) error {
	tx, err := r.s.pool.Begin(ctx)
	if err != nil {
		return mapError(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := insertRotationRunTx(ctx, tx, in); err != nil {
		return err
	}
	return mapError(tx.Commit(ctx))
}

// ListRuns returns runs for a policy newest-first, keyset-paginated by id DESC.
// cursor=0 starts at the newest; pass the last returned ID to page older.
func (r *RotationRepo) ListRuns(ctx context.Context, policyID string, cursor int64, limit int) ([]RotationRun, error) {
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	rows, err := r.s.pool.Query(ctx,
		`SELECT id, policy_id, started_at, ended_at, status, error, config_version, attempt_num, created_at
		   FROM rotation_runs
		  WHERE policy_id = $1::uuid AND ($2 = 0 OR id < $2)
		  ORDER BY id DESC LIMIT $3`, policyID, cursor, limit)
	if err != nil {
		return nil, mapError(err)
	}
	defer rows.Close()
	out := make([]RotationRun, 0, limit)
	for rows.Next() {
		var x RotationRun
		if err := rows.Scan(&x.ID, &x.PolicyID, &x.StartedAt, &x.EndedAt, &x.Status,
			&x.Error, &x.ConfigVersion, &x.AttemptNum, &x.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, x)
	}
	return out, mapError(rows.Err())
}
