package store

import (
	"context"

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
