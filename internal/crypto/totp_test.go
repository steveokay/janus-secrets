package crypto

import (
	"testing"
	"time"
)

// RFC 6238 Appendix B test vectors (SHA-1, secret "12345678901234567890"),
// truncated to the 6 low digits of the published 8-digit values.
func TestTOTPRFC6238Vectors(t *testing.T) {
	secret := []byte("12345678901234567890")
	cases := []struct {
		unix int64
		code string // low 6 digits of the RFC's 8-digit value
	}{
		{59, "287082"},         // 94287082
		{1111111109, "081804"}, // 07081804
		{1111111111, "050471"}, // 14050471
		{1234567890, "005924"}, // 89005924
		{2000000000, "279037"}, // 69279037
	}
	for _, c := range cases {
		got := TOTPCodeAt(secret, time.Unix(c.unix, 0))
		if got != c.code {
			t.Errorf("TOTPCodeAt(%d) = %s, want %s", c.unix, got, c.code)
		}
		if !VerifyTOTP(secret, c.code, time.Unix(c.unix, 0), 1) {
			t.Errorf("VerifyTOTP failed for known-good code at %d", c.unix)
		}
	}
}

func TestVerifyTOTPWindowAndRejections(t *testing.T) {
	secret, err := GenerateTOTPSecret()
	if err != nil {
		t.Fatal(err)
	}
	if len(secret) != TOTPSecretBytes {
		t.Fatalf("secret length = %d, want %d", len(secret), TOTPSecretBytes)
	}
	now := time.Unix(1_700_000_000, 0)
	code := TOTPCodeAt(secret, now)

	// Accepted within the skew window (previous/next 30s step).
	if !VerifyTOTP(secret, code, now.Add(25*time.Second), 1) {
		t.Error("code should verify one step later within skew")
	}
	if !VerifyTOTP(secret, code, now.Add(-25*time.Second), 1) {
		t.Error("code should verify one step earlier within skew")
	}
	// Rejected outside the window.
	if VerifyTOTP(secret, code, now.Add(5*time.Minute), 1) {
		t.Error("stale code should be rejected outside the window")
	}
	// Wrong length and wrong value rejected.
	if VerifyTOTP(secret, "12345", now, 1) {
		t.Error("short code should be rejected")
	}
	if VerifyTOTP(secret, "000000", now.Add(90*time.Second), 0) && code == "000000" {
		t.Error("unexpected match")
	}
	// Counter clamp: a t before the epoch with skew must not panic and rejects.
	if VerifyTOTP(secret, code, time.Unix(0, 0), 1) && code != TOTPCodeAt(secret, time.Unix(0, 0)) {
		t.Error("near-epoch verification mishandled")
	}
}

func TestTOTPSecretWrapRoundTrip(t *testing.T) {
	k := NewKeyring()
	if err := k.Unseal(testKey(0xAA)); err != nil {
		t.Fatal(err)
	}
	secret, _ := GenerateTOTPSecret()

	ct, err := k.WrapTOTPSecret("user-1", secret)
	if err != nil {
		t.Fatalf("wrap: %v", err)
	}
	got, err := k.UnwrapTOTPSecret("user-1", ct)
	if err != nil {
		t.Fatalf("unwrap: %v", err)
	}
	if string(got) != string(secret) {
		t.Fatal("round-trip mismatch")
	}
	// AAD binds the user id.
	if _, err := k.UnwrapTOTPSecret("user-2", ct); err == nil {
		t.Fatal("expected error unwrapping under a different user id")
	}
	ct.Data[0] ^= 0xff
	if _, err := k.UnwrapTOTPSecret("user-1", ct); err == nil {
		t.Fatal("expected error unwrapping tampered ciphertext")
	}
}

func TestTOTPSecretWrapSealed(t *testing.T) {
	k := NewKeyring() // sealed
	if _, err := k.WrapTOTPSecret("u", []byte("x")); err != ErrSealed {
		t.Fatalf("want ErrSealed, got %v", err)
	}
	if _, err := k.UnwrapTOTPSecret("u", Ciphertext{Nonce: make([]byte, NonceSize)}); err != ErrSealed {
		t.Fatalf("want ErrSealed, got %v", err)
	}
}

// TestGenerateTOTPSecretRandFailure exercises the error path via the randReader seam.
func TestGenerateTOTPSecretRandFailure(t *testing.T) {
	orig := randReader
	randReader = failReader{}
	defer func() { randReader = orig }()
	if _, err := GenerateTOTPSecret(); err == nil {
		t.Fatal("expected error when the random source fails")
	}
}
