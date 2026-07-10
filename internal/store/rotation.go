package store

import (
	"context"
	"time"
)

// RotationPolicy is one rotation binding: a rotator over a single secret key.
// The *_ct/nonce/wrapped_dek fields hold the envelope-encrypted rotator config
// blob; pending_* holds an in-flight generated value awaiting commit.
type RotationPolicy struct {
	ID                  string
	ProjectID           string
	ConfigID            string
	SecretKey           string
	Type                string // "postgres" | "webhook"
	IntervalSeconds     int64
	NextRotationAt      time.Time
	Status              string // "active" | "failed" | "paused"
	FailureCount        int
	LastError           *string
	LastRotatedAt       *time.Time
	LastConfigVersion   *int
	ConfigCT            []byte
	ConfigNonce         []byte
	ConfigWrappedDEK    []byte
	ConfigDEKKEKVersion int
	PendingCT           []byte
	PendingNonce        []byte
	PendingWrappedDEK   []byte
	PendingState        *string // nil or "applying"
	CreatedBy           string
	CreatedAt           time.Time
	UpdatedAt           time.Time
}

// RotationRepo persists rotation policies (crypto-blind).
type RotationRepo struct{ s *Store }

func NewRotationRepo(s *Store) *RotationRepo { return &RotationRepo{s: s} }

const rotationCols = `id::text, project_id::text, config_id::text, secret_key, type,
	interval_seconds, next_rotation_at, status, failure_count, last_error,
	last_rotated_at, last_config_version, config_ct, config_nonce, config_wrapped_dek,
	config_dek_kek_version, pending_ct, pending_nonce, pending_wrapped_dek, pending_state,
	created_by, created_at, updated_at`

func scanPolicy(row interface{ Scan(...any) error }) (*RotationPolicy, error) {
	var p RotationPolicy
	if err := row.Scan(&p.ID, &p.ProjectID, &p.ConfigID, &p.SecretKey, &p.Type,
		&p.IntervalSeconds, &p.NextRotationAt, &p.Status, &p.FailureCount, &p.LastError,
		&p.LastRotatedAt, &p.LastConfigVersion, &p.ConfigCT, &p.ConfigNonce, &p.ConfigWrappedDEK,
		&p.ConfigDEKKEKVersion, &p.PendingCT, &p.PendingNonce, &p.PendingWrappedDEK, &p.PendingState,
		&p.CreatedBy, &p.CreatedAt, &p.UpdatedAt); err != nil {
		return nil, mapError(err)
	}
	return &p, nil
}

// Create inserts a policy. Duplicate (config_id, secret_key) → ErrAlreadyExists.
func (r *RotationRepo) Create(ctx context.Context, p *RotationPolicy) (*RotationPolicy, error) {
	_, err := r.s.pool.Exec(ctx,
		`INSERT INTO rotation_policies
		 (id, project_id, config_id, secret_key, type, interval_seconds, next_rotation_at,
		  config_ct, config_nonce, config_wrapped_dek, config_dek_kek_version, created_by)
		 VALUES ($1::uuid,$2::uuid,$3::uuid,$4,$5,$6,$7,$8,$9,$10,$11,$12)`,
		p.ID, p.ProjectID, p.ConfigID, p.SecretKey, p.Type, p.IntervalSeconds, p.NextRotationAt,
		p.ConfigCT, p.ConfigNonce, p.ConfigWrappedDEK, p.ConfigDEKKEKVersion, p.CreatedBy)
	if err != nil {
		return nil, mapError(err)
	}
	return r.Get(ctx, p.ID)
}

func (r *RotationRepo) Get(ctx context.Context, id string) (*RotationPolicy, error) {
	return scanPolicy(r.s.pool.QueryRow(ctx,
		`SELECT `+rotationCols+` FROM rotation_policies WHERE id = $1::uuid`, id))
}

// GetByConfigKey resolves the unique policy on (config_id, secret_key).
func (r *RotationRepo) GetByConfigKey(ctx context.Context, configID, key string) (*RotationPolicy, error) {
	return scanPolicy(r.s.pool.QueryRow(ctx,
		`SELECT `+rotationCols+` FROM rotation_policies WHERE config_id = $1::uuid AND secret_key = $2`,
		configID, key))
}

