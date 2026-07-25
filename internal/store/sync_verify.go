package store

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

// VerifyRunHistoryCap bounds retained sync_verify_runs rows per target
// (newest-first), mirroring RunHistoryCap for rotation/sync runs.
const VerifyRunHistoryCap = 100

// DefaultVerifyIntervalSeconds is the per-target verification pace applied when
// a target has no sync_verify_state row yet. Verification reads values back
// from the destination, so it is deliberately far slower than the push tick.
const DefaultVerifyIntervalSeconds int64 = 3600

// SyncVerifyState is the per-target drift-verification schedule + last result.
// It carries NO secret material: only booleans, timings, a status word, and a
// drift COUNT.
type SyncVerifyState struct {
	TargetID        string
	Enabled         bool
	IntervalSeconds int64
	NextVerifyAt    time.Time
	LastVerifyAt    *time.Time
	LastStatus      *string // clean | drift | error | unsupported
	LastDriftCount  int
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

// SyncVerifyRun is one recorded verification pass. Value-free by construction:
// key NAMES, counts, a capability word, and a sanitized error category only.
type SyncVerifyRun struct {
	ID             int64
	TargetID       string
	StartedAt      time.Time
	EndedAt        time.Time
	Status         string // clean | drift | error | unsupported
	Capability     string // values | names_only | none
	ValuesCompared bool
	MissingKeys     []string
	ModifiedKeys    []string
	ExtraKeys       []string
	UnreadableKeys  []string
	MissingCount    int
	ModifiedCount   int
	ExtraCount      int
	UnreadableCount int
	CheckedCount    int
	Error           *string
	CreatedAt       time.Time
}

// SyncVerifyRunInput is the insert payload for one verification pass.
type SyncVerifyRunInput struct {
	TargetID       string
	StartedAt      time.Time
	EndedAt        time.Time
	Status         string
	Capability     string
	ValuesCompared bool
	MissingKeys     []string
	ModifiedKeys    []string
	ExtraKeys       []string
	UnreadableKeys  []string
	MissingCount    int
	ModifiedCount   int
	ExtraCount      int
	UnreadableCount int
	CheckedCount    int
	Error           *string
}

const verifyStateCols = `target_id::text, enabled, interval_seconds, next_verify_at,
	last_verify_at, last_status, last_drift_count, created_at, updated_at`

func scanVerifyState(row interface{ Scan(...any) error }) (*SyncVerifyState, error) {
	var v SyncVerifyState
	if err := row.Scan(&v.TargetID, &v.Enabled, &v.IntervalSeconds, &v.NextVerifyAt,
		&v.LastVerifyAt, &v.LastStatus, &v.LastDriftCount, &v.CreatedAt, &v.UpdatedAt); err != nil {
		return nil, mapError(err)
	}
	return &v, nil
}

// DefaultVerifyState is the effective state of a target that has no
// sync_verify_state row yet: enabled, default pace, due now. Materializing this
// lazily (rather than seeding a row on target create) keeps the verify feature
// entirely inside its own migration and store file.
func DefaultVerifyState(targetID string) SyncVerifyState {
	return SyncVerifyState{
		TargetID: targetID, Enabled: true,
		IntervalSeconds: DefaultVerifyIntervalSeconds,
	}
}

// GetVerifyState returns a target's verify state, or the lazy default when no
// row exists yet. It does NOT verify that the target exists — callers resolve
// the target first.
func (r *SyncTargetRepo) GetVerifyState(ctx context.Context, targetID string) (SyncVerifyState, error) {
	v, err := scanVerifyState(r.s.pool.QueryRow(ctx,
		`SELECT `+verifyStateCols+` FROM sync_verify_state WHERE target_id = $1::uuid`, targetID))
	if errors.Is(err, ErrNotFound) {
		return DefaultVerifyState(targetID), nil
	}
	if err != nil {
		return SyncVerifyState{}, err
	}
	return *v, nil
}

// VerifyStatesByProject returns target_id → state for every materialized verify
// state row belonging to a project. Targets absent from the map use
// DefaultVerifyState. One query for a whole project list view.
func (r *SyncTargetRepo) VerifyStatesByProject(ctx context.Context, projectID string) (map[string]SyncVerifyState, error) {
	rows, err := r.s.pool.Query(ctx,
		`SELECT v.target_id::text, v.enabled, v.interval_seconds, v.next_verify_at,
		        v.last_verify_at, v.last_status, v.last_drift_count, v.created_at, v.updated_at
		   FROM sync_verify_state v
		   JOIN sync_targets t ON t.id = v.target_id
		  WHERE t.project_id = $1::uuid`, projectID)
	if err != nil {
		return nil, mapError(err)
	}
	defer rows.Close()
	out := map[string]SyncVerifyState{}
	for rows.Next() {
		v, err := scanVerifyState(rows)
		if err != nil {
			return nil, err
		}
		out[v.TargetID] = *v
	}
	return out, mapError(rows.Err())
}

// SetVerifySchedule upserts the operator-controlled knobs (enabled / pace). nil
// args leave the corresponding stored value unchanged. When the row does not
// exist yet it is materialized from the lazy defaults first.
func (r *SyncTargetRepo) SetVerifySchedule(ctx context.Context, targetID string, enabled *bool, intervalSeconds *int64, now time.Time) error {
	next := now
	if intervalSeconds != nil {
		next = now.Add(time.Duration(*intervalSeconds) * time.Second)
	}
	_, err := r.s.pool.Exec(ctx,
		`INSERT INTO sync_verify_state (target_id, enabled, interval_seconds, next_verify_at)
		 VALUES ($1::uuid, COALESCE($2::boolean, true), COALESCE($3::bigint, $5::bigint), $4)
		 ON CONFLICT (target_id) DO UPDATE SET
		   enabled          = COALESCE($2::boolean, sync_verify_state.enabled),
		   interval_seconds = COALESCE($3::bigint, sync_verify_state.interval_seconds),
		   next_verify_at   = CASE WHEN $3::bigint IS NULL THEN sync_verify_state.next_verify_at ELSE $4 END,
		   updated_at       = now()`,
		targetID, enabled, intervalSeconds, next, DefaultVerifyIntervalSeconds)
	return mapError(err)
}

// ClaimVerifyDueIDs returns the ids of active sync targets whose verification is
// due, oldest-due first. A target with no sync_verify_state row is treated as
// enabled and immediately due (COALESCE defaults), so drift verification starts
// working for pre-existing targets without a data backfill.
func (r *SyncTargetRepo) ClaimVerifyDueIDs(ctx context.Context, now time.Time, limit int) ([]string, error) {
	rows, err := r.s.pool.Query(ctx,
		`SELECT t.id::text
		   FROM sync_targets t
		   LEFT JOIN sync_verify_state v ON v.target_id = t.id
		  WHERE t.status = 'active'
		    AND COALESCE(v.enabled, true)
		    AND COALESCE(v.next_verify_at, $1) <= $1
		  ORDER BY COALESCE(v.next_verify_at, t.created_at) ASC
		  LIMIT $2`, now, limit)
	if err != nil {
		return nil, mapError(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, mapError(rows.Err())
}

// RecordVerifyRun persists one verification pass and advances the target's
// verify schedule, in a single transaction. History is capped per target.
func (r *SyncTargetRepo) RecordVerifyRun(ctx context.Context, in SyncVerifyRunInput, next time.Time) error {
	return r.s.withTx(ctx, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx,
			`INSERT INTO sync_verify_runs
			   (target_id, started_at, ended_at, status, capability, values_compared,
			    missing_keys, modified_keys, extra_keys, unreadable_keys,
			    missing_count, modified_count, extra_count, unreadable_count, checked_count, error)
			 VALUES ($1::uuid,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16)`,
			in.TargetID, in.StartedAt, in.EndedAt, in.Status, in.Capability, in.ValuesCompared,
			nonNilStrings(in.MissingKeys), nonNilStrings(in.ModifiedKeys), nonNilStrings(in.ExtraKeys),
			nonNilStrings(in.UnreadableKeys),
			in.MissingCount, in.ModifiedCount, in.ExtraCount, in.UnreadableCount,
			in.CheckedCount, in.Error); err != nil {
			return mapError(err)
		}
		if _, err := tx.Exec(ctx,
			`DELETE FROM sync_verify_runs WHERE target_id=$1::uuid AND id NOT IN (
			   SELECT id FROM sync_verify_runs WHERE target_id=$1::uuid ORDER BY id DESC LIMIT $2)`,
			in.TargetID, VerifyRunHistoryCap); err != nil {
			return mapError(err)
		}
		drift := in.MissingCount + in.ModifiedCount + in.ExtraCount
		_, err := tx.Exec(ctx,
			`INSERT INTO sync_verify_state
			   (target_id, interval_seconds, next_verify_at, last_verify_at, last_status, last_drift_count)
			 VALUES ($1::uuid, $5, $2, $3, $4, $6)
			 ON CONFLICT (target_id) DO UPDATE SET
			   next_verify_at   = $2,
			   last_verify_at   = $3,
			   last_status      = $4,
			   last_drift_count = $6,
			   updated_at       = now()`,
			in.TargetID, next, in.EndedAt, in.Status, DefaultVerifyIntervalSeconds, drift)
		return mapError(err)
	})
}

// ListVerifyRuns returns verification passes for a target newest-first,
// keyset-paginated by id DESC (mirrors ListRuns).
func (r *SyncTargetRepo) ListVerifyRuns(ctx context.Context, targetID string, cursor int64, limit int) ([]SyncVerifyRun, error) {
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	rows, err := r.s.pool.Query(ctx,
		`SELECT id, target_id::text, started_at, ended_at, status, capability, values_compared,
		        missing_keys, modified_keys, extra_keys, unreadable_keys,
		        missing_count, modified_count, extra_count, unreadable_count, checked_count,
		        error, created_at
		   FROM sync_verify_runs
		  WHERE target_id = $1::uuid AND ($2 = 0 OR id < $2)
		  ORDER BY id DESC LIMIT $3`, targetID, cursor, limit)
	if err != nil {
		return nil, mapError(err)
	}
	defer rows.Close()
	out := make([]SyncVerifyRun, 0, limit)
	for rows.Next() {
		var x SyncVerifyRun
		if err := rows.Scan(&x.ID, &x.TargetID, &x.StartedAt, &x.EndedAt, &x.Status, &x.Capability,
			&x.ValuesCompared, &x.MissingKeys, &x.ModifiedKeys, &x.ExtraKeys, &x.UnreadableKeys,
			&x.MissingCount, &x.ModifiedCount, &x.ExtraCount, &x.UnreadableCount, &x.CheckedCount,
			&x.Error, &x.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, x)
	}
	return out, mapError(rows.Err())
}

// nonNilStrings normalizes a nil slice to an empty one so the NOT NULL text[]
// columns never receive NULL.
func nonNilStrings(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}
