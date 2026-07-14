package store

import (
	"context"
	"time"
)

// SyncRun is one recorded sync attempt (value-free: no secret material).
type SyncRun struct {
	ID            int64
	TargetID      string
	StartedAt     time.Time
	EndedAt       time.Time
	Status        string // success | failure
	Error         *string
	ConfigVersion *int
	// KeysCount is the number of managed keys pushed on a success run (0 on failure).
	KeysCount int
	// AttemptNum is the target's failure_count this run corresponds to, NOT a
	// monotonic attempt counter (success records the prior failure streak, 0 for a
	// first-ever success; failure records the post-increment count, 1 for a first
	// failure). Display as context, not "attempt N of M".
	AttemptNum int
	CreatedAt  time.Time
}

type SyncRunInput struct {
	TargetID      string
	StartedAt     time.Time
	EndedAt       time.Time
	Status        string
	Error         *string
	ConfigVersion *int
	KeysCount     int
	AttemptNum    int
}

func insertSyncRunTx(ctx context.Context, ex runExecer, in SyncRunInput) error {
	if _, err := ex.Exec(ctx,
		`INSERT INTO sync_runs (target_id, started_at, ended_at, status, error, config_version, keys_count, attempt_num)
		 VALUES ($1::uuid, $2, $3, $4, $5, $6, $7, $8)`,
		in.TargetID, in.StartedAt, in.EndedAt, in.Status, in.Error, in.ConfigVersion, in.KeysCount, in.AttemptNum); err != nil {
		return mapError(err)
	}
	_, err := ex.Exec(ctx,
		`DELETE FROM sync_runs WHERE target_id=$1::uuid AND id NOT IN (
		   SELECT id FROM sync_runs WHERE target_id=$1::uuid ORDER BY id DESC LIMIT $2)`,
		in.TargetID, RunHistoryCap)
	return mapError(err)
}

// InsertRun records a run in its own transaction (tests + standalone callers).
func (r *SyncTargetRepo) InsertRun(ctx context.Context, in SyncRunInput) error {
	tx, err := r.s.pool.Begin(ctx)
	if err != nil {
		return mapError(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := insertSyncRunTx(ctx, tx, in); err != nil {
		return err
	}
	return mapError(tx.Commit(ctx))
}

// ListRuns returns runs for a target newest-first, keyset-paginated by id DESC.
func (r *SyncTargetRepo) ListRuns(ctx context.Context, targetID string, cursor int64, limit int) ([]SyncRun, error) {
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	rows, err := r.s.pool.Query(ctx,
		`SELECT id, target_id, started_at, ended_at, status, error, config_version, keys_count, attempt_num, created_at
		   FROM sync_runs
		  WHERE target_id = $1::uuid AND ($2 = 0 OR id < $2)
		  ORDER BY id DESC LIMIT $3`, targetID, cursor, limit)
	if err != nil {
		return nil, mapError(err)
	}
	defer rows.Close()
	out := make([]SyncRun, 0, limit)
	for rows.Next() {
		var x SyncRun
		if err := rows.Scan(&x.ID, &x.TargetID, &x.StartedAt, &x.EndedAt, &x.Status,
			&x.Error, &x.ConfigVersion, &x.KeysCount, &x.AttemptNum, &x.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, x)
	}
	return out, mapError(rows.Err())
}
