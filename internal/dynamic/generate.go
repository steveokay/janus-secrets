package dynamic

import (
	"crypto/rand"
	"errors"
	"regexp"
	"strings"
)

const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789"
const lowerAlnum = "abcdefghijklmnopqrstuvwxyz0123456789"

// identRe restricts generated usernames to a plain SQL identifier (<=63 bytes).
var identRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,62}$`)

var prefixStrip = regexp.MustCompile(`[^a-z0-9_]`)

// randChars returns n cryptographically-random characters drawn from alpha. The
// modulo mapping introduces a negligible bias (256 is not a multiple of 62/36);
// at these lengths the entropy loss is immaterial (~0.003 bits/char) and this
// matches the rotation engine's construction.
func randChars(n int, alpha string) (string, error) {
	if n <= 0 {
		return "", errors.New("dynamic: length must be positive")
	}
	if len(alpha) == 0 {
		return "", errors.New("dynamic: empty alphabet")
	}
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	for i := range buf {
		buf[i] = alpha[int(buf[i])%len(alpha)]
	}
	return string(buf), nil
}

func generatePassword(n int) (string, error) { return randChars(n, alphabet) }

// generateUsername builds "janus_<prefix>_<random>", identifier-safe and <=63 bytes.
func generateUsername(roleName string) (string, error) {
	prefix := prefixStrip.ReplaceAllString(strings.ToLower(roleName), "")
	if prefix == "" {
		prefix = "role"
	}
	const suffixLen = 12
	maxPrefix := 63 - len("janus_") - 1 - suffixLen
	if len(prefix) > maxPrefix {
		prefix = prefix[:maxPrefix]
	}
	suffix, err := randChars(suffixLen, lowerAlnum)
	if err != nil {
		return "", err
	}
	u := "janus_" + prefix + "_" + suffix
	if !identRe.MatchString(u) {
		return "", errors.New("dynamic: generated username failed identifier check")
	}
	return u, nil
}
