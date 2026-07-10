package rotation

import (
	"crypto/rand"
	"errors"
)

// alphabet is intentionally alphanumeric only: the generated value is
// interpolated into an ALTER ROLE ... PASSWORD literal, and excluding quotes /
// backslashes removes any SQL-literal escaping hazard at the source (defensive
// quoting is still applied in the postgres rotator).
const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789"

// generatePassword returns n cryptographically-random alphanumeric characters.
func generatePassword(n int) (string, error) {
	if n <= 0 {
		return "", errors.New("rotation: password length must be positive")
	}
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	for i := range buf {
		buf[i] = alphabet[int(buf[i])%len(alphabet)]
	}
	return string(buf), nil
}
