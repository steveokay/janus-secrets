package auth

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/steveokay/janus-secrets/internal/crypto"
)

// codeFor decodes the base32 secret returned by EnrollTOTP and produces a live
// TOTP code for "now".
func codeFor(t *testing.T, secretB32 string) string {
	t.Helper()
	raw, err := b32.DecodeString(secretB32)
	if err != nil {
		t.Fatalf("decode secret: %v", err)
	}
	return crypto.TOTPCodeAt(raw, time.Now())
}

// enrollConfirm runs a full enroll→confirm and returns the base32 secret and the
// recovery codes issued on confirmation.
func enrollConfirm(t *testing.T, svc *Service, ctx context.Context, uid string) (string, []string) {
	t.Helper()
	secret, otpauth, err := svc.EnrollTOTP(ctx, uid, "user@example.com")
	if err != nil {
		t.Fatalf("enroll: %v", err)
	}
	if secret == "" || otpauth == "" {
		t.Fatalf("empty enroll result: secret=%q url=%q", secret, otpauth)
	}
	codes, err := svc.ConfirmTOTP(ctx, uid, codeFor(t, secret))
	if err != nil {
		t.Fatalf("confirm: %v", err)
	}
	if len(codes) == 0 {
		t.Fatal("no recovery codes issued on confirm")
	}
	return secret, codes
}

func TestTOTPEnrollConfirmStatus(t *testing.T) {
	svc, email, _ := newTestService(t)
	ctx := context.Background()
	uid, err := svc.userByEmailForTest(ctx, email)
	if err != nil {
		t.Fatal(err)
	}

	// Before enrollment: disabled, zero recovery codes.
	if enabled, n, err := svc.TOTPStatus(ctx, uid); err != nil || enabled || n != 0 {
		t.Fatalf("pre-enroll status: enabled=%v n=%d err=%v", enabled, n, err)
	}

	secret, codes := enrollConfirm(t, svc, ctx, uid)

	// After confirmation: enabled with the full recovery set.
	enabled, n, err := svc.TOTPStatus(ctx, uid)
	if err != nil || !enabled || n != len(codes) {
		t.Fatalf("post-confirm status: enabled=%v n=%d (codes=%d) err=%v", enabled, n, len(codes), err)
	}

	// A live code produced from the same secret verifies as a second factor.
	if ok, err := svc.verifySecondFactor(ctx, uid, codeFor(t, secret)); err != nil || !ok {
		t.Fatalf("verifySecondFactor with live code: ok=%v err=%v", ok, err)
	}
}

func TestTOTPEnrollRefusesWhenActive(t *testing.T) {
	svc, email, _ := newTestService(t)
	ctx := context.Background()
	uid, _ := svc.userByEmailForTest(ctx, email)
	enrollConfirm(t, svc, ctx, uid)

	// Re-enrolling an active factor is refused.
	if _, _, err := svc.EnrollTOTP(ctx, uid, "user@example.com"); !errors.Is(err, ErrTOTPState) {
		t.Fatalf("re-enroll active: want ErrTOTPState, got %v", err)
	}
}

func TestTOTPReEnrollBeforeActivationAllowed(t *testing.T) {
	svc, email, _ := newTestService(t)
	ctx := context.Background()
	uid, _ := svc.userByEmailForTest(ctx, email)

	// First enroll (pending, not confirmed).
	if _, _, err := svc.EnrollTOTP(ctx, uid, "user@example.com"); err != nil {
		t.Fatal(err)
	}
	// Second enroll before activation is allowed and rotates the secret.
	secret2, _, err := svc.EnrollTOTP(ctx, uid, "user@example.com")
	if err != nil {
		t.Fatalf("re-enroll pending: %v", err)
	}
	// Confirming with the latest secret works.
	if _, err := svc.ConfirmTOTP(ctx, uid, codeFor(t, secret2)); err != nil {
		t.Fatalf("confirm after re-enroll: %v", err)
	}
}

