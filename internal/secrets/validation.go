package secrets

import (
	"fmt"
	"regexp"
	"strings"
)

// keyRe restricts secret keys to environment-variable identifiers, since the
// flagship `kh run` injects them as env vars.
var keyRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// validateKey rejects keys that are not valid env-var identifiers. The key name
// is not secret (audit records key names, never values), so it may appear in
// the error.
func validateKey(k string) error {
	if !keyRe.MatchString(k) {
		return fmt.Errorf("%w: key %q is not a valid identifier", ErrValidation, k)
	}
	return nil
}

// validateSlug rejects empty/blank slugs.
func validateSlug(s string) error {
	if strings.TrimSpace(s) == "" {
		return fmt.Errorf("%w: slug must not be empty", ErrValidation)
	}
	return nil
}
