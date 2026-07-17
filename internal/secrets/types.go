package secrets

import "fmt"

// allowedTypes is the single source of truth for secret display/handling types.
// Type is a hint (rendering/validation/icon), never a storage or crypto concern.
var allowedTypes = map[string]bool{
	"string": true, "password": true, "json": true,
	"ssh_key": true, "certificate": true, "note": true,
}

// normalizeType maps an empty type to the default "string"; non-empty values are
// returned unchanged (validation is a separate step).
func normalizeType(t string) string {
	if t == "" {
		return "string"
	}
	return t
}

// validateType returns ErrValidation for a non-allowed type. Empty is allowed
// (normalized to "string" at the write boundary).
func validateType(t string) error {
	if t == "" || allowedTypes[t] {
		return nil
	}
	return fmt.Errorf("%w: type %q is not a recognized secret type", ErrValidation, t)
}
