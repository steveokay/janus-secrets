package auth

import (
	"context"
	"crypto/rand"
	"encoding/base32"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/steveokay/janus-secrets/internal/crypto"
	"github.com/steveokay/janus-secrets/internal/store"
)

const (
	totpIssuer         = "Janus"
	recoveryCodeCount  = 10
	recoveryCodeBytes  = 8 // random bytes per recovery code
	totpVerifySkew     = 1 // ±1 step (±30s) clock tolerance
	recoveryCodeGroups = 4
)

var b32 = base32.StdEncoding.WithPadding(base32.NoPadding)

// TOTPStatus reports whether a user has an activated TOTP factor and how many
// recovery codes remain.
func (s *Service) TOTPStatus(ctx context.Context, userID string) (enabled bool, recoveryRemaining int, err error) {
	t, err := s.totp.GetTOTP(ctx, userID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return false, 0, nil
		}
		return false, 0, err
	}
	if t.ActivatedAt == nil {
		return false, 0, nil
	}
	n, err := s.totp.CountUnusedRecoveryCodes(ctx, userID)
	if err != nil {
		return false, 0, err
	}
	return true, n, nil
}

// EnrollTOTP generates a fresh secret for a user (replacing any un-confirmed
// enrollment) and returns the base32 secret + an otpauth:// URI shown once. It
// refuses to overwrite an already-activated factor (disable first).
func (s *Service) EnrollTOTP(ctx context.Context, userID, accountLabel string) (secretB32, otpauthURL string, err error) {
	if existing, gErr := s.totp.GetTOTP(ctx, userID); gErr == nil && existing.ActivatedAt != nil {
		return "", "", fmt.Errorf("%w: TOTP already enabled; disable it first", ErrTOTPState)
	} else if gErr != nil && !errors.Is(gErr, store.ErrNotFound) {
		return "", "", gErr
	}
	secret, err := crypto.GenerateTOTPSecret()
	if err != nil {
		return "", "", err
	}
	defer zeroize(secret)
	ct, err := s.keyring.WrapTOTPSecret(userID, secret)
	if err != nil {
		return "", "", err // crypto.ErrSealed passes through
	}
	if err := s.totp.Upsert(ctx, userID, ct.Marshal()); err != nil {
		return "", "", err
	}
	secretB32 = b32.EncodeToString(secret)
	return secretB32, otpauthURI(accountLabel, secretB32), nil
}

// otpauthURI builds the standard provisioning URI for authenticator apps.
func otpauthURI(account, secretB32 string) string {
	label := url.PathEscape(totpIssuer + ":" + account)
	q := url.Values{}
	q.Set("secret", secretB32)
	q.Set("issuer", totpIssuer)
	q.Set("algorithm", "SHA1")
	q.Set("digits", "6")
	q.Set("period", "30")
	return "otpauth://totp/" + label + "?" + q.Encode()
}

// ConfirmTOTP verifies a code against the pending secret, activates the factor,
// and returns a fresh set of single-use recovery codes (shown once).
func (s *Service) ConfirmTOTP(ctx context.Context, userID, code string) ([]string, error) {
	t, err := s.totp.GetTOTP(ctx, userID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, fmt.Errorf("%w: no pending enrollment", ErrTOTPState)
		}
		return nil, err
	}
	secret, err := s.unwrapTOTP(userID, t)
	if err != nil {
		return nil, err
	}
	defer zeroize(secret)
	if !crypto.VerifyTOTP(secret, strings.TrimSpace(code), time.Now(), totpVerifySkew) {
		return nil, ErrInvalidCredentials
	}
	if err := s.totp.Activate(ctx, userID); err != nil {
		return nil, err
	}
	return s.regenerateRecoveryCodes(ctx, userID)
}

// DisableTOTP verifies a current TOTP or recovery code, then removes the factor
// and all recovery codes.
func (s *Service) DisableTOTP(ctx context.Context, userID, code string) error {
	ok, err := s.verifySecondFactor(ctx, userID, code)
	if err != nil {
		return err
	}
	if !ok {
		return ErrInvalidCredentials
	}
	return s.totp.DeleteTOTP(ctx, userID)
}