// ListByProject returns policies for a project, newest first.
func (r *RotationRepo) ListByProject(ctx context.Context, projectID string) ([]*RotationPolicy, error) {
	rows, err := r.s.pool.Query(ctx,
		`SELECT `+rotationCols+` FROM rotation_policies WHERE project_id = $1::uuid ORDER BY created_at DESC, id`,
		projectID)
	if err != nil {
		return nil, mapError(err)
	}
	defer rows.Close()
	var out []*RotationPolicy
	for rows.Next() {
		p, err := scanPolicy(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, mapError(rows.Err())
}

// Update sets interval/status and (optionally) a new encrypted config blob.
// nil config* leaves the blob unchanged.
func (r *RotationRepo) Update(ctx context.Context, id string, intervalSeconds *int64, status *string,
	configCT, configNonce, configWrappedDEK []byte, configKEKVer *int) error {
	return r.s.execAffectingOne(ctx,
		`UPDATE rotation_policies SET
		   interval_seconds       = COALESCE($2, interval_seconds),
		   status                 = COALESCE($3, status),
		   config_ct              = COALESCE($4, config_ct),
		   config_nonce           = COALESCE($5, config_nonce),
		   config_wrapped_dek     = COALESCE($6, config_wrapped_dek),
		   config_dek_kek_version = COALESCE($7, config_dek_kek_version),
		   updated_at             = now()
		 WHERE id = $1::uuid`,
		id, intervalSeconds, status, configCT, configNonce, configWrappedDEK, configKEKVer)
}

func (r *RotationRepo) Delete(ctx context.Context, id string) error {
	return r.s.execAffectingOne(ctx, `DELETE FROM rotation_policies WHERE id = $1::uuid`, id)
}

// ClaimDue returns active policies that are due or have an in-flight pending
// value (crash recovery), oldest-due first, up to limit.
//
// Single-node deployments run exactly one scheduler goroutine, so a plain
// SELECT is race-free; we deliberately do NOT hold FOR UPDATE row locks here
// because rotation performs network I/O (ALTER ROLE / webhook) that must not
// run inside a long-lived transaction. A future multi-node design would add a
// claimed_at column + FOR UPDATE SKIP LOCKED.
func (r *RotationRepo) ClaimDue(ctx context.Context, now time.Time, limit int) ([]*RotationPolicy, error) {
	rows, err := r.s.pool.Query(ctx,
		`SELECT `+rotationCols+` FROM rotation_policies
		 WHERE status = 'active' AND (next_rotation_at <= $1 OR pending_state IS NOT NULL)
		 ORDER BY next_rotation_at ASC LIMIT $2`, now, limit)
	if err != nil {
		return nil, mapError(err)
	}
	defer rows.Close()
	var out []*RotationPolicy
	for rows.Next() {
		p, err := scanPolicy(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, mapError(rows.Err())
}

// SetPending stores an in-flight encrypted value and marks the policy applying.
func (r *RotationRepo) SetPending(ctx context.Context, id string, ct, nonce, wrappedDEK []byte) error {
	return r.s.execAffectingOne(ctx,
		`UPDATE rotation_policies SET pending_ct=$2, pending_nonce=$3, pending_wrapped_dek=$4,
		   pending_state='applying', updated_at=now() WHERE id=$1::uuid`,
		id, ct, nonce, wrappedDEK)
}

// MarkRotated records a successful rotation: clears pending, resets failure
// state, advances next_rotation_at, and stores the produced config version.
func (r *RotationRepo) MarkRotated(ctx context.Context, id string, configVersion int, next time.Time) error {
	return r.s.execAffectingOne(ctx,
		`UPDATE rotation_policies SET
		   pending_ct=NULL, pending_nonce=NULL, pending_wrapped_dek=NULL, pending_state=NULL,
		   failure_count=0, status='active', last_error=NULL,
		   last_rotated_at=now(), last_config_version=$2, next_rotation_at=$3, updated_at=now()
		 WHERE id=$1::uuid`, id, configVersion, next)
}

// MarkFailure records a failed attempt: bumps failure_count, stores a sanitized
// error, sets the backoff retry time, and flips to 'failed' at the threshold.
func (r *RotationRepo) MarkFailure(ctx context.Context, id, sanitizedErr string, next time.Time, threshold int) error {
	return r.s.execAffectingOne(ctx,
		`UPDATE rotation_policies SET
		   failure_count = failure_count + 1,
		   last_error    = $2,
		   next_rotation_at = $3,
		   status = CASE WHEN failure_count + 1 >= $4 THEN 'failed' ELSE status END,
		   updated_at = now()
		 WHERE id=$1::uuid`, id, sanitizedErr, next, threshold)
}
