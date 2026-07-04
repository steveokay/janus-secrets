package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"strconv"
	"strings"

	"golang.org/x/crypto/argon2"
)

// OWASP-recommended Argon2id parameters (m=19 MiB, t=2, p=1). The PHC string
// is self-describing, so raising these later re-hashes lazily at next login
// via the needsRehash flag.
const (
	argonTime    uint32 = 2
	argonMemory  uint32 = 19 * 1024 // KiB
	argonThreads uint8  = 1
	argonKeyLen  uint32 = 32
	saltLen             = 16
)

// b64 is the PHC-standard base64 (std alphabet, no padding).
var b64 = base64.RawStdEncoding

// HashPassword returns an Argon2id PHC string for pw. The caller owns wiping
// pw afterwards.
func HashPassword(pw []byte) (string, error) {
	return hashWithParams(pw, argonTime, argonMemory, argonThreads)
}

func hashWithParams(pw []byte, time_, memory uint32, threads uint8) (string, error) {
	salt := make([]byte, saltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	key := argon2.IDKey(pw, salt, time_, memory, threads, argonKeyLen)
	return fmt.Sprintf("$argon2id$v=19$m=%d,t=%d,p=%d$%s$%s",
		memory, time_, threads, b64.EncodeToString(salt), b64.EncodeToString(key)), nil
}

// VerifyPassword checks pw against a PHC hash. needsRehash reports that the
// hash was minted at weaker-than-current parameters and should be re-hashed
// on this (successful) login. Malformed hashes error without revealing why.
func VerifyPassword(phc string, pw []byte) (ok, needsRehash bool, err error) {
	parts := strings.Split(phc, "$")
	// "" / "argon2id" / "v=19" / "m=..,t=..,p=.." / salt / hash
	if len(parts) != 6 || parts[0] != "" || parts[1] != "argon2id" || parts[2] != "v=19" {
		return false, false, fmt.Errorf("%w: malformed password hash", ErrValidation)
	}
	var m, t uint32
	var p uint8
	if !parseArgonParams(parts[3], &m, &t, &p) {
		return false, false, fmt.Errorf("%w: malformed password hash", ErrValidation)
	}
	salt, err := b64.DecodeString(parts[4])
	if err != nil {
		return false, false, fmt.Errorf("%w: malformed password hash", ErrValidation)
	}
	if len(salt) < 8 || len(salt) > 64 {
		return false, false, fmt.Errorf("%w: malformed password hash", ErrValidation)
	}
	want, err := b64.DecodeString(parts[5])
	if err != nil || len(want) < 16 || len(want) > 64 {
		return false, false, fmt.Errorf("%w: malformed password hash", ErrValidation)
	}
	// len(want) is bounded to [16,64] above, so the conversion cannot overflow.
	keyLen := uint32(len(want)) // #nosec G115 -- want length validated to [16,64]
	got := argon2.IDKey(pw, salt, t, m, p, keyLen)
	if subtle.ConstantTimeCompare(got, want) != 1 {
		return false, false, nil
	}
	needsRehash = m < argonMemory || t < argonTime || p < argonThreads
	return true, needsRehash, nil
}

// parseArgonParams strictly parses "m=<n>,t=<n>,p=<n>" and bounds-checks the
// values. Bounds are deliberately tight — every stored hash is minted by
// HashPassword, so anything outside them is corruption or tampering, and
// unvalidated values reach argon2.IDKey, which panics on t=0/p=0 and
// allocates m KiB (a crafted huge m is an OOM). Widen only when the minting
// defaults are raised.
func parseArgonParams(s string, m, t *uint32, p *uint8) bool {
	fields := strings.Split(s, ",")
	if len(fields) != 3 ||
		!strings.HasPrefix(fields[0], "m=") ||
		!strings.HasPrefix(fields[1], "t=") ||
		!strings.HasPrefix(fields[2], "p=") {
		return false
	}
	mv, err1 := strconv.ParseUint(fields[0][2:], 10, 32)
	tv, err2 := strconv.ParseUint(fields[1][2:], 10, 32)
	pv, err3 := strconv.ParseUint(fields[2][2:], 10, 8)
	if err1 != nil || err2 != nil || err3 != nil {
		return false
	}
	// 8 MiB..256 MiB, 1..16 passes, 1..8 lanes.
	if mv < 8*1024 || mv > 256*1024 || tv < 1 || tv > 16 || pv < 1 || pv > 8 {
		return false
	}
	*m, *t, *p = uint32(mv), uint32(tv), uint8(pv)
	return true
}
