package crypto

import (
	"crypto/hmac"
	"crypto/sha1" // #nosec G505 -- RFC 6238 TOTP is defined over HMAC-SHA1; this is the interop standard, not a security hash of secret data
	"crypto/subtle"
	"encoding/binary"
	"io"
	"strconv"
	"time"
)

// TOTP (RFC 6238) is a construction over the stdlib HMAC-SHA1 primitive — the
// same "build protocols over stdlib primitives" approach as the envelope
// encryption and audit hash chain. No third-party crypto dependency is used.
// HMAC-SHA1 is mandated by RFC 6238 for authenticator-app interop; it is not a
// security hash over secret storage.

const (
	totpDigits = 6
	totpStep   = 30 * time.Second
	// TOTPSecretBytes is the shared-secret length (160-bit, per RFC 4226 §4).
	TOTPSecretBytes = 20
)

// GenerateTOTPSecret returns a fresh random 160-bit TOTP shared secret.
func GenerateTOTPSecret() ([]byte, error) {
	secret := make([]byte, TOTPSecretBytes)
	if _, err := io.ReadFull(randReader, secret); err != nil {
		return nil, err
	}
	return secret, nil
}

// hotp computes the RFC 4226 HOTP value for a counter.
func hotp(secret []byte, counter uint64) string {
	var buf [8]byte
	binary.BigEndian.PutUint64(buf[:], counter)
	mac := hmac.New(sha1.New, secret)
	mac.Write(buf[:])
	sum := mac.Sum(nil)
	offset := sum[len(sum)-1] & 0x0f
	bin := (uint32(sum[offset])&0x7f)<<24 |
		uint32(sum[offset+1])<<16 |
		uint32(sum[offset+2])<<8 |
		uint32(sum[offset+3])
	code := bin % 1_000_000 // 10^totpDigits
	return leftPad(strconv.FormatUint(uint64(code), 10), totpDigits)
}

func leftPad(s string, n int) string {
	for len(s) < n {
		s = "0" + s
	}
	return s
}

// TOTPCodeAt returns the TOTP code for a secret at time t. Pre-epoch times
// clamp to counter 0, mirroring VerifyTOTP's negative-step guard (G115).
func TOTPCodeAt(secret []byte, t time.Time) string {
	counter := t.Unix() / int64(totpStep.Seconds())
	if counter < 0 {
		counter = 0
	}
	return hotp(secret, uint64(counter))
}

// VerifyTOTP reports whether code is valid for secret at time t, allowing a
// ±skew window of time steps (skew=1 → ±30s tolerance for clock drift). The
// comparison is constant-time and length-checked.
func VerifyTOTP(secret []byte, code string, t time.Time, skew int) bool {
	if len(code) != totpDigits {
		return false
	}
	center := int64(t.Unix()) / int64(totpStep.Seconds())
	var ok bool
	// Check every window unconditionally (no early return) to avoid leaking
	// which step matched via timing.
	for i := -skew; i <= skew; i++ {
		c := center + int64(i)
		if c < 0 {
			continue
		}
		want := hotp(secret, uint64(c))
		if subtle.ConstantTimeCompare([]byte(want), []byte(code)) == 1 {
			ok = true
		}
	}
	return ok
}
