package auth

import (
	"context"
	"crypto/hmac"
	"errors"
	"strings"
	"time"

	"github.com/steveokay/janus-secrets/internal/store"
)

// sessionTTL is the fixed session lifetime (last_seen slides for observability;
// expiry is absolute).
const sessionTTL = 24 * time.Hour

// dummyPHC equalizes timing when the user does not exist: Login always runs
// one Argon2id verification. The password behind it is unknowable (random at
// package init would break determinism; a fixed hash of nothing-in-particular
// is fine because its only purpose is to burn the same CPU).
var dummyPHC = func() string {
	h, err := HashPassword([]byte("janus-dummy-timing-equalizer"))
	if err != nil {
		panic(err)
	}
	return h
}()

// Login verifies email+password (and a TOTP second factor when the user has one
// enabled) and mints a session, returning the opaque cookie value. Password
// failures are ErrInvalidCredentials (no enumeration oracle). If the password is
// correct but an activated TOTP factor exists and totpCode is empty, it returns
// ErrTOTPRequired; a wrong code is ErrInvalidCredentials. totpCode may be a live
// TOTP code or an unused recovery code.
func (s *Service) Login(ctx context.Context, email string, password []byte, totpCode string) (string, error) {
	defer zeroize(password)

	u, err := s.users.GetByEmail(ctx, email)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			// Burn the same Argon2id cost as a real verification.
			_, _, _ = VerifyPassword(dummyPHC, password)
			return "", ErrInvalidCredentials
		}
		return "", err
	}
	if u.DisabledAt != nil || u.PasswordHash == nil {
		_, _, _ = VerifyPassword(dummyPHC, password)
		return "", ErrInvalidCredentials
	}
	ok, needsRehash, err := VerifyPassword(*u.PasswordHash, password)
	if err != nil || !ok {
		return "", ErrInvalidCredentials
	}
	if needsRehash {
		if newHash, hErr := HashPassword(password); hErr == nil {
			_ = s.users.UpdatePassword(ctx, u.ID, newHash) // best-effort
		}
	}

	// Second factor: only enforced for users with an activated TOTP.
	enabled, _, err := s.TOTPStatus(ctx, u.ID)
	if err != nil {
		return "", err
	}
	if enabled {
		if strings.TrimSpace(totpCode) == "" {
			return "", ErrTOTPRequired
		}
		verified, err := s.verifySecondFactor(ctx, u.ID, totpCode)
		if err != nil {
			return "", err
		}
		if !verified {
			return "", ErrInvalidCredentials
		}
	}

	return s.createSession(ctx, u.ID)
}

// createSession mints a session cookie for an already-authenticated user
// (password verified, or OIDC identity resolved). The caller owns the auth
// decision; this only issues the credential.
func (s *Service) createSession(ctx context.Context, userID string) (string, error) {
	cookie, err := randToken(32)
	if err != nil {
		return "", err
	}
	key, err := s.hmacKey(ctx)
	if err != nil {
		return "", err
	}
	defer zeroize(key)
	meta := sessionMetaFrom(ctx)
	if _, err := s.sessions.Create(ctx, userID, mac(key, cookie), time.Now().Add(sessionTTL), meta.IP, meta.UserAgent); err != nil {
		return "", err
	}
	return cookie, nil
}

// VerifySession resolves a cookie value to a Principal. Expired sessions are
// deleted on sight.
func (s *Service) VerifySession(ctx context.Context, cookie string) (Principal, error) {
	if cookie == "" {
		return Principal{}, ErrUnauthenticated
	}
	key, err := s.hmacKey(ctx)
	if err != nil {
		return Principal{}, err // crypto.ErrSealed passes through
	}
	defer zeroize(key)
	sess, err := s.sessions.GetByHMAC(ctx, mac(key, cookie))
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return Principal{}, ErrUnauthenticated
		}
		return Principal{}, err
	}
	if time.Now().After(sess.ExpiresAt) {
		_ = s.sessions.Delete(ctx, sess.ID) // opportunistic cleanup
		return Principal{}, ErrUnauthenticated
	}
	if s.idleTimeout > 0 {
		last := sess.LastSeenAt
		if last.IsZero() { // defensive: column is NOT NULL, but stay safe
			last = sess.CreatedAt
		}
		if time.Since(last) > s.idleTimeout {
			_ = s.sessions.Delete(ctx, sess.ID) // opportunistic cleanup
			return Principal{}, ErrSessionExpired
		}
	}
	_ = s.sessions.TouchLastSeen(ctx, sess.ID) // best-effort
	u, err := s.users.Get(ctx, sess.UserID)
	if err != nil {
		return Principal{}, ErrUnauthenticated
	}
	if u.DisabledAt != nil {
		return Principal{}, ErrUnauthenticated
	}
	return Principal{Kind: KindUser, ID: u.ID, Name: u.Email}, nil
}