func TestTOTPConfirmErrors(t *testing.T) {
	svc, email, _ := newTestService(t)
	ctx := context.Background()
	uid, _ := svc.userByEmailForTest(ctx, email)

	// Confirm with no pending enrollment → ErrTOTPState.
	if _, err := svc.ConfirmTOTP(ctx, uid, "123456"); !errors.Is(err, ErrTOTPState) {
		t.Fatalf("confirm w/o enrollment: want ErrTOTPState, got %v", err)
	}

	// Enroll, then confirm with a wrong code → ErrInvalidCredentials.
	if _, _, err := svc.EnrollTOTP(ctx, uid, "user@example.com"); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.ConfirmTOTP(ctx, uid, "000000"); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("confirm wrong code: want ErrInvalidCredentials, got %v", err)
	}
	// Still pending (not activated) after a failed confirm.
	if enabled, _, _ := svc.TOTPStatus(ctx, uid); enabled {
		t.Fatal("failed confirm must not activate the factor")
	}
}

func TestTOTPRecoveryCodeLogin(t *testing.T) {
	svc, email, password := newTestService(t)
	ctx := context.Background()
	uid, _ := svc.userByEmailForTest(ctx, email)
	_, codes := enrollConfirm(t, svc, ctx, uid)

	// A recovery code satisfies the second factor once.
	cookie, err := svc.Login(ctx, email, []byte(password), codes[0])
	if err != nil {
		t.Fatalf("login with recovery code: %v", err)
	}
	if cookie == "" {
		t.Fatal("empty cookie")
	}
	// Same recovery code cannot be reused.
	if _, err := svc.Login(ctx, email, []byte(password), codes[0]); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("recovery code reused: want ErrInvalidCredentials, got %v", err)
	}
	// A different, unused code still works.
	if _, err := svc.Login(ctx, email, []byte(password), codes[1]); err != nil {
		t.Fatalf("second recovery code: %v", err)
	}
	// Count dropped by exactly the two used codes.
	if _, n, _ := svc.TOTPStatus(ctx, uid); n != len(codes)-2 {
		t.Fatalf("recovery_remaining = %d, want %d", n, len(codes)-2)
	}
}

func TestTOTPRecoveryCodeNormalization(t *testing.T) {
	svc, email, password := newTestService(t)
	ctx := context.Background()
	uid, _ := svc.userByEmailForTest(ctx, email)
	_, codes := enrollConfirm(t, svc, ctx, uid)

	// The issued codes are grouped with dashes; verify a mangled form (spaces,
	// lowercase, extra dashes stripped) still matches after normalization.
	raw := codes[0]
	// Build a messy variant: lowercase + spaces instead of dashes + padding.
	messy := "  " + toLowerSpaces(raw) + "  "
	if _, err := svc.Login(ctx, email, []byte(password), messy); err != nil {
		t.Fatalf("normalized recovery code rejected: raw=%q messy=%q err=%v", raw, messy, err)
	}
}

// toLowerSpaces lowercases a code and replaces dashes with spaces.
func toLowerSpaces(s string) string {
	out := make([]rune, 0, len(s))
	for _, r := range s {
		switch {
		case r == '-':
			out = append(out, ' ')
		case r >= 'A' && r <= 'Z':
			out = append(out, r+('a'-'A'))
		default:
			out = append(out, r)
		}
	}
	return string(out)
}

func TestTOTPDisableRequiresValidFactor(t *testing.T) {
	svc, email, password := newTestService(t)
	ctx := context.Background()
	uid, _ := svc.userByEmailForTest(ctx, email)
	secret, _ := enrollConfirm(t, svc, ctx, uid)

	// Wrong code → refused, factor stays enabled.
	if err := svc.DisableTOTP(ctx, uid, "000000"); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("disable wrong code: want ErrInvalidCredentials, got %v", err)
	}
	if enabled, _, _ := svc.TOTPStatus(ctx, uid); !enabled {
		t.Fatal("factor wrongly disabled by a bad code")
	}

	// Valid code → disabled.
	if err := svc.DisableTOTP(ctx, uid, codeFor(t, secret)); err != nil {
		t.Fatalf("disable valid code: %v", err)
	}
	if enabled, _, _ := svc.TOTPStatus(ctx, uid); enabled {
		t.Fatal("factor still enabled after disable")
	}
	// Disabling again (nothing enrolled) → ErrTOTPState.
	if err := svc.DisableTOTP(ctx, uid, "000000"); !errors.Is(err, ErrTOTPState) {
		t.Fatalf("disable w/o factor: want ErrTOTPState, got %v", err)
	}

	// Login now ignores any code (2FA fully removed).
	if _, err := svc.Login(ctx, email, []byte(password), ""); err != nil {
		t.Fatalf("login after disable: %v", err)
	}
}

