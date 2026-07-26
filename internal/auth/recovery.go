package auth

import (
	"context"
	"errors"
	"strings"

	"github.com/steveokay/janus-secrets/internal/store"
)

// This file holds the local-console disaster-recovery primitives behind
// `janus admin reset-password`. They are deliberately NOT reachable over HTTP:
// nothing in internal/api calls them, and they exist so an operator standing on
// the server host can recover a locked-out account without destroying the
// database. See docs/guides/disaster-recovery.md.

// ErrNoPriorPassword is returned by PasswordReset.Undo when the account had no
// password hash before the reset (a federated / OIDC-only identity). There is
// nothing to restore, so the freshly minted password stays live and the
// operator must be told so explicitly.
var ErrNoPriorPassword = errors.New("auth: account had no prior password to restore")

// PasswordReset is the outcome of a local-console password reset.
type PasswordReset struct {
	UserID string
	// Email is the stored (canonical) address, which may differ in case from
	// the one the operator typed.
	Email string
	// Password is the freshly generated password. It lives in memory and in the
	// operator's terminal only: Janus persists nothing but its Argon2id hash,
	// and it is never logged, never put in an error string, and never recorded
	// in the audit log.
	Password string
	// SessionsRevoked is how many live UI sessions were destroyed by the reset.
	SessionsRevoked int

	// priorHash is the PHC string the account carried before the reset, kept so
	// Undo can roll the credential back when the mandatory audit append fails.
	// nil means the account had no password at all.
	priorHash *string
	users     *store.UserRepo
}

// ResetPasswordByEmail generates a fresh random password for the user with the
// given email, replaces the stored Argon2id hash, and revokes every session the
// account holds. It verifies no old password — that is the whole point; the
// authority to run it comes from the caller proving possession of the seal
// material (see cmd/janus/admin_commands.go) plus root on the host, not from a
// credential the locked-out user no longer has.
//
// Sessions are revoked BEFORE the credential is replaced, so a failure part way
// through fails safe: worst case every session is gone and the old password
// still works. Revocation needs no cookie and therefore no HMAC key, so it works
// on a sealed keyring.
//
// Returns ErrValidation for an empty email and ErrNotFound for an unknown one.
func (s *Service) ResetPasswordByEmail(ctx context.Context, email string) (*PasswordReset, error) {
	email = strings.TrimSpace(email)
	if email == "" {
		return nil, ErrValidation
	}
	u, err := s.users.GetByEmail(ctx, email)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}

	// Kill every live session first: any of them may be the session an attacker
	// established with the credential we are about to retire.
	revoked, err := s.RevokeOtherSessions(ctx, u.ID, "")
	if err != nil {
		return nil, err
	}

	// Same strength as the bootstrap admin credential minted by createUser:
	// 24 random bytes rendered as 32 base64url characters.
	password, err := randToken(24)
	if err != nil {
		return nil, err
	}
	pw := []byte(password)
	hash, err := HashPassword(pw)
	zeroize(pw)
	if err != nil {
		return nil, err
	}
	if err := s.users.UpdatePassword(ctx, u.ID, hash); err != nil {
		return nil, err
	}
	return &PasswordReset{
		UserID:          u.ID,
		Email:           u.Email,
		Password:        password,
		SessionsRevoked: revoked,
		priorHash:       u.PasswordHash,
		users:           s.users,
	}, nil
}

// Undo restores the password hash the account carried before the reset. It
// exists for one caller: the local-console command, when the mandatory audit
// append fails after the credential was already replaced. Rolling back keeps
// the invariant that no credential change survives unaudited.
//
// Revoked sessions are NOT restored (they cannot be) — that direction is
// fail-safe. Returns ErrNoPriorPassword when there was no hash to restore.
func (r *PasswordReset) Undo(ctx context.Context) error {
	if r.priorHash == nil {
		return ErrNoPriorPassword
	}
	return r.users.UpdatePassword(ctx, r.UserID, *r.priorHash)
}

// ClearTOTP removes a user's TOTP factor and every recovery code WITHOUT
// verifying a second factor. It is the local-console counterpart to
// DisableTOTP, for the case where the authenticator device is lost along with
// the password. Nothing in internal/api calls it — over HTTP, disabling TOTP
// always requires a current code or recovery code.
//
// Returns ErrNotFound when the user has no enrolment.
func (s *Service) ClearTOTP(ctx context.Context, userID string) error {
	if err := s.totp.DeleteTOTP(ctx, userID); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return ErrNotFound
		}
		return err
	}
	return nil
}
