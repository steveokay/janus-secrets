package store

import (
	"context"
	"encoding/json"

	"github.com/jackc/pgx/v5"
)

// --- trusted issuers (one row per issuer) ---

const fedConfigCols = `id::text, issuer, audience, preset, enabled, created_at, updated_at`

// OIDCFederationConfigRepo persists the set of trusted federation issuers used
// to verify federated machine identity tokens (CI providers such as GitHub
// Actions, and Kubernetes cluster service-account issuers).
type OIDCFederationConfigRepo struct{ s *Store }

// NewOIDCFederationConfigRepo returns a federation-config repository.
func NewOIDCFederationConfigRepo(s *Store) *OIDCFederationConfigRepo {
	return &OIDCFederationConfigRepo{s: s}
}

// Put replaces the whole trusted-issuer set with this one issuer
// (delete-then-insert). It backs the legacy single-issuer admin endpoint; the
// multi-issuer path uses Upsert.
func (r *OIDCFederationConfigRepo) Put(ctx context.Context, c OIDCFederationConfig) error {
	return r.s.withTx(ctx, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `DELETE FROM oidc_federation_config`); err != nil {
			return err
		}
		_, err := tx.Exec(ctx,
			`INSERT INTO oidc_federation_config (issuer, audience, preset, enabled)
			 VALUES ($1, $2, $3, $4)`, c.Issuer, c.Audience, c.Preset, c.Enabled)
		return err
	})
}

// Upsert inserts a trusted issuer, or updates the audience/preset/enabled flag
// of the existing row with the same issuer. Other issuers are left alone.
func (r *OIDCFederationConfigRepo) Upsert(ctx context.Context, c OIDCFederationConfig) (*OIDCFederationConfig, error) {
	row := r.s.pool.QueryRow(ctx,
		`INSERT INTO oidc_federation_config (issuer, audience, preset, enabled)
		 VALUES ($1, $2, $3, $4)
		 ON CONFLICT (issuer) DO UPDATE
		   SET audience = EXCLUDED.audience, preset = EXCLUDED.preset,
		       enabled = EXCLUDED.enabled, updated_at = now()
		 RETURNING `+fedConfigCols,
		c.Issuer, c.Audience, c.Preset, c.Enabled)
	return scanFedConfig(row)
}

// Get returns the oldest trusted issuer (LIMIT 1), or ErrNotFound. It backs the
// legacy single-issuer read; the multi-issuer path uses List.
func (r *OIDCFederationConfigRepo) Get(ctx context.Context) (*OIDCFederationConfig, error) {
	row := r.s.pool.QueryRow(ctx,
		`SELECT `+fedConfigCols+` FROM oidc_federation_config ORDER BY created_at LIMIT 1`)
	return scanFedConfig(row)
}

// List returns every trusted issuer, oldest first.
func (r *OIDCFederationConfigRepo) List(ctx context.Context) ([]OIDCFederationConfig, error) {
	rows, err := r.s.pool.Query(ctx,
		`SELECT `+fedConfigCols+` FROM oidc_federation_config ORDER BY created_at, id`)
	if err != nil {
		return nil, mapError(err)
	}
	defer rows.Close()
	var out []OIDCFederationConfig
	for rows.Next() {
		c, err := scanFedConfig(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *c)
	}
	return out, mapError(rows.Err())
}

// Delete removes every trusted issuer.
func (r *OIDCFederationConfigRepo) Delete(ctx context.Context) error {
	_, err := r.s.pool.Exec(ctx, `DELETE FROM oidc_federation_config`)
	return mapError(err)
}

// DeleteByID removes one trusted issuer. ErrNotFound if absent.
func (r *OIDCFederationConfigRepo) DeleteByID(ctx context.Context, id string) error {
	return r.s.execAffectingOne(ctx,
		`DELETE FROM oidc_federation_config WHERE id = $1::uuid`, id)
}

func scanFedConfig(row interface{ Scan(...any) error }) (*OIDCFederationConfig, error) {
	var c OIDCFederationConfig
	if err := row.Scan(&c.ID, &c.Issuer, &c.Audience, &c.Preset, &c.Enabled,
		&c.CreatedAt, &c.UpdatedAt); err != nil {
		return nil, mapError(err)
	}
	return &c, nil
}

// --- bindings ---

const fedBindingCols = `id::text, name, issuer, match_claims, scope_kind, scope_id::text,
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
		   (name, issuer, match_claims, scope_kind, scope_id, access, ttl_seconds, enabled)
		 VALUES ($1, $2, $3, $4, $5::uuid, $6, $7, $8)
		 RETURNING `+fedBindingCols,
		b.Name, b.Issuer, claims, b.ScopeKind, b.ScopeID, b.Access, b.TTLSeconds, b.Enabled)
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
	if err := row.Scan(&b.ID, &b.Name, &b.Issuer, &claims, &b.ScopeKind, &b.ScopeID,
		&b.Access, &b.TTLSeconds, &b.Enabled, &b.CreatedAt, &b.UpdatedAt); err != nil {
		return nil, mapError(err)
	}
	if err := json.Unmarshal(claims, &b.MatchClaims); err != nil {
		return nil, err
	}
	return &b, nil
}