// RegenerateRecoveryCodes re-issues the recovery-code set after verifying a
// current second factor.
func (s *Service) RegenerateRecoveryCodes(ctx context.Context, userID, code string) ([]string, error) {
	ok, err := s.verifySecondFactor(ctx, userID, code)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, ErrInvalidCredentials
	}
	return s.regenerateRecoveryCodes(ctx, userID)
}

// verifySecondFactor accepts either a live TOTP code or an unused recovery code.
// Only meaningful for an activated factor.
func (s *Service) verifySecondFactor(ctx context.Context, userID, code string) (bool, error) {
	t, err := s.totp.GetTOTP(ctx, userID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return false, fmt.Errorf("%w: TOTP not enabled", ErrTOTPState)
		}
		return false, err
	}
	if t.ActivatedAt == nil {
		return false, fmt.Errorf("%w: TOTP not enabled", ErrTOTPState)
	}
	code = strings.TrimSpace(code)
	secret, err := s.unwrapTOTP(userID, t)
	if err != nil {
		return false, err
	}
	defer zeroize(secret)
	if crypto.VerifyTOTP(secret, code, time.Now(), totpVerifySkew) {
		return true, nil
	}
	// Fall back to a recovery code (normalized: strip spaces/dashes, upper).
	return s.consumeRecovery(ctx, userID, code)
}

func (s *Service) consumeRecovery(ctx context.Context, userID, code string) (bool, error) {
	h, err := s.recoveryHMAC(ctx, code)
	if err != nil {
		return false, err
	}
	return s.totp.ConsumeRecoveryCode(ctx, userID, h)
}

// regenerateRecoveryCodes creates a new set, stores their HMACs, and returns the
// plaintext codes.
func (s *Service) regenerateRecoveryCodes(ctx context.Context, userID string) ([]string, error) {
	codes := make([]string, 0, recoveryCodeCount)
	hmacs := make([][]byte, 0, recoveryCodeCount)
	for i := 0; i < recoveryCodeCount; i++ {
		c, err := newRecoveryCode()
		if err != nil {
			return nil, err
		}
		h, err := s.recoveryHMAC(ctx, c)
		if err != nil {
			return nil, err
		}
		codes = append(codes, c)
		hmacs = append(hmacs, h)
	}
	if err := s.totp.ReplaceRecoveryCodes(ctx, userID, hmacs); err != nil {
		return nil, err
	}
	return codes, nil
}

// recoveryHMAC normalizes a recovery code (strip dashes/spaces, upper-case) and
// HMACs it under the token-HMAC key, so codes are never stored in the clear and
// a DB dump cannot be verified offline.
func (s *Service) recoveryHMAC(ctx context.Context, code string) ([]byte, error) {
	key, err := s.hmacKey(ctx)
	if err != nil {
		return nil, err
	}
	defer zeroize(key)
	norm := strings.ToUpper(strings.NewReplacer("-", "", " ", "").Replace(strings.TrimSpace(code)))
	return mac(key, "recovery:"+norm), nil
}

// newRecoveryCode returns a random human-typable recovery code (base32 groups).
func newRecoveryCode() (string, error) {
	raw := make([]byte, recoveryCodeBytes)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	s := b32.EncodeToString(raw) // ~13 chars
	// Group as xxxx-xxxx-xxxx-x for readability.
	var b strings.Builder
	for i, r := range s {
		if i > 0 && i%recoveryCodeGroups == 0 {
			b.WriteByte('-')
		}
		b.WriteRune(r)
	}
	return b.String(), nil
}

func (s *Service) unwrapTOTP(userID string, t *store.UserTOTP) ([]byte, error) {
	ct, err := crypto.ParseCiphertext(t.WrappedSecret)
	if err != nil {
		return nil, err
	}
	return s.keyring.UnwrapTOTPSecret(userID, ct)
}