// Logout deletes the session. Unknown cookies are a no-op (idempotent).
func (s *Service) Logout(ctx context.Context, cookie string) error {
	key, err := s.hmacKey(ctx)
	if err != nil {
		return err
	}
	defer zeroize(key)
	if err := s.sessions.DeleteByHMAC(ctx, mac(key, cookie)); err != nil && !errors.Is(err, store.ErrNotFound) {
		return err
	}
	return nil
}

// ChangePassword re-verifies the old password and stores a new hash.
func (s *Service) ChangePassword(ctx context.Context, userID string, oldPW, newPW []byte) error {
	defer zeroize(oldPW)
	defer zeroize(newPW)
	u, err := s.users.Get(ctx, userID)
	if err != nil {
		return ErrInvalidCredentials
	}
	if u.PasswordHash == nil {
		return ErrInvalidCredentials
	}
	ok, _, err := VerifyPassword(*u.PasswordHash, oldPW)
	if err != nil || !ok {
		return ErrInvalidCredentials
	}
	hash, err := HashPassword(newPW)
	if err != nil {
		return err
	}
	return s.users.UpdatePassword(ctx, u.ID, hash)
}

// SweepExpiredSessions removes all expired sessions (called at boot).
func (s *Service) SweepExpiredSessions(ctx context.Context) error {
	return s.sessions.DeleteExpired(ctx)
}

// SessionInfo describes one active session for the self-service management
// surface. It carries no credential material — no HMAC, no cookie value — only
// non-secret metadata and a Current flag marking the requesting session.
type SessionInfo struct {
	ID         string
	CreatedAt  time.Time
	LastSeenAt time.Time
	ExpiresAt  time.Time
	IP         string
	UserAgent  string
	Current    bool
}

// ListSessions returns a user's non-expired sessions. currentCookie (the
// caller's own cookie, or "") marks which entry is the requesting session so
// the UI never lets a user cut the branch they are sitting on by accident.
func (s *Service) ListSessions(ctx context.Context, userID, currentCookie string) ([]SessionInfo, error) {
	var currentHMAC []byte
	if currentCookie != "" {
		key, err := s.hmacKey(ctx)
		if err != nil {
			return nil, err
		}
		currentHMAC = mac(key, currentCookie)
		zeroize(key)
	}
	rows, err := s.sessions.ListByUser(ctx, userID)
	if err != nil {
		return nil, err
	}
	out := make([]SessionInfo, 0, len(rows))
	for _, sess := range rows {
		info := SessionInfo{
			ID: sess.ID, CreatedAt: sess.CreatedAt, LastSeenAt: sess.LastSeenAt,
			ExpiresAt: sess.ExpiresAt, Current: hmac.Equal(sess.TokenHMAC, currentHMAC),
		}
		if sess.IP != nil {
			info.IP = *sess.IP
		}
		if sess.UserAgent != nil {
			info.UserAgent = *sess.UserAgent
		}
		out = append(out, info)
	}
	return out, nil
}

// RevokeSession deletes one of the user's own sessions by id. Revoking a
// session belonging to another user is indistinguishable from a missing one
// (ErrNotFound) — the store scopes the delete to userID.
func (s *Service) RevokeSession(ctx context.Context, userID, sessionID string) error {
	err := s.sessions.DeleteForUser(ctx, sessionID, userID)
	if errors.Is(err, store.ErrNotFound) {
		return ErrNotFound
	}
	return err
}

// RevokeOtherSessions deletes all of the user's sessions except the one
// identified by currentCookie (kept so the caller stays logged in). A "" cookie
// (e.g. a token-authenticated caller) revokes every session. Returns the count.
func (s *Service) RevokeOtherSessions(ctx context.Context, userID, currentCookie string) (int, error) {
	var keepID *string
	if currentCookie != "" {
		key, err := s.hmacKey(ctx)
		if err != nil {
			return 0, err
		}
		sess, err := s.sessions.GetByHMAC(ctx, mac(key, currentCookie))
		zeroize(key)
		if err != nil && !errors.Is(err, store.ErrNotFound) {
			return 0, err
		}
		if sess != nil {
			keepID = &sess.ID
		}
	}
	n, err := s.sessions.DeleteOthersForUser(ctx, userID, keepID)
	return int(n), err
}
