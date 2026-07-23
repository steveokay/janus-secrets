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
	scope_id::text, access, created_at, expires_at, revoked_at, last_used_at,
	federation_binding::text, ip_allowlist`

func scanServiceToken(row interface{ Scan(...any) error }) (*ServiceToken, error) {
	var t ServiceToken
	// scope_id is nullable (transit tokens may target all keys); created_by is
	// nullable for federated tokens; federation_binding is nullable for
	// human-minted tokens; last_used_at is nullable (never authenticated);
	// ip_allowlist is nullable (empty allowlist = any IP) and scans into a slice.
	var scopeID, createdBy, fedBinding *string
	if err := row.Scan(&t.ID, &t.Name, &t.TokenHMAC, &createdBy, &t.ScopeKind,
		&scopeID, &t.Access, &t.CreatedAt, &t.ExpiresAt, &t.RevokedAt, &t.LastUsedAt,
		&fedBinding, &t.IPAllowlist); err != nil {
		return nil, mapError(err)
	}
	if scopeID != nil {
		t.ScopeID = *scopeID
	}
	if createdBy != nil {
		t.CreatedBy = *createdBy
	}
	if fedBinding != nil {
		t.FederationBinding = *fedBinding
	}
	return &t, nil
}

// Create inserts a service token. expiresAt nil means long-lived. An empty
// scopeID is persisted as SQL NULL (a transit token targeting all keys). An
// empty/nil ipAllowlist is persisted as SQL NULL (any IP).
func (r *ServiceTokenRepo) Create(ctx context.Context, name string, tokenHMAC []byte,
	createdBy, scopeKind, scopeID, access string, expiresAt *time.Time, ipAllowlist []string) (*ServiceToken, error) {
	var sid any = scopeID
	if scopeID == "" {
		sid = nil
	}
	var allow any = ipAllowlist
	if len(ipAllowlist) == 0 {
		allow = nil
	}
	// scope_id is text: config/environment scopes store a UUID (as text), a
	// transit scope stores the key NAME, and an all-keys transit token stores
	// NULL. No ::uuid cast — a transit key name is not a UUID.
	row := r.s.pool.QueryRow(ctx,
		`INSERT INTO service_tokens (name, token_hmac, created_by, scope_kind, scope_id, access, expires_at, ip_allowlist)
		 VALUES ($1, $2, $3::uuid, $4, $5, $6, $7, $8)
		 RETURNING `+svcTokenCols,
		name, tokenHMAC, createdBy, scopeKind, sid, access, expiresAt, allow)
	return scanServiceToken(row)
}

// CreateFederated inserts a service token minted by CI federation: created_by
// is NULL and federation_binding records the matched binding that minted it.
func (r *ServiceTokenRepo) CreateFederated(ctx context.Context, name string, tokenHMAC []byte,
	scopeKind, scopeID, access string, expiresAt *time.Time, bindingID string) (*ServiceToken, error) {
	var sid any = scopeID
	if scopeID == "" {
		sid = nil
	}
	row := r.s.pool.QueryRow(ctx,
		`INSERT INTO service_tokens
		   (name, token_hmac, created_by, scope_kind, scope_id, access, expires_at, federation_binding)
		 VALUES ($1, $2, NULL, $3, $4, $5, $6, $7::uuid)
		 RETURNING `+svcTokenCols,
		name, tokenHMAC, scopeKind, sid, access, expiresAt, bindingID)
	return scanServiceToken(row)
}

// GetByHMAC returns the token whose stored HMAC matches. Revocation/expiry
// policy is the caller's concern.
func (r *ServiceTokenRepo) GetByHMAC(ctx context.Context, tokenHMAC []byte) (*ServiceToken, error) {
	row := r.s.pool.QueryRow(ctx,
		`SELECT `+svcTokenCols+` FROM service_tokens WHERE token_hmac = $1`, tokenHMAC)
	return scanServiceToken(row)
}

// ListPage returns service tokens in (created_at DESC, id DESC) order.
// limit<=0 is unbounded; after==nil is the first page. There is no base WHERE
// filter (no soft-delete on service_tokens), so the keyset predicate opens with
// WHERE rather than AND.
func (r *ServiceTokenRepo) ListPage(ctx context.Context, limit int, after *Cursor) ([]*ServiceToken, error) {
	q := `SELECT ` + svcTokenCols + ` FROM service_tokens`
	var args []any
	if ks, ksArgs := keyset(after, len(args)+1); ks != "" {
		q += " WHERE " + ks
		args = append(args, ksArgs...)
	}
	q += " ORDER BY created_at DESC, id DESC"
	if ls, lArgs := limitSQL(limit, len(args)+1); ls != "" {
		q += ls
		args = append(args, lArgs...)
	}
	rows, err := r.s.pool.Query(ctx, q, args...)
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

// List returns all tokens, newest first (metadata; HMACs are opaque bytes).
func (r *ServiceTokenRepo) List(ctx context.Context) ([]*ServiceToken, error) {
	return r.ListPage(ctx, 0, nil)
}

// TouchLastUsed best-effort stamps last_used_at=now() for the token, THROTTLED:
// the write is a no-op when last_used_at was set within the throttle window, so
// hot tokens do not incur a write per request. The window is evaluated in SQL
// (now() - throttle) to avoid client clock skew. A no-op (row already fresh, or
// token gone) is NOT an error — callers treat any error as non-fatal. This never
// touches token values or the HMAC; it is a pure value-free timestamp update.
func (r *ServiceTokenRepo) TouchLastUsed(ctx context.Context, id string, throttle time.Duration) error {
	_, err := r.s.pool.Exec(ctx,
		`UPDATE service_tokens
		   SET last_used_at = now()
		 WHERE id = $1::uuid
		   AND (last_used_at IS NULL OR last_used_at < now() - ($2 * interval '1 microsecond'))`,
		id, throttle.Microseconds())
	return mapError(err)
}

// Revoke marks a token revoked. Returns ErrNotFound if absent or already
// revoked.
func (r *ServiceTokenRepo) Revoke(ctx context.Context, id string) error {
	return r.s.execAffectingOne(ctx,
		`UPDATE service_tokens SET revoked_at = now()
		 WHERE id = $1::uuid AND revoked_at IS NULL`, id)
}

// SetIPAllowlist replaces a token's IP allowlist. An empty/nil list clears it
// (persisted as NULL → any IP). Returns ErrNotFound if the token is absent.
// Value-free: touches only the CIDR list, never the token value or HMAC.
func (r *ServiceTokenRepo) SetIPAllowlist(ctx context.Context, id string, ipAllowlist []string) error {
	var allow any = ipAllowlist
	if len(ipAllowlist) == 0 {
		allow = nil
	}
	return r.s.execAffectingOne(ctx,
		`UPDATE service_tokens SET ip_allowlist = $2 WHERE id = $1::uuid`, id, allow)
}

// RecordSeenIP records that a token authenticated from ip. It returns isNew=true
// exactly when this (token, ip) pair had not been seen before (a fresh INSERT),
// which the caller uses to flag a new-IP anomaly. Because the PRIMARY KEY makes
// repeat pairs a no-op via ON CONFLICT DO NOTHING, writes are naturally
// throttled to genuinely new pairs. Value-free: stores only the IP + timestamp.
// The caller treats any error as non-fatal (must not fail the request).
func (r *ServiceTokenRepo) RecordSeenIP(ctx context.Context, tokenID, ip string) (bool, error) {
	tag, err := r.s.pool.Exec(ctx,
		`INSERT INTO token_seen_ips (token_id, ip) VALUES ($1::uuid, $2)
		 ON CONFLICT (token_id, ip) DO NOTHING`, tokenID, ip)
	if err != nil {
		return false, mapError(err)
	}
	return tag.RowsAffected() == 1, nil
}

// CountRecentNewIPs counts distinct (token, ip) first-sightings whose
// first_seen_at is within the given window (now() - within). It is the cheap
// aggregate behind the Overview "token used from a new IP" in-tray item.
// Value-free: counts only, no IPs or secret material returned.
func (r *ServiceTokenRepo) CountRecentNewIPs(ctx context.Context, within time.Duration) (int, error) {
	var n int
	err := r.s.pool.QueryRow(ctx,
		`SELECT count(*) FROM token_seen_ips
		 WHERE first_seen_at >= now() - ($1 * interval '1 microsecond')`,
		within.Microseconds()).Scan(&n)
	return n, mapError(err)
}
