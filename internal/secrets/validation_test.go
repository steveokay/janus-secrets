package secrets

import (
	"errors"
	"strings"
	"testing"
)

func TestValidateKey_FilenameCharset(t *testing.T) {
	ok := []string{"API_KEY", "vigil-cloud.secrets.backup.txt", ".secrets", "a", "A_B-1.2"}
	for _, k := range ok {
		if err := validateKey(k); err != nil {
			t.Errorf("validateKey(%q) = %v, want nil", k, err)
		}
	}
	bad := []string{"", ".", "..", "a/b", "a\\b", "a b", "a$b", strings.Repeat("x", 256)}
	for _, k := range bad {
		if err := validateKey(k); !errors.Is(err, ErrValidation) {
			t.Errorf("validateKey(%q) = %v, want ErrValidation", k, err)
		}
	}
}
