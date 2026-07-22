package store

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
)

// UserTOTP is a user's enrolled TOTP factor. WrappedSecret is the master-wrapped
// shared secret; ActivatedAt is nil until the enrollment is confirmed. The store
// stays crypto-blind.
type UserTOTP struct {
	UserID        string
	WrappedSecret []byte
	ActivatedAt   *time.Time
	CreatedAt     time.Time
}

// TOTPRepo persists TOTP secrets and single-use recovery codes.
type TOTPRepo struct{ s *Store }

// NewTOTPRepo returns a TOTP repository.
func NewTOTPRepo(s *Store) *TOTPRepo { return &TOTPRepo{s: s} }

// Upsert stores a fresh (unconfirmed) TOTP secret for a user, replacing any
// existing enrollment — re-enrolling resets confirmation.
func (r *TOTPRepo) Upsert(ctx context.Context, userID string, wrappedSecret []byte) error {
	_, err := r.s.pool.Exec(ctx,
		`INSERT INTO user_totp (user_id, wrapped_secret, activated_at, created_at)
		 VALUES ($1::uuid, $2, NULL, now())
		 ON CONFLICT (user_id) DO UPDATE
		   SET wrapped_secret = EXCLUDED.wrapped_secret, activated_at = NULL, created_at = now()`,
		userID, wrappedSecret)
	return mapError(err)
}

// GetTOTP returns a user's TOTP row, or ErrNotFound.
func (r *TOTPRepo) GetTOTP(ctx context.Context, userID string) (*UserTOTP, error) {
	var t UserTOTP
	err := r.s.pool.QueryRow(ctx,
		`SELECT user_id::text, wrapped_secret, activated_at, created_at
		   FROM user_totp WHERE user_id = $1::uuid`, userID).
		Scan(&t.UserID, &t.WrappedSecret, &t.ActivatedAt, &t.CreatedAt)
	if err != nil {
		return nil, mapError(err)
	}
	return &t, nil
}

// Activate confirms an enrollment (sets activated_at). ErrNotFound if there is
// no pending row.
func (r *TOTPRepo) Activate(ctx context.Context, userID string) error {
	return r.s.execAffectingOne(ctx,
		`UPDATE user_totp SET activated_at = now()
		  WHERE user_id = $1::uuid AND activated_at IS NULL`, userID)
}

// DeleteTOTP removes a user's TOTP factor and all of their recovery codes.
// user_recovery_codes references users(id), not user_totp, so deleting the
// factor row alone would strand still-valid recovery codes that a later
// re-enrollment could inherit; both are removed atomically here.
func (r *TOTPRepo) DeleteTOTP(ctx context.Context, userID string) error {
	return r.s.withTx(ctx, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx,
			`DELETE FROM user_recovery_codes WHERE user_id = $1::uuid`, userID); err != nil {
			return mapError(err)
		}
		tag, err := tx.Exec(ctx, `DELETE FROM user_totp WHERE user_id = $1::uuid`, userID)
		if err != nil {
			return mapError(err)
		}
		if tag.RowsAffected() == 0 {
			return ErrNotFound
		}
		return nil
	})
}

// ReplaceRecoveryCodes atomically replaces a user's recovery-code set.
func (r *TOTPRepo) ReplaceRecoveryCodes(ctx context.Context, userID string, hmacs [][]byte) error {
	return r.s.withTx(ctx, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `DELETE FROM user_recovery_codes WHERE user_id = $1::uuid`, userID); err != nil {
			return mapError(err)
		}
		for _, h := range hmacs {
			if _, err := tx.Exec(ctx,
				`INSERT INTO user_recovery_codes (user_id, code_hmac) VALUES ($1::uuid, $2)`, userID, h); err != nil {
				return mapError(err)
			}
		}
		return nil
	})
}

// ConsumeRecoveryCode marks one unused recovery code (by its HMAC) as spent,
// returning true iff a matching unused code existed. Single-use is enforced
// unconditionally: the inner SELECT takes a row lock with FOR UPDATE SKIP
// LOCKED and the outer UPDATE repeats used_at IS NULL, so two concurrent
// logins presenting the same code can never both succeed — the loser's
// subquery skips the locked row and matches nothing.
func (r *TOTPRepo) ConsumeRecoveryCode(ctx context.Context, userID string, codeHMAC []byte) (bool, error) {
	var id string
	err := r.s.pool.QueryRow(ctx,
		`UPDATE user_recovery_codes SET used_at = now()
		  WHERE used_at IS NULL
		    AND id = (SELECT id FROM user_recovery_codes
		              WHERE user_id = $1::uuid AND code_hmac = $2 AND used_at IS NULL
		              LIMIT 1 FOR UPDATE SKIP LOCKED)
		  RETURNING id::text`, userID, codeHMAC).Scan(&id)
	if err != nil {
		if err == pgx.ErrNoRows {
			return false, nil
		}
		return false, mapError(err)
	}
	return true, nil
}

// CountUnusedRecoveryCodes returns how many recovery codes remain for a user.
func (r *TOTPRepo) CountUnusedRecoveryCodes(ctx context.Context, userID string) (int, error) {
	var n int
	err := r.s.pool.QueryRow(ctx,
		`SELECT count(*) FROM user_recovery_codes WHERE user_id = $1::uuid AND used_at IS NULL`, userID).Scan(&n)
	return n, mapError(err)
}
