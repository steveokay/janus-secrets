package store

import (
	"context"
	"time"
)

// SessionRepo persists UI sessions (HMACs only, never raw cookie values).
type SessionRepo struct{ s *Store }

// NewSessionRepo returns a session repository.
func NewSessionRepo(s *Store) *SessionRepo { return &SessionRepo{s: s} }

const sessionCols = `id::text, user_id::text, token_hmac, created_at, expires_at, last_seen_at, ip, user_agent`

func scanSession(row interface{ Scan(...any) error }) (*Session, error) {
	var sess Session
	if err := row.Scan(&sess.ID, &sess.UserID, &sess.TokenHMAC,
		&sess.CreatedAt, &sess.ExpiresAt, &sess.LastSeenAt, &sess.IP, &sess.UserAgent); err != nil {
		return nil, mapError(err)
	}
	return &sess, nil
}

// Create inserts a session. ip and userAgent are non-secret client metadata;
// empty strings persist as NULL.
func (r *SessionRepo) Create(ctx context.Context, userID string, tokenHMAC []byte, expiresAt time.Time, ip, userAgent string) (*Session, error) {
	row := r.s.pool.QueryRow(ctx,
		`INSERT INTO sessions (user_id, token_hmac, expires_at, ip, user_agent)
		 VALUES ($1::uuid, $2, $3, NULLIF($4,''), NULLIF($5,'')) RETURNING `+sessionCols,
		userID, tokenHMAC, expiresAt, ip, userAgent)
	return scanSession(row)
}

// ListByUser returns a user's non-expired sessions, most recent first. Used by
// the self-service session-management surface.
func (r *SessionRepo) ListByUser(ctx context.Context, userID string) ([]*Session, error) {
	rows, err := r.s.pool.Query(ctx,
		`SELECT `+sessionCols+` FROM sessions
		 WHERE user_id = $1::uuid AND expires_at > now()
		 ORDER BY created_at DESC`, userID)
	if err != nil {
		return nil, mapError(err)
	}
	defer rows.Close()
	var out []*Session
	for rows.Next() {
		sess, err := scanSession(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, sess)
	}
	return out, mapError(rows.Err())
}

// DeleteForUser removes one session, but only if it belongs to userID (so a
// caller can never revoke another user's session by guessing an id). Returns
// ErrNotFound when no such owned session exists.
func (r *SessionRepo) DeleteForUser(ctx context.Context, id, userID string) error {
	return r.s.execAffectingOne(ctx,
		`DELETE FROM sessions WHERE id = $1::uuid AND user_id = $2::uuid`, id, userID)
}

// DeleteOthersForUser removes all of a user's sessions except keepID (nil keeps
// none). Returns the number of sessions revoked.
func (r *SessionRepo) DeleteOthersForUser(ctx context.Context, userID string, keepID *string) (int64, error) {
	tag, err := r.s.pool.Exec(ctx,
		`DELETE FROM sessions
		 WHERE user_id = $1::uuid AND ($2::uuid IS NULL OR id <> $2::uuid)`,
		userID, keepID)
	if err != nil {
		return 0, mapError(err)
	}
	return tag.RowsAffected(), nil
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
