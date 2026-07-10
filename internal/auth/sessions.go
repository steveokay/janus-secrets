package auth

import (
	"context"
	"errors"
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

// Login verifies email+password and mints a session, returning the opaque
// cookie value. All failures are ErrInvalidCredentials.
func (s *Service) Login(ctx context.Context, email string, password []byte) (string, error) {
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
	if _, err := s.sessions.Create(ctx, userID, mac(key, cookie), time.Now().Add(sessionTTL)); err != nil {
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
