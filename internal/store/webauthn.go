package store

import (
	"context"
	"time"
)

// WebAuthnCredential is one registered passkey. Everything on it is public
// credential material: the raw credential id, the JSON-encoded credential record
// (COSE public key + attestation metadata), and value-free usage metadata. The
// store stays format-blind — it never parses Credential.
type WebAuthnCredential struct {
	ID           string
	UserID       string
	RPID         string
	CredentialID []byte
	// Credential is the JSON-encoded go-webauthn credential record. Opaque here.
	Credential []byte
	SignCount  int64
	Nickname   string
	CreatedAt  time.Time
	LastUsedAt *time.Time
}

// WebAuthnChallenge is a pending ceremony. Challenge is the base64url challenge
// echoed back by the browser inside clientDataJSON; SessionData is the
// JSON-encoded go-webauthn SessionData. UserID is nil for a login challenge
// issued against an unknown email (the begin endpoint must answer identically
// for known and unknown accounts).
type WebAuthnChallenge struct {
	ID          string
	Challenge   string
	Purpose     string
	UserID      *string
	SessionData []byte
	CreatedAt   time.Time
	ExpiresAt   time.Time
}

// WebAuthnRepo persists passkey credentials and pending ceremony challenges.
type WebAuthnRepo struct{ s *Store }

// NewWebAuthnRepo returns a WebAuthn repository.
func NewWebAuthnRepo(s *Store) *WebAuthnRepo { return &WebAuthnRepo{s: s} }

// InsertChallenge stores a pending ceremony. userID may be nil (unknown-account
// login probe). The challenge column is UNIQUE, so a (cryptographically
// impossible) collision surfaces as ErrAlreadyExists rather than silently
// overwriting a live ceremony.
func (r *WebAuthnRepo) InsertChallenge(ctx context.Context, challenge, purpose string, userID *string, sessionData []byte, expiresAt time.Time) error {
	_, err := r.s.pool.Exec(ctx,
		`INSERT INTO webauthn_challenges (challenge, purpose, user_id, session_data, expires_at)
		 VALUES ($1, $2, $3::uuid, $4, $5)`,
		challenge, purpose, userID, sessionData, expiresAt)
	return mapError(err)
}

// ClaimChallenge atomically consumes a pending challenge: it deletes the row and
// returns it, but only when the purpose matches and it has not expired. The
// DELETE ... RETURNING is the single-use guarantee — two concurrent finishes for
// the same challenge cannot both win, and a replay after the first finish finds
// nothing (ErrNotFound). An expired row is left in place for the sweeper and
// reported as ErrNotFound, so expiry and replay are indistinguishable to the
// caller.
func (r *WebAuthnRepo) ClaimChallenge(ctx context.Context, challenge, purpose string) (*WebAuthnChallenge, error) {
	var c WebAuthnChallenge
	err := r.s.pool.QueryRow(ctx,
		`DELETE FROM webauthn_challenges
		  WHERE challenge = $1 AND purpose = $2 AND expires_at > now()
		 RETURNING id::text, challenge, purpose, user_id::text, session_data, created_at, expires_at`,
		challenge, purpose).
		Scan(&c.ID, &c.Challenge, &c.Purpose, &c.UserID, &c.SessionData, &c.CreatedAt, &c.ExpiresAt)
	if err != nil {
		return nil, mapError(err)
	}
	return &c, nil
}

// DeleteExpiredChallenges removes challenges past their expiry. Called
// opportunistically from the begin endpoints and at boot; bounded work.
func (r *WebAuthnRepo) DeleteExpiredChallenges(ctx context.Context) error {
	_, err := r.s.pool.Exec(ctx, `DELETE FROM webauthn_challenges WHERE expires_at <= now()`)
	return mapError(err)
}

// InsertCredential registers a passkey. A duplicate credential id (the same
// authenticator credential registered twice, possibly to a different account) or
// a duplicate nickname for the user maps to ErrAlreadyExists.
func (r *WebAuthnRepo) InsertCredential(ctx context.Context, userID, rpID string, credentialID, credential []byte, signCount int64, nickname string) (*WebAuthnCredential, error) {
	var c WebAuthnCredential
	err := r.s.pool.QueryRow(ctx,
		`INSERT INTO webauthn_credentials (user_id, rp_id, credential_id, credential, sign_count, nickname)
		 VALUES ($1::uuid, $2, $3, $4, $5, $6)
		 RETURNING id::text, user_id::text, rp_id, credential_id, credential, sign_count, nickname, created_at, last_used_at`,
		userID, rpID, credentialID, credential, signCount, nickname).
		Scan(&c.ID, &c.UserID, &c.RPID, &c.CredentialID, &c.Credential, &c.SignCount, &c.Nickname, &c.CreatedAt, &c.LastUsedAt)
	if err != nil {
		return nil, mapError(err)
	}
	return &c, nil
}

