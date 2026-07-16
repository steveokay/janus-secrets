package version

import "testing"

func TestDefaults(t *testing.T) {
	if Version == "" {
		t.Fatal("Version must have a non-empty default")
	}
	if String() == "" {
		t.Fatal("String() must render something")
	}
}

func TestStringFormat(t *testing.T) {
	// Restore the package globals so test order can't leak the mutated values.
	origV, origC, origD := Version, Commit, Date
	defer func() { Version, Commit, Date = origV, origC, origD }()
	Version, Commit, Date = "1.2.3", "abc1234", "2026-07-16T00:00:00Z"
	got := String()
	for _, want := range []string{"1.2.3", "abc1234", "2026-07-16T00:00:00Z"} {
		if !contains(got, want) {
			t.Fatalf("String()=%q missing %q", got, want)
		}
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || indexOf(s, sub) >= 0)
}
func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
