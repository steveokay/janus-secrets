package store

import (
	"context"
	"time"
)

// UserRepo persists users. The store is secret-blind: it stores PHC hash
// strings, never raw passwords.
type UserRepo struct{ s *Store }

// NewUserRepo returns a user repository.
func NewUserRepo(s *Store) *UserRepo { return &UserRepo{s: s} }

const userCols = `id::text, email, password_hash, created_at, updated_at, disabled_at, ` +
	`failed_login_count, lockout_level, locked_until, last_failed_login_at`

func scanUser(row interface{ Scan(...any) error }) (*User, error) {
	var u User
	if err := row.Scan(&u.ID, &u.Email, &u.PasswordHash,
		&u.CreatedAt, &u.UpdatedAt, &u.DisabledAt,
		&u.FailedLoginCount, &u.LockoutLevel, &u.LockedUntil, &u.LastFailedLoginAt); err != nil {
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

// SetDisabled sets or clears the disabled_at timestamp.
func (r *UserRepo) SetDisabled(ctx context.Context, id string, disabled bool) error {
	if disabled {
		return r.s.execAffectingOne(ctx,
			`UPDATE users SET disabled_at = now(), updated_at = now() WHERE id = $1::uuid`, id)
	}
	return r.s.execAffectingOne(ctx,
		`UPDATE users SET disabled_at = NULL, updated_at = now() WHERE id = $1::uuid`, id)
}

// RecordFailedLogin records one counted failed login for the user in a single
// atomic UPDATE. It increments failed_login_count and stamps
// last_failed_login_at=now(). When the incremented count reaches threshold, the
// account is locked: lockout_level is bumped, locked_until is set to
// now()+window (window is the caller-computed duration for the NEW level), and
// failed_login_count is reset to 0 so the next cycle starts clean. Otherwise the
// count simply accrues.
//
// The update is expressed in terms of the row's own column values
// (failed_login_count + 1), so concurrent callers converge without lost updates.
// now() is evaluated in SQL to avoid client clock skew. Returns whether this
// call tripped a lock and, if so, the resulting locked_until.
//
// The caller (auth.Service) owns the lockout policy; the store stays policy-light,
// receiving only the threshold and the pre-computed window for the new level.
func (r *UserRepo) RecordFailedLogin(ctx context.Context, id string, threshold int, window time.Duration) (locked bool, lockedUntil *time.Time, err error) {
	row := r.s.pool.QueryRow(ctx, `
		UPDATE users SET
			last_failed_login_at = now(),
			failed_login_count = CASE WHEN failed_login_count + 1 >= $2 THEN 0 ELSE failed_login_count + 1 END,
			lockout_level      = CASE WHEN failed_login_count + 1 >= $2 THEN lockout_level + 1 ELSE lockout_level END,
			locked_until       = CASE WHEN failed_login_count + 1 >= $2 THEN now() + ($3 * interval '1 microsecond') ELSE locked_until END,
			updated_at         = now()
		WHERE id = $1::uuid
		RETURNING (failed_login_count = 0 AND lockout_level > 0 AND locked_until IS NOT NULL AND locked_until > now()) AS just_locked, locked_until`,
		id, threshold, window.Microseconds())
	var justLocked bool
	var lu *time.Time
	if scanErr := row.Scan(&justLocked, &lu); scanErr != nil {
		return false, nil, mapError(scanErr)
	}
	if justLocked {
		return true, lu, nil
	}
	return false, nil, nil
}

// ResetLoginFailures clears the failure counter, escalation level, and lock —
// called on a fully successful login. Idempotent; a missing row is a no-op error.
func (r *UserRepo) ResetLoginFailures(ctx context.Context, id string) error {
	return r.s.execAffectingOne(ctx,
		`UPDATE users SET failed_login_count = 0, lockout_level = 0, locked_until = NULL, updated_at = now()
		 WHERE id = $1::uuid`, id)
}

// AdminUnlock clears the lock state for an operator-initiated early unlock. It is
// the same state reset as ResetLoginFailures but is a distinct method so the API
// layer can audit it as a separate action (user.unlock vs. a login success).
func (r *UserRepo) AdminUnlock(ctx context.Context, id string) error {
	return r.ResetLoginFailures(ctx, id)
}

// ListPage returns users in (created_at DESC, id DESC) order. limit<=0 is
// unbounded; after==nil is the first page. There is no base WHERE filter (no
// soft-delete on users), so the keyset predicate opens with WHERE rather than
// AND.
func (r *UserRepo) ListPage(ctx context.Context, limit int, after *Cursor) ([]*User, error) {
	q := `SELECT ` + userCols + ` FROM users`
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
	var out []*User
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, mapError(rows.Err())
}

// List returns all users, newest first (unbounded; kept for existing callers).
func (r *UserRepo) List(ctx context.Context) ([]*User, error) {
	return r.ListPage(ctx, 0, nil)
}

// Oldest returns the earliest-created user (bootstrap reconciliation).
func (r *UserRepo) Oldest(ctx context.Context) (*User, error) {
	row := r.s.pool.QueryRow(ctx,
		`SELECT `+userCols+` FROM users ORDER BY created_at ASC LIMIT 1`)
	return scanUser(row)
}