// ListCredentials returns a user's passkeys registered under rpID, oldest first.
// Credentials registered under a different RP ID are excluded: they are not
// usable for the current relying party and must not be offered in allowCredentials.
func (r *WebAuthnRepo) ListCredentials(ctx context.Context, userID, rpID string) ([]WebAuthnCredential, error) {
	rows, err := r.s.pool.Query(ctx,
		`SELECT id::text, user_id::text, rp_id, credential_id, credential, sign_count, nickname, created_at, last_used_at
		   FROM webauthn_credentials
		  WHERE user_id = $1::uuid AND rp_id = $2
		  ORDER BY created_at, id`, userID, rpID)
	if err != nil {
		return nil, mapError(err)
	}
	defer rows.Close()
	out := []WebAuthnCredential{}
	for rows.Next() {
		var c WebAuthnCredential
		if err := rows.Scan(&c.ID, &c.UserID, &c.RPID, &c.CredentialID, &c.Credential,
			&c.SignCount, &c.Nickname, &c.CreatedAt, &c.LastUsedAt); err != nil {
			return nil, mapError(err)
		}
		out = append(out, c)
	}
	return out, mapError(rows.Err())
}

// CountCredentials returns how many passkeys a user has under rpID.
func (r *WebAuthnRepo) CountCredentials(ctx context.Context, userID, rpID string) (int, error) {
	var n int
	err := r.s.pool.QueryRow(ctx,
		`SELECT count(*) FROM webauthn_credentials WHERE user_id = $1::uuid AND rp_id = $2`,
		userID, rpID).Scan(&n)
	return n, mapError(err)
}

// GetCredentialByCredentialID looks a passkey up by its raw authenticator
// credential id (globally unique). ErrNotFound when unknown.
func (r *WebAuthnRepo) GetCredentialByCredentialID(ctx context.Context, credentialID []byte) (*WebAuthnCredential, error) {
	var c WebAuthnCredential
	err := r.s.pool.QueryRow(ctx,
		`SELECT id::text, user_id::text, rp_id, credential_id, credential, sign_count, nickname, created_at, last_used_at
		   FROM webauthn_credentials WHERE credential_id = $1`, credentialID).
		Scan(&c.ID, &c.UserID, &c.RPID, &c.CredentialID, &c.Credential,
			&c.SignCount, &c.Nickname, &c.CreatedAt, &c.LastUsedAt)
	if err != nil {
		return nil, mapError(err)
	}
	return &c, nil
}

// RecordAssertion records a successful assertion: it refreshes the stored
// credential record, stamps last_used_at, and advances the signature counter —
// but ONLY when newCount is strictly greater than the stored one. This is the
// clone/replay detector required by WebAuthn L3 §6.1.3 step 21, enforced in the
// database as a compare-and-swap so two concurrent assertions carrying the same
// counter cannot both succeed.
//
// Authenticators that do not implement a counter always report 0; for those the
// stored count stays 0 and the update is allowed (newCount == 0 && stored == 0),
// since a counter that is always zero carries no clone signal.
//
// Returns false when the counter did not move forward (clone warning / replay);
// the caller must then reject the assertion.
func (r *WebAuthnRepo) RecordAssertion(ctx context.Context, id string, credential []byte, newCount int64) (bool, error) {
	tag, err := r.s.pool.Exec(ctx,
		`UPDATE webauthn_credentials
		    SET credential = $2, sign_count = $3, last_used_at = now()
		  WHERE id = $1::uuid
		    AND ($3 > sign_count OR ($3 = 0 AND sign_count = 0))`,
		id, credential, newCount)
	if err != nil {
		return false, mapError(err)
	}
	return tag.RowsAffected() == 1, nil
}

// RenameCredential sets a passkey's nickname, scoped to its owner so renaming
// another user's credential is indistinguishable from a missing one. A nickname
// already used by the same user maps to ErrAlreadyExists.
func (r *WebAuthnRepo) RenameCredential(ctx context.Context, id, userID, nickname string) error {
	return r.s.execAffectingOne(ctx,
		`UPDATE webauthn_credentials SET nickname = $3
		  WHERE id = $1::uuid AND user_id = $2::uuid`, id, userID, nickname)
}

// DeleteCredential removes one of the user's own passkeys. Scoped to userID, so
// deleting someone else's credential returns ErrNotFound.
func (r *WebAuthnRepo) DeleteCredential(ctx context.Context, id, userID string) error {
	return r.s.execAffectingOne(ctx,
		`DELETE FROM webauthn_credentials WHERE id = $1::uuid AND user_id = $2::uuid`, id, userID)
}
