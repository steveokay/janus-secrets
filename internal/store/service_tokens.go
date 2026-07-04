package store

import (
	"context"
	"time"
)

// ServiceTokenRepo persists service tokens (HMACs only, never raw tokens).
type ServiceTokenRepo struct{ s *Store }

// NewServiceTokenRepo returns a service-token repository.
func NewServiceTokenRepo(s *Store) *ServiceTokenRepo { return &ServiceTokenRepo{s: s} }

// #nosec G101 -- this is a SQL column list, not a hardcoded credential.
const svcTokenCols = `id::text, name, token_hmac, created_by::text, scope_kind,
	scope_id::text, access, created_at, expires_at, revoked_at`

func scanServiceToken(row interface{ Scan(...any) error }) (*ServiceToken, error) {
	var t ServiceToken
	if err := row.Scan(&t.ID, &t.Name, &t.TokenHMAC, &t.CreatedBy, &t.ScopeKind,
		&t.ScopeID, &t.Access, &t.CreatedAt, &t.ExpiresAt, &t.RevokedAt); err != nil {
		return nil, mapError(err)
	}
	return &t, nil
}

// Create inserts a service token. expiresAt nil means long-lived.
func (r *ServiceTokenRepo) Create(ctx context.Context, name string, tokenHMAC []byte,
	createdBy, scopeKind, scopeID, access string, expiresAt *time.Time) (*ServiceToken, error) {
	row := r.s.pool.QueryRow(ctx,
		`INSERT INTO service_tokens (name, token_hmac, created_by, scope_kind, scope_id, access, expires_at)
		 VALUES ($1, $2, $3::uuid, $4, $5::uuid, $6, $7)
		 RETURNING `+svcTokenCols,
		name, tokenHMAC, createdBy, scopeKind, scopeID, access, expiresAt)
	return scanServiceToken(row)
}

// GetByHMAC returns the token whose stored HMAC matches. Revocation/expiry
// policy is the caller's concern.
func (r *ServiceTokenRepo) GetByHMAC(ctx context.Context, tokenHMAC []byte) (*ServiceToken, error) {
	row := r.s.pool.QueryRow(ctx,
		`SELECT `+svcTokenCols+` FROM service_tokens WHERE token_hmac = $1`, tokenHMAC)
	return scanServiceToken(row)
}

// List returns all tokens, newest first (metadata; HMACs are opaque bytes).
func (r *ServiceTokenRepo) List(ctx context.Context) ([]*ServiceToken, error) {
	rows, err := r.s.pool.Query(ctx,
		`SELECT `+svcTokenCols+` FROM service_tokens ORDER BY created_at DESC`)
	if err != nil {
		return nil, mapError(err)
	}
	defer rows.Close()
	var out []*ServiceToken
	for rows.Next() {
		t, err := scanServiceToken(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, mapError(rows.Err())
}

// Revoke marks a token revoked. Returns ErrNotFound if absent or already
// revoked.
func (r *ServiceTokenRepo) Revoke(ctx context.Context, id string) error {
	return r.s.execAffectingOne(ctx,
		`UPDATE service_tokens SET revoked_at = now()
		 WHERE id = $1::uuid AND revoked_at IS NULL`, id)
}
