package store

import (
	"context"
	"encoding/json"

	"github.com/jackc/pgx/v5"
)

// --- config (single row) ---

const fedConfigCols = `id::text, issuer, audience, enabled, created_at, updated_at`

// OIDCFederationConfigRepo persists the single trust-provider row used to
// verify federated CI identity tokens (e.g. GitHub Actions OIDC).
type OIDCFederationConfigRepo struct{ s *Store }

// NewOIDCFederationConfigRepo returns a federation-config repository.
func NewOIDCFederationConfigRepo(s *Store) *OIDCFederationConfigRepo {
	return &OIDCFederationConfigRepo{s: s}
}

// Put upserts the single federation config row (delete-then-insert keeps it to
// one row without needing a unique sentinel column).
func (r *OIDCFederationConfigRepo) Put(ctx context.Context, c OIDCFederationConfig) error {
	return r.s.withTx(ctx, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `DELETE FROM oidc_federation_config`); err != nil {
			return err
		}
		_, err := tx.Exec(ctx,
			`INSERT INTO oidc_federation_config (issuer, audience, enabled)
			 VALUES ($1, $2, $3)`, c.Issuer, c.Audience, c.Enabled)
		return err
	})
}

// Get returns the single configured federation config (LIMIT 1), or
// ErrNotFound.
func (r *OIDCFederationConfigRepo) Get(ctx context.Context) (*OIDCFederationConfig, error) {
	row := r.s.pool.QueryRow(ctx,
		`SELECT `+fedConfigCols+` FROM oidc_federation_config ORDER BY created_at LIMIT 1`)
	var c OIDCFederationConfig
	if err := row.Scan(&c.ID, &c.Issuer, &c.Audience, &c.Enabled, &c.CreatedAt, &c.UpdatedAt); err != nil {
		return nil, mapError(err)
	}
	return &c, nil
}

// Delete removes the federation config row, if any.
func (r *OIDCFederationConfigRepo) Delete(ctx context.Context) error {
	_, err := r.s.pool.Exec(ctx, `DELETE FROM oidc_federation_config`)
	return mapError(err)
}

// --- bindings ---

const fedBindingCols = `id::text, name, match_claims, scope_kind, scope_id::text,
	access, ttl_seconds, enabled, created_at, updated_at`

// OIDCFederationBindingRepo persists claim-match bindings that mint scoped,
// time-limited service tokens for federated CI identities.
type OIDCFederationBindingRepo struct{ s *Store }

// NewOIDCFederationBindingRepo returns a federation-binding repository.
func NewOIDCFederationBindingRepo(s *Store) *OIDCFederationBindingRepo {
	return &OIDCFederationBindingRepo{s: s}
}

// Create inserts a binding. Duplicate name → ErrAlreadyExists.
func (r *OIDCFederationBindingRepo) Create(ctx context.Context, b OIDCFederationBinding) (*OIDCFederationBinding, error) {
	claims, err := json.Marshal(b.MatchClaims)
	if err != nil {
		return nil, err
	}
	row := r.s.pool.QueryRow(ctx,
		`INSERT INTO oidc_federation_bindings
		   (name, match_claims, scope_kind, scope_id, access, ttl_seconds, enabled)
		 VALUES ($1, $2, $3, $4::uuid, $5, $6, $7)
		 RETURNING `+fedBindingCols,
		b.Name, claims, b.ScopeKind, b.ScopeID, b.Access, b.TTLSeconds, b.Enabled)
	return scanFedBinding(row)
}

// List returns all bindings, oldest first.
func (r *OIDCFederationBindingRepo) List(ctx context.Context) ([]OIDCFederationBinding, error) {
	rows, err := r.s.pool.Query(ctx,
		`SELECT `+fedBindingCols+` FROM oidc_federation_bindings ORDER BY created_at`)
	if err != nil {
		return nil, mapError(err)
	}
	defer rows.Close()
	var out []OIDCFederationBinding
	for rows.Next() {
		b, err := scanFedBinding(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *b)
	}
	return out, mapError(rows.Err())
}

// Delete removes a binding by id. Returns ErrNotFound if absent.
func (r *OIDCFederationBindingRepo) Delete(ctx context.Context, id string) error {
	return r.s.execAffectingOne(ctx,
		`DELETE FROM oidc_federation_bindings WHERE id = $1::uuid`, id)
}

func scanFedBinding(row interface{ Scan(...any) error }) (*OIDCFederationBinding, error) {
	var b OIDCFederationBinding
	var claims []byte
	if err := row.Scan(&b.ID, &b.Name, &claims, &b.ScopeKind, &b.ScopeID,
		&b.Access, &b.TTLSeconds, &b.Enabled, &b.CreatedAt, &b.UpdatedAt); err != nil {
		return nil, mapError(err)
	}
	if err := json.Unmarshal(claims, &b.MatchClaims); err != nil {
		return nil, err
	}
	return &b, nil
}
