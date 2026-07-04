package store

import "context"

// UserRepo persists users. The store is secret-blind: it stores PHC hash
// strings, never raw passwords.
type UserRepo struct{ s *Store }

// NewUserRepo returns a user repository.
func NewUserRepo(s *Store) *UserRepo { return &UserRepo{s: s} }

const userCols = `id::text, email, password_hash, created_at, updated_at, disabled_at`

func scanUser(row interface{ Scan(...any) error }) (*User, error) {
	var u User
	if err := row.Scan(&u.ID, &u.Email, &u.PasswordHash,
		&u.CreatedAt, &u.UpdatedAt, &u.DisabledAt); err != nil {
		return nil, mapError(err)
	}
	return &u, nil
}

// Create inserts a user. passwordHash may be nil (federated identities).
func (r *UserRepo) Create(ctx context.Context, email string, passwordHash *string) (*User, error) {
	row := r.s.pool.QueryRow(ctx,
		`INSERT INTO users (email, password_hash) VALUES ($1, $2)
		 RETURNING `+userCols, email, passwordHash)
	return scanUser(row)
}

// Get returns a user by id.
func (r *UserRepo) Get(ctx context.Context, id string) (*User, error) {
	row := r.s.pool.QueryRow(ctx,
		`SELECT `+userCols+` FROM users WHERE id = $1::uuid`, id)
	return scanUser(row)
}

// GetByEmail returns a user by email, case-insensitively.
func (r *UserRepo) GetByEmail(ctx context.Context, email string) (*User, error) {
	row := r.s.pool.QueryRow(ctx,
		`SELECT `+userCols+` FROM users WHERE lower(email) = lower($1)`, email)
	return scanUser(row)
}

// UpdatePassword replaces the stored PHC hash.
func (r *UserRepo) UpdatePassword(ctx context.Context, id, passwordHash string) error {
	return r.s.execAffectingOne(ctx,
		`UPDATE users SET password_hash = $2, updated_at = now()
		 WHERE id = $1::uuid`, id, passwordHash)
}

// Count returns the number of users (bootstrap idempotency check).
func (r *UserRepo) Count(ctx context.Context) (int, error) {
	var n int
	err := r.s.pool.QueryRow(ctx, `SELECT count(*) FROM users`).Scan(&n)
	return n, mapError(err)
}
