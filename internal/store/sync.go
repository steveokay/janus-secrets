package store

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
)

// SyncTarget is one sync binding: a config pushed to an external destination
// (a GitHub repo's Actions secrets, or a Kubernetes Secret). The creds_*
// fields hold the envelope-encrypted destination credentials blob (crypto-
// blind to this package); addr holds the provider-specific destination
// address as jsonb (e.g. {"owner":"o","repo":"r"} or {"namespace":"n",...}).
type SyncTarget struct {
	ID                  string
	ProjectID           string
	ConfigID            string
	Provider            string // "github" | "k8s"
	Prune               bool
	IntervalSeconds     int64
	NextSyncAt          time.Time
	Status              string // "active" | "failed" | "paused"
	FailureCount        int
	LastError           *string
	LastSyncedAt        *time.Time
	SyncedConfigVersion *int
	CredsCT             []byte
	CredsNonce          []byte
	CredsWrappedDEK     []byte
	CredsDEKKEKVersion  int
	Addr                []byte // raw jsonb bytes
	ManagedKeys         []string
	SyncedFingerprint   []byte
	CreatedBy           string
	CreatedAt           time.Time
	UpdatedAt           time.Time
}

// SyncTargetRepo persists sync targets (crypto-blind).
type SyncTargetRepo struct{ s *Store }

func NewSyncTargetRepo(s *Store) *SyncTargetRepo { return &SyncTargetRepo{s: s} }

const syncCols = `id::text, project_id::text, config_id::text, provider, prune,
	interval_seconds, next_sync_at, status, failure_count, last_error,
	last_synced_at, synced_config_version, creds_ct, creds_nonce, creds_wrapped_dek,
	creds_dek_kek_version, addr, managed_keys, synced_fingerprint,
	created_by, created_at, updated_at`

func scanSyncTarget(row interface{ Scan(...any) error }) (*SyncTarget, error) {
	var t SyncTarget
	if err := row.Scan(&t.ID, &t.ProjectID, &t.ConfigID, &t.Provider, &t.Prune,
		&t.IntervalSeconds, &t.NextSyncAt, &t.Status, &t.FailureCount, &t.LastError,
		&t.LastSyncedAt, &t.SyncedConfigVersion, &t.CredsCT, &t.CredsNonce, &t.CredsWrappedDEK,
		&t.CredsDEKKEKVersion, &t.Addr, &t.ManagedKeys, &t.SyncedFingerprint,
		&t.CreatedBy, &t.CreatedAt, &t.UpdatedAt); err != nil {
		return nil, mapError(err)
	}
	return &t, nil
}

// Create inserts a sync target. managed_keys is omitted from the INSERT so
// the column default ('{}') applies. Duplicate (config_id, provider, addr) →
// ErrAlreadyExists.
func (r *SyncTargetRepo) Create(ctx context.Context, t *SyncTarget) (*SyncTarget, error) {
	_, err := r.s.pool.Exec(ctx,
		`INSERT INTO sync_targets
		 (id, project_id, config_id, provider, prune, interval_seconds, next_sync_at,
		  creds_ct, creds_nonce, creds_wrapped_dek, creds_dek_kek_version, addr, created_by)
		 VALUES ($1::uuid,$2::uuid,$3::uuid,$4,$5,$6,$7,$8,$9,$10,$11,$12::jsonb,$13)`,
		t.ID, t.ProjectID, t.ConfigID, t.Provider, t.Prune, t.IntervalSeconds, t.NextSyncAt,
		t.CredsCT, t.CredsNonce, t.CredsWrappedDEK, t.CredsDEKKEKVersion, t.Addr, t.CreatedBy)
	if err != nil {
		return nil, mapError(err)
	}
	return r.Get(ctx, t.ID)
}

func (r *SyncTargetRepo) Get(ctx context.Context, id string) (*SyncTarget, error) {
	return scanSyncTarget(r.s.pool.QueryRow(ctx,
		`SELECT `+syncCols+` FROM sync_targets WHERE id = $1::uuid`, id))
}

