package secrets

import (
	"fmt"
	"regexp"
	"strings"
)

// keyRe allows filename-style secret keys: letters, digits, and . _ - . Keys are
// NOT restricted to env-var identifiers because a secret may be a file (keyed by
// its filename) that is materialized to disk rather than injected via `janus run`.
// Env-var injection is gated separately at run time (cmd/janus isEnvVarName).
var keyRe = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)

// validateKey rejects keys that are not filename-safe. The key name is not secret
// (audit records key names, never values), so it may appear in the error. Rejects
// "."/".." and path separators so a key can never traverse when materialized to a
// file, and caps length at 255 (filesystem limit).
func validateKey(k string) error {
	if k == "" || len(k) > 255 || k == "." || k == ".." ||
		strings.ContainsAny(k, `/\`) || !keyRe.MatchString(k) {
		return fmt.Errorf("%w: key %q must be letters, digits, and . _ - (not '.'/'..' or path separators, ≤255)", ErrValidation, k)
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
