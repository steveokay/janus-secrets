package crypto

import (
	"context"
	"encoding/base64"
	"encoding/hex"
	"regexp"
	"strings"
	"testing"
)

// TestNoSecretsInErrorMessages drives a broad set of error paths with known
// key material and plaintext, then asserts none of it — raw, hex, or base64 —
// appears in any error message. This seeds the project-wide leak test
// required by CLAUDE.md.
func TestNoSecretsInErrorMessages(t *testing.T) {
	ctx := context.Background()
	key := testKey(0x5A)
	plaintext := []byte("SUPER-SECRET-VALUE-DO-NOT-LEAK")

	var errs []error
	collect := func(err error) {
		if err != nil {
			errs = append(errs, err)
		}
	}

	// AEAD failures around real key material and plaintext.
	ct, err := Encrypt(key, plaintext, []byte("aad"))
	if err != nil {
		t.Fatal(err)
	}
	_, err = Decrypt(testKey(0x5B), ct, []byte("aad"))
	collect(err)
	_, err = Decrypt(key, ct, []byte("wrong-aad"))
	collect(err)
	_, err = Decrypt(key[:16], ct, []byte("aad"))
	collect(err)
	_, err = ParseCiphertext(plaintext)
	collect(err)

	// Wrapping failures.
	_, err = WrapKey(key, plaintext, nil)
	collect(err)
	_, err = UnwrapKey(testKey(0x5B), ct, []byte("aad"))
	collect(err)

	// Keyring failures.
	kr := NewKeyring()
	_, err = kr.WrapProjectKEK(key, "p")
	collect(err)
	collect(kr.Unseal(plaintext))

	// Unseal failures.
	collect(verifyKCV(key, plaintext))
	su := NewShamirUnsealer(fileStore(t), 5, 3)
	res, err := su.Init(ctx)
	if err != nil {
		t.Fatal(err)
	}
	_, err = su.SubmitShare(ctx, res.Shares[0])
	collect(err)
	_, err = su.SubmitShare(ctx, res.Shares[0]) // duplicate
	collect(err)
	_, err = su.Unseal(ctx) // not enough
	collect(err)

	if len(errs) < 10 {
		t.Fatalf("expected to collect at least 10 errors, got %d — error paths lost?", len(errs))
	}

	forbidden := []string{
		string(plaintext),
		hex.EncodeToString(key),
		base64.StdEncoding.EncodeToString(key),
		hex.EncodeToString(res.Shares[0]),
	}
	// Any long token in an error message is suspicious even if it isn't a
	// known secret: sentinel messages are short English phrases.
	longToken := regexp.MustCompile(`[A-Za-z0-9+/=_-]{24,}`)

	for _, e := range errs {
		msg := e.Error()
		for _, f := range forbidden {
			if strings.Contains(msg, f) {
				t.Errorf("error message contains secret material: %q", msg)
			}
		}
		if longToken.MatchString(msg) {
			t.Errorf("error message contains suspicious long token: %q", msg)
		}
	}
}

// TestSentinelMessagesAreClean asserts every exported sentinel is a short,
// fixed English phrase with no interpolation.
func TestSentinelMessagesAreClean(t *testing.T) {
	sentinels := []error{
		ErrSealed, ErrAlreadyUnsealed, ErrInvalidKeySize, ErrDecryptFailed,
		ErrInvalidShare, ErrDuplicateShare, ErrNotEnoughShares,
		ErrKeyCheckFailed, ErrNoSealConfig, ErrAlreadyInitialized,
		ErrInvalidSealConfig,
	}
	re := regexp.MustCompile(`^[a-z][a-z0-9 -]{5,60}$`)
	for _, s := range sentinels {
		if !re.MatchString(s.Error()) {
			t.Errorf("sentinel message %q does not look like a fixed phrase", s.Error())
		}
	}
}
