package rotation

import "testing"

func TestGeneratePassword(t *testing.T) {
	got, err := generatePassword(32)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 32 {
		t.Fatalf("len = %d, want 32", len(got))
	}
	for _, c := range got {
		if !((c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9')) {
			t.Fatalf("unsafe char %q in generated password", c)
		}
	}
	other, _ := generatePassword(32)
	if got == other {
		t.Fatal("two generations collided")
	}
	if _, err := generatePassword(0); err == nil {
		t.Fatal("want error for non-positive length")
	}
}
