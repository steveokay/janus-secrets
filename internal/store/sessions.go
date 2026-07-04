package store

import (
	"context"
	"time"
)

// SessionRepo persists UI sessions (HMACs only, never raw cookie values).
type SessionRepo struct{ s *Store }

// NewSessionRepo returns a session repository.
func NewSessionRepo(s *Store) *SessionRepo { return &SessionRepo{s: s} }

const sessionCols = `id::text, user_id::text, token_hmac, created_at, expires_at, last_seen_at`

func scanSession(row interface{ Scan(...any) error }) (*Session, error) {
	var sess Session
	if err := row.Scan(&sess.ID, &sess.UserID, &sess.TokenHMAC,
		&sess.CreatedAt, &sess.ExpiresAt, &sess.LastSeenAt); err != nil {
		return nil, mapError(err)
	}
	return &sess, nil
}

// Create inserts a session.
func (r *SessionRepo) Create(ctx context.Context, userID string, tokenHMAC []byte, expiresAt time.Time) (*Session, error) {
	row := r.s.pool.QueryRow(ctx,
		`INSERT INTO sessions (user_id, token_hmac, expires_at)
		 VALUES ($1::uuid, $2, $3) RETURNING `+sessionCols,
		userID, tokenHMAC, expiresAt)
	return scanSession(row)
}

// GetByHMAC returns the session whose stored HMAC matches. Expiry is the
// caller's concern (the store stays policy-free).
func (r *SessionRepo) GetByHMAC(ctx context.Context, tokenHMAC []byte) (*Session, error) {
	row := r.s.pool.QueryRow(ctx,
		`SELECT `+sessionCols+` FROM sessions WHERE token_hmac = $1`, tokenHMAC)
	return scanSession(row)
}

// TouchLastSeen bumps last_seen_at.
func (r *SessionRepo) TouchLastSeen(ctx context.Context, id string) error {
	return r.s.execAffectingOne(ctx,
		`UPDATE sessions SET last_seen_at = now() WHERE id = $1::uuid`, id)
}

// DeleteByHMAC removes one session (logout).
func (r *SessionRepo) DeleteByHMAC(ctx context.Context, tokenHMAC []byte) error {
	return r.s.execAffectingOne(ctx,
		`DELETE FROM sessions WHERE token_hmac = $1`, tokenHMAC)
}

// Delete removes one session by id (expiry cleanup on lookup).
func (r *SessionRepo) Delete(ctx context.Context, id string) error {
	return r.s.execAffectingOne(ctx, `DELETE FROM sessions WHERE id = $1::uuid`, id)
}

// DeleteExpired sweeps all expired sessions (called at boot).
func (r *SessionRepo) DeleteExpired(ctx context.Context) error {
	_, err := r.s.pool.Exec(ctx, `DELETE FROM sessions WHERE expires_at < now()`)
	return mapError(err)
}
