package store

import (
	"context"
	"time"
)

// --- Roles ---

type DynamicRole struct {
	ID                  string
	ProjectID           string
	ConfigID            string
	Name                string
	DefaultTTLSeconds   int64
	MaxTTLSeconds       int64
	ConfigCT            []byte
	ConfigNonce         []byte
	ConfigWrappedDEK    []byte
	ConfigDEKKEKVersion int
	CreatedBy           string
	CreatedAt           time.Time
	UpdatedAt           time.Time
}

type DynamicRoleRepo struct{ s *Store }

func NewDynamicRoleRepo(s *Store) *DynamicRoleRepo { return &DynamicRoleRepo{s: s} }

const dynamicRoleCols = `id::text, project_id::text, config_id::text, name,
	default_ttl_seconds, max_ttl_seconds, config_ct, config_nonce, config_wrapped_dek,
	config_dek_kek_version, created_by, created_at, updated_at`

func scanRole(row interface{ Scan(...any) error }) (*DynamicRole, error) {
	var r DynamicRole
	if err := row.Scan(&r.ID, &r.ProjectID, &r.ConfigID, &r.Name,
		&r.DefaultTTLSeconds, &r.MaxTTLSeconds, &r.ConfigCT, &r.ConfigNonce, &r.ConfigWrappedDEK,
		&r.ConfigDEKKEKVersion, &r.CreatedBy, &r.CreatedAt, &r.UpdatedAt); err != nil {
		return nil, mapError(err)
	}
	return &r, nil
}

// Create inserts a role. Duplicate (config_id, name) -> ErrAlreadyExists.
func (repo *DynamicRoleRepo) Create(ctx context.Context, r *DynamicRole) (*DynamicRole, error) {
	_, err := repo.s.pool.Exec(ctx,
		`INSERT INTO dynamic_roles
		 (id, project_id, config_id, name, default_ttl_seconds, max_ttl_seconds,
		  config_ct, config_nonce, config_wrapped_dek, config_dek_kek_version, created_by)
		 VALUES ($1::uuid,$2::uuid,$3::uuid,$4,$5,$6,$7,$8,$9,$10,$11)`,
		r.ID, r.ProjectID, r.ConfigID, r.Name, r.DefaultTTLSeconds, r.MaxTTLSeconds,
		r.ConfigCT, r.ConfigNonce, r.ConfigWrappedDEK, r.ConfigDEKKEKVersion, r.CreatedBy)
	if err != nil {
		return nil, mapError(err)
	}
	return repo.Get(ctx, r.ID)
}

func (repo *DynamicRoleRepo) Get(ctx context.Context, id string) (*DynamicRole, error) {
	return scanRole(repo.s.pool.QueryRow(ctx,
		`SELECT `+dynamicRoleCols+` FROM dynamic_roles WHERE id = $1::uuid`, id))
}

