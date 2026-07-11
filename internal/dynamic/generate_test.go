package dynamic

import "testing"

func TestGenerateUsernameIsIdentifierSafe(t *testing.T) {
	cases := []string{"readonly", "read-write!!", "", "ADMIN", "a_very_long_role_name_exceeding_limits_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}
	for _, name := range cases {
		u, err := generateUsername(name)
		if err != nil {
			t.Fatalf("generateUsername(%q): %v", name, err)
		}
		if !identRe.MatchString(u) {
			t.Fatalf("username %q not identifier-safe", u)
		}
		if len(u) > 63 {
			t.Fatalf("username %q exceeds 63 bytes", u)
		}
	}
	a, _ := generateUsername("x")
	b, _ := generateUsername("x")
	if a == b {
		t.Fatal("expected distinct usernames")
	}
}

func TestGeneratePasswordAlphabet(t *testing.T) {
	p, err := generatePassword(40)
	if err != nil || len(p) != 40 {
		t.Fatalf("generatePassword: %q err=%v", p, err)
	}
	for _, c := range p {
		if (c < 'A' || c > 'Z') && (c < 'a' || c > 'z') && (c < '0' || c > '9') {
			t.Fatalf("password has non-alphanumeric char %q", c)
		}
	}
}
