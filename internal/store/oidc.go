package store

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
)

const oidcProviderCols = `id::text, name, issuer, client_id, wrapped_client_secret,
	scopes, redirect_url, enabled, created_at, updated_at`

// OIDCProviderRepo persists the (single) configured OIDC provider. It is
// crypto-blind: wrapped_client_secret is stored and returned as opaque bytes.
type OIDCProviderRepo struct{ s *Store }

// NewOIDCProviderRepo returns an OIDC provider repository.
func NewOIDCProviderRepo(s *Store) *OIDCProviderRepo { return &OIDCProviderRepo{s: s} }

// Put upserts the provider keyed by name.
func (r *OIDCProviderRepo) Put(ctx context.Context, p OIDCProvider) error {
	_, err := r.s.pool.Exec(ctx,
		`INSERT INTO oidc_providers
		   (name, issuer, client_id, wrapped_client_secret, scopes, redirect_url, enabled, updated_at)
		 VALUES ($1,$2,$3,$4,$5,$6,$7, now())
		 ON CONFLICT (name) DO UPDATE SET
		   issuer=$2, client_id=$3, wrapped_client_secret=$4, scopes=$5,
		   redirect_url=$6, enabled=$7, updated_at=now()`,
		p.Name, p.Issuer, p.ClientID, p.WrappedClientSecret, p.Scopes, p.RedirectURL, p.Enabled)
	return mapError(err)
}

// Get returns the single configured provider (LIMIT 1), or ErrNotFound.
func (r *OIDCProviderRepo) Get(ctx context.Context) (*OIDCProvider, error) {
	row := r.s.pool.QueryRow(ctx,
		`SELECT `+oidcProviderCols+` FROM oidc_providers ORDER BY created_at LIMIT 1`)
	return scanOIDCProvider(row)
}

// Delete removes all provider rows.
func (r *OIDCProviderRepo) Delete(ctx context.Context) error {
	_, err := r.s.pool.Exec(ctx, `DELETE FROM oidc_providers`)
	return mapError(err)
}

func scanOIDCProvider(row pgx.Row) (*OIDCProvider, error) {
	var p OIDCProvider
	if err := row.Scan(&p.ID, &p.Name, &p.Issuer, &p.ClientID, &p.WrappedClientSecret,
		&p.Scopes, &p.RedirectURL, &p.Enabled, &p.CreatedAt, &p.UpdatedAt); err != nil {
		return nil, mapError(err)
	}
	return &p, nil
}

const oidcIdentityCols = `id::text, user_id::text, issuer, subject, created_at, last_login_at`

// OIDCIdentityRepo links provider subjects to Janus users.
type OIDCIdentityRepo struct{ s *Store }

func NewOIDCIdentityRepo(s *Store) *OIDCIdentityRepo { return &OIDCIdentityRepo{s: s} }

// GetBySubject returns the identity for (issuer, subject), or ErrNotFound.
func (r *OIDCIdentityRepo) GetBySubject(ctx context.Context, issuer, subject string) (*OIDCIdentity, error) {
	row := r.s.pool.QueryRow(ctx,
		`SELECT `+oidcIdentityCols+` FROM oidc_identities WHERE issuer=$1 AND subject=$2`, issuer, subject)
	return scanOIDCIdentity(row)
}

// Create links a subject to a user. Duplicate (issuer, subject) → ErrAlreadyExists.
func (r *OIDCIdentityRepo) Create(ctx context.Context, userID, issuer, subject string) (*OIDCIdentity, error) {
	row := r.s.pool.QueryRow(ctx,
		`INSERT INTO oidc_identities (user_id, issuer, subject)
		 VALUES ($1::uuid, $2, $3) RETURNING `+oidcIdentityCols,
		userID, issuer, subject)
	return scanOIDCIdentity(row)
}

// TouchLastLogin bumps last_login_at.
func (r *OIDCIdentityRepo) TouchLastLogin(ctx context.Context, id string) error {
	_, err := r.s.pool.Exec(ctx, `UPDATE oidc_identities SET last_login_at=now() WHERE id=$1::uuid`, id)
	return mapError(err)
}

func scanOIDCIdentity(row pgx.Row) (*OIDCIdentity, error) {
	var i OIDCIdentity
	if err := row.Scan(&i.ID, &i.UserID, &i.Issuer, &i.Subject, &i.CreatedAt, &i.LastLoginAt); err != nil {
		return nil, mapError(err)
	}
	return &i, nil
}

// OIDCAuthRequestRepo persists single-use login state.
type OIDCAuthRequestRepo struct{ s *Store }

func NewOIDCAuthRequestRepo(s *Store) *OIDCAuthRequestRepo { return &OIDCAuthRequestRepo{s: s} }

// Create inserts a login-state row.
func (r *OIDCAuthRequestRepo) Create(ctx context.Context, state, nonce, verifier, providerID string, expiresAt time.Time) error {
	_, err := r.s.pool.Exec(ctx,
		`INSERT INTO oidc_auth_requests (state, nonce, pkce_verifier, provider_id, expires_at)
		 VALUES ($1,$2,$3,$4::uuid,$5)`,
		state, nonce, verifier, providerID, expiresAt)
	return mapError(err)
}

// Consume atomically returns and deletes the row for state, but only if it has
// not expired. Missing or expired → ErrNotFound (single-use, replay-safe).
func (r *OIDCAuthRequestRepo) Consume(ctx context.Context, state string) (*OIDCAuthRequest, error) {
	var a OIDCAuthRequest
	err := r.s.withTx(ctx, func(tx pgx.Tx) error {
		row := tx.QueryRow(ctx,
			`DELETE FROM oidc_auth_requests
			 WHERE state=$1 AND expires_at > now()
			 RETURNING state, nonce, pkce_verifier, provider_id::text, created_at, expires_at`, state)
		return row.Scan(&a.State, &a.Nonce, &a.PKCEVerifier, &a.ProviderID, &a.CreatedAt, &a.ExpiresAt)
	})
	if err != nil {
		return nil, mapError(err)
	}
	return &a, nil
}

// DeleteExpired removes stale rows (called at boot).
func (r *OIDCAuthRequestRepo) DeleteExpired(ctx context.Context) error {
	_, err := r.s.pool.Exec(ctx, `DELETE FROM oidc_auth_requests WHERE expires_at <= now()`)
	return mapError(err)
}
