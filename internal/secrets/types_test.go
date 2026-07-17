package secrets

import "testing"

func TestNormalizeAndValidateType(t *testing.T) {
	for _, ok := range []string{"string", "password", "json", "ssh_key", "certificate", "note"} {
		if err := validateType(ok); err != nil {
			t.Errorf("%q should be valid: %v", ok, err)
		}
	}
	if normalizeType("") != "string" {
		t.Errorf("empty should normalize to string")
	}
	if err := validateType(""); err != nil {
		t.Errorf("empty type must be allowed (normalized at write boundary): %v", err)
	}
	if err := validateType("bogus"); err == nil {
		t.Errorf("bogus should be invalid")
	}
}