// ListByProject returns sync targets for a project, newest first.
func (r *SyncTargetRepo) ListByProject(ctx context.Context, projectID string) ([]*SyncTarget, error) {
	rows, err := r.s.pool.Query(ctx,
		`SELECT `+syncCols+` FROM sync_targets WHERE project_id = $1::uuid ORDER BY created_at DESC, id`,
		projectID)
	if err != nil {
		return nil, mapError(err)
	}
	defer rows.Close()
	var out []*SyncTarget
	for rows.Next() {
		t, err := scanSyncTarget(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, mapError(rows.Err())
}

// Update sets interval/prune/status and (optionally) a new encrypted creds
// blob and/or destination address. nil args leave the corresponding
// column(s) unchanged; the creds group (ct/nonce/wrappedDEK/kekVer) is
// treated as a single unit — pass all four or none.
func (r *SyncTargetRepo) Update(ctx context.Context, id string, intervalSeconds *int64, prune *bool, status *string,
	credsCT, credsNonce, credsWrapped []byte, credsKEKVer *int, addr []byte) error {
	return r.s.execAffectingOne(ctx,
		`UPDATE sync_targets SET
		   interval_seconds      = COALESCE($2, interval_seconds),
		   prune                 = COALESCE($3, prune),
		   status                = COALESCE($4, status),
		   creds_ct              = COALESCE($5, creds_ct),
		   creds_nonce           = COALESCE($6, creds_nonce),
		   creds_wrapped_dek     = COALESCE($7, creds_wrapped_dek),
		   creds_dek_kek_version = COALESCE($8, creds_dek_kek_version),
		   addr                  = COALESCE($9::jsonb, addr),
		   updated_at            = now()
		 WHERE id = $1::uuid`,
		id, intervalSeconds, prune, status, credsCT, credsNonce, credsWrapped, credsKEKVer, addr)
}

func (r *SyncTargetRepo) Delete(ctx context.Context, id string) error {
	return r.s.execAffectingOne(ctx, `DELETE FROM sync_targets WHERE id = $1::uuid`, id)
}

// ClaimDue returns active targets whose next_sync_at is due, oldest-due
// first, up to limit. Mirrors RotationRepo.ClaimDue: a single
// next_sync_at <= now predicate serves both crash-recovery and backoff, so
// there is deliberately no separate pending-state clause (sync has no
// in-flight pending value to resume, unlike rotation).
func (r *SyncTargetRepo) ClaimDue(ctx context.Context, now time.Time, limit int) ([]*SyncTarget, error) {
	rows, err := r.s.pool.Query(ctx,
		`SELECT `+syncCols+` FROM sync_targets
		 WHERE status = 'active' AND next_sync_at <= $1
		 ORDER BY next_sync_at ASC LIMIT $2`, now, limit)
	if err != nil {
		return nil, mapError(err)
	}
	defer rows.Close()
	var out []*SyncTarget
	for rows.Next() {
		t, err := scanSyncTarget(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, mapError(rows.Err())
}

// MarkSynced records a successful sync: resets failure state, advances
// next_sync_at, and stores the managed-key set, content fingerprint, and
// synced config version.
func (r *SyncTargetRepo) MarkSynced(ctx context.Context, id string, managedKeys []string, fingerprint []byte, configVersion int, next, startedAt time.Time, attemptNum int) error {
	return r.s.withTx(ctx, func(tx pgx.Tx) error {
		ct, err := tx.Exec(ctx,
			`UPDATE sync_targets SET
			   managed_keys = $2, synced_fingerprint = $3, synced_config_version = $4,
			   last_synced_at = now(), failure_count = 0, status = 'active', last_error = NULL,
			   next_sync_at = $5, updated_at = now()
			 WHERE id = $1::uuid`, id, managedKeys, fingerprint, configVersion, next)
		if err != nil {
			return mapError(err)
		}
		if ct.RowsAffected() != 1 {
			return ErrNotFound
		}
		cv := configVersion
		return insertSyncRunTx(ctx, tx, SyncRunInput{
			TargetID: id, StartedAt: startedAt, EndedAt: time.Now(),
			Status: "success", ConfigVersion: &cv, KeysCount: len(managedKeys), AttemptNum: attemptNum,
		})
	})
}

// MarkFailure records a failed attempt: bumps failure_count, stores a
// sanitized error, sets the backoff retry time, and flips to 'failed' at the
// threshold. Mirrors RotationRepo.MarkFailure exactly.
func (r *SyncTargetRepo) MarkFailure(ctx context.Context, id, sanitizedErr string, next time.Time, threshold int, startedAt time.Time, attemptNum int) error {
	return r.s.withTx(ctx, func(tx pgx.Tx) error {
		ct, err := tx.Exec(ctx,
			`UPDATE sync_targets SET
			   failure_count = failure_count + 1,
			   last_error    = $2,
			   next_sync_at  = $3,
			   status = CASE WHEN failure_count + 1 >= $4 THEN 'failed' ELSE status END,
			   updated_at = now()
			 WHERE id=$1::uuid`, id, sanitizedErr, next, threshold)
		if err != nil {
			return mapError(err)
		}
		if ct.RowsAffected() != 1 {
			return ErrNotFound
		}
		e := sanitizedErr
		return insertSyncRunTx(ctx, tx, SyncRunInput{
			TargetID: id, StartedAt: startedAt, EndedAt: time.Now(),
			Status: "failure", Error: &e, ConfigVersion: nil, KeysCount: 0, AttemptNum: attemptNum,
		})
	})
}

// PrepareSyncNow readies a target for a manual sync: it makes the target
// immediately due, and — only if the target was 'failed' — reactivates it
// and clears the failure counters so the manual attempt starts fresh. A
// 'paused' target keeps its status. Mirrors RotationRepo.PrepareRotateNow.
func (r *SyncTargetRepo) PrepareSyncNow(ctx context.Context, id string, now time.Time) error {
	return r.s.execAffectingOne(ctx,
		`UPDATE sync_targets SET
		   next_sync_at  = $2,
		   status        = CASE WHEN status = 'failed' THEN 'active' ELSE status END,
		   failure_count = CASE WHEN status = 'failed' THEN 0 ELSE failure_count END,
		   last_error    = CASE WHEN status = 'failed' THEN NULL ELSE last_error END,
		   updated_at    = now()
		 WHERE id = $1::uuid`, id, now)
}