func TestTOTPRegenerateRecoveryCodes(t *testing.T) {
	svc, email, _ := newTestService(t)
	ctx := context.Background()
	uid, _ := svc.userByEmailForTest(ctx, email)
	secret, oldCodes := enrollConfirm(t, svc, ctx, uid)

	// Regenerating requires a valid factor.
	if _, err := svc.RegenerateRecoveryCodes(ctx, uid, "000000"); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("regen wrong code: want ErrInvalidCredentials, got %v", err)
	}

	newCodes, err := svc.RegenerateRecoveryCodes(ctx, uid, codeFor(t, secret))
	if err != nil {
		t.Fatalf("regen: %v", err)
	}
	if len(newCodes) != len(oldCodes) {
		t.Fatalf("new set size %d, want %d", len(newCodes), len(oldCodes))
	}
	// Old codes are invalidated.
	if ok, err := svc.verifySecondFactor(ctx, uid, oldCodes[0]); err != nil || ok {
		t.Fatalf("old recovery code still valid after regen: ok=%v err=%v", ok, err)
	}
	// New codes work.
	if ok, err := svc.verifySecondFactor(ctx, uid, newCodes[0]); err != nil || !ok {
		t.Fatalf("new recovery code invalid: ok=%v err=%v", ok, err)
	}
}

func TestLoginTOTPGate(t *testing.T) {
	svc, email, password := newTestService(t)
	ctx := context.Background()
	uid, _ := svc.userByEmailForTest(ctx, email)
	secret, _ := enrollConfirm(t, svc, ctx, uid)

	// Correct password + active TOTP + empty code → ErrTOTPRequired.
	if _, err := svc.Login(ctx, email, []byte(password), ""); !errors.Is(err, ErrTOTPRequired) {
		t.Fatalf("empty code: want ErrTOTPRequired, got %v", err)
	}
	// Correct password + wrong code → ErrInvalidCredentials.
	if _, err := svc.Login(ctx, email, []byte(password), "000000"); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("wrong code: want ErrInvalidCredentials, got %v", err)
	}
	// Wrong password + active TOTP → still ErrInvalidCredentials (no oracle:
	// must not reveal that the password was the only thing wrong).
	if _, err := svc.Login(ctx, email, []byte("nope"), codeFor(t, secret)); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("wrong password: want ErrInvalidCredentials, got %v", err)
	}
	// Correct password + valid code → session.
	cookie, err := svc.Login(ctx, email, []byte(password), codeFor(t, secret))
	if err != nil || cookie == "" {
		t.Fatalf("valid login: cookie=%q err=%v", cookie, err)
	}
	if _, err := svc.VerifySession(ctx, cookie); err != nil {
		t.Fatalf("minted session invalid: %v", err)
	}
}

func TestLoginWithoutTOTPIgnoresCode(t *testing.T) {
	svc, email, password := newTestService(t)
	ctx := context.Background()
	// A user with no TOTP logs in normally, and any supplied code is ignored.
	if _, err := svc.Login(ctx, email, []byte(password), "somerandomcode"); err != nil {
		t.Fatalf("login w/o TOTP but with a code: %v", err)
	}
}

func TestVerifySecondFactorNoFactor(t *testing.T) {
	svc, email, _ := newTestService(t)
	ctx := context.Background()
	uid, _ := svc.userByEmailForTest(ctx, email)
	// No enrollment at all → ErrTOTPState.
	if _, err := svc.verifySecondFactor(ctx, uid, "123456"); !errors.Is(err, ErrTOTPState) {
		t.Fatalf("verify w/o factor: want ErrTOTPState, got %v", err)
	}
	// Pending (enrolled, not activated) → still ErrTOTPState (not "enabled").
	if _, _, err := svc.EnrollTOTP(ctx, uid, "user@example.com"); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.verifySecondFactor(ctx, uid, "123456"); !errors.Is(err, ErrTOTPState) {
		t.Fatalf("verify pending factor: want ErrTOTPState, got %v", err)
	}
}
