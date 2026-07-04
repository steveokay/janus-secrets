package store

import "context"

// AuthConfigRepo persists the single wrapped token-HMAC key row.
type AuthConfigRepo struct{ s *Store }

// NewAuthConfigRepo returns the auth-config repository.
func NewAuthConfigRepo(s *Store) *AuthConfigRepo { return &AuthConfigRepo{s: s} }

// GetWrappedHMACKey returns the wrapped key, or ErrNotFound before bootstrap.
func (r *AuthConfigRepo) GetWrappedHMACKey(ctx context.Context) ([]byte, error) {
	var wrapped []byte
	err := r.s.pool.QueryRow(ctx,
		`SELECT wrapped_token_hmac_key FROM auth_config WHERE id = 1`).Scan(&wrapped)
	if err != nil {
		return nil, mapError(err)
	}
	return wrapped, nil
}

// PutWrappedHMACKeyIfAbsent stores the wrapped key once; concurrent racers
// converge on the first writer's key (ON CONFLICT DO NOTHING).
func (r *AuthConfigRepo) PutWrappedHMACKeyIfAbsent(ctx context.Context, wrapped []byte) error {
	_, err := r.s.pool.Exec(ctx,
		`INSERT INTO auth_config (id, wrapped_token_hmac_key) VALUES (1, $1)
		 ON CONFLICT (id) DO NOTHING`, wrapped)
	return mapError(err)
}