func (repo *DynamicRoleRepo) ListByConfig(ctx context.Context, configID string) ([]*DynamicRole, error) {
	rows, err := repo.s.pool.Query(ctx,
		`SELECT `+dynamicRoleCols+` FROM dynamic_roles WHERE config_id = $1::uuid ORDER BY created_at DESC, id`,
		configID)
	if err != nil {
		return nil, mapError(err)
	}
	defer rows.Close()
	var out []*DynamicRole
	for rows.Next() {
		r, err := scanRole(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, mapError(rows.Err())
}

// Update sets TTLs and (optionally) a new encrypted config blob. nil leaves a
// field unchanged.
func (repo *DynamicRoleRepo) Update(ctx context.Context, id string, defaultTTL, maxTTL *int64,
	configCT, configNonce, configWrappedDEK []byte, configKEKVer *int) error {
	return repo.s.execAffectingOne(ctx,
		`UPDATE dynamic_roles SET
		   default_ttl_seconds    = COALESCE($2, default_ttl_seconds),
		   max_ttl_seconds        = COALESCE($3, max_ttl_seconds),
		   config_ct              = COALESCE($4, config_ct),
		   config_nonce           = COALESCE($5, config_nonce),
		   config_wrapped_dek     = COALESCE($6, config_wrapped_dek),
		   config_dek_kek_version = COALESCE($7, config_dek_kek_version),
		   updated_at             = now()
		 WHERE id = $1::uuid`,
		id, defaultTTL, maxTTL, configCT, configNonce, configWrappedDEK, configKEKVer)
}

func (repo *DynamicRoleRepo) Delete(ctx context.Context, id string) error {
	return repo.s.execAffectingOne(ctx, `DELETE FROM dynamic_roles WHERE id = $1::uuid`, id)
}

// --- Leases ---

type DynamicLease struct {
	ID           string
	RoleID       string
	ProjectID    string
	DBUsername   string
	Status       string
	IssuedAt     time.Time
	ExpiresAt    time.Time
	MaxExpiresAt time.Time
	RenewedAt    *time.Time
	RevokedAt    *time.Time
	LastError    *string
	CreatedBy    string
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

type DynamicLeaseRepo struct{ s *Store }

func NewDynamicLeaseRepo(s *Store) *DynamicLeaseRepo { return &DynamicLeaseRepo{s: s} }

const dynamicLeaseCols = `id::text, role_id::text, project_id::text, db_username, status,
	issued_at, expires_at, max_expires_at, renewed_at, revoked_at, last_error,
	created_by, created_at, updated_at`

func scanLease(row interface{ Scan(...any) error }) (*DynamicLease, error) {
	var l DynamicLease
	if err := row.Scan(&l.ID, &l.RoleID, &l.ProjectID, &l.DBUsername, &l.Status,
		&l.IssuedAt, &l.ExpiresAt, &l.MaxExpiresAt, &l.RenewedAt, &l.RevokedAt, &l.LastError,
		&l.CreatedBy, &l.CreatedAt, &l.UpdatedAt); err != nil {
		return nil, mapError(err)
	}
	return &l, nil
}

// Create inserts a lease in status 'creating'.
func (repo *DynamicLeaseRepo) Create(ctx context.Context, l *DynamicLease) error {
	_, err := repo.s.pool.Exec(ctx,
		`INSERT INTO dynamic_leases
		 (id, role_id, project_id, db_username, expires_at, max_expires_at, created_by)
		 VALUES ($1::uuid,$2::uuid,$3::uuid,$4,$5,$6,$7)`,
		l.ID, l.RoleID, l.ProjectID, l.DBUsername, l.ExpiresAt, l.MaxExpiresAt, l.CreatedBy)
	return mapError(err)
}

func (repo *DynamicLeaseRepo) Get(ctx context.Context, id string) (*DynamicLease, error) {
	return scanLease(repo.s.pool.QueryRow(ctx,
		`SELECT `+dynamicLeaseCols+` FROM dynamic_leases WHERE id = $1::uuid`, id))
}

func (repo *DynamicLeaseRepo) ListByRole(ctx context.Context, roleID string) ([]*DynamicLease, error) {
	rows, err := repo.s.pool.Query(ctx,
		`SELECT `+dynamicLeaseCols+` FROM dynamic_leases WHERE role_id = $1::uuid ORDER BY created_at DESC, id`,
		roleID)
	if err != nil {
		return nil, mapError(err)
	}
	defer rows.Close()
	var out []*DynamicLease
	for rows.Next() {
		l, err := scanLease(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, mapError(rows.Err())
}

// Activate flips a 'creating' lease to 'active'.
func (repo *DynamicLeaseRepo) Activate(ctx context.Context, id string) error {
	return repo.s.execAffectingOne(ctx,
		`UPDATE dynamic_leases SET status='active', updated_at=now()
		 WHERE id=$1::uuid AND status='creating'`, id)
}

// ClaimDue returns leases the lease-manager must act on: active leases past
// expiry, revoke retries, and crash-orphaned 'creating' rows older than the
// grace window (a running IssueCreds activates within milliseconds, so grace
// prevents revoking an in-flight lease). Single-node = one scheduler goroutine,
// so a plain SELECT is race-free; no FOR UPDATE because revocation performs
// network I/O.
func (repo *DynamicLeaseRepo) ClaimDue(ctx context.Context, now, creatingBefore time.Time, limit int) ([]*DynamicLease, error) {
	rows, err := repo.s.pool.Query(ctx,
		`SELECT `+dynamicLeaseCols+` FROM dynamic_leases
		 WHERE (status='active' AND expires_at <= $1)
		    OR status='revoke_failed'
		    OR (status='creating' AND created_at <= $2)
		 ORDER BY expires_at ASC LIMIT $3`, now, creatingBefore, limit)
	if err != nil {
		return nil, mapError(err)
	}
	defer rows.Close()
	var out []*DynamicLease
	for rows.Next() {
		l, err := scanLease(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, mapError(rows.Err())
}

// MarkRevoked records a successful revocation with the given terminal status
// ('revoked' or 'expired').
func (repo *DynamicLeaseRepo) MarkRevoked(ctx context.Context, id, status string, now time.Time) error {
	return repo.s.execAffectingOne(ctx,
		`UPDATE dynamic_leases SET status=$2, revoked_at=$3, last_error=NULL, updated_at=now()
		 WHERE id=$1::uuid`, id, status, now)
}

// MarkRevokeFailed records a failed revocation for scheduler retry.
func (repo *DynamicLeaseRepo) MarkRevokeFailed(ctx context.Context, id, sanitizedErr string) error {
	return repo.s.execAffectingOne(ctx,
		`UPDATE dynamic_leases SET status='revoke_failed', last_error=$2, updated_at=now()
		 WHERE id=$1::uuid`, id, sanitizedErr)
}

// Renew advances an active lease's expiry.
func (repo *DynamicLeaseRepo) Renew(ctx context.Context, id string, newExpiry, now time.Time) error {
	return repo.s.execAffectingOne(ctx,
		`UPDATE dynamic_leases SET expires_at=$2, renewed_at=$3, updated_at=now()
		 WHERE id=$1::uuid AND status='active'`, id, newExpiry, now)
}
