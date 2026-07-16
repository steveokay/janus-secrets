package meta

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// repoRoot walks up from this test file to the module root (the dir with go.mod).
func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 10; i++ {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		dir = filepath.Dir(dir)
	}
	t.Fatal("go.mod not found walking up from test dir")
	return ""
}

func TestApacheLicensePresent(t *testing.T) {
	root := repoRoot(t)
	b, err := os.ReadFile(filepath.Join(root, "LICENSE"))
	if err != nil {
		t.Fatalf("LICENSE missing: %v", err)
	}
	if !strings.Contains(string(b), "Apache License") || !strings.Contains(string(b), "Version 2.0") {
		t.Fatal("LICENSE is not Apache-2.0")
	}
	if _, err := os.Stat(filepath.Join(root, "NOTICE")); err != nil {
		t.Fatalf("NOTICE missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "internal", "crypto", "shamir", "LICENSE")); err != nil {
		t.Fatalf("vendored shamir LICENSE removed: %v", err)
	}
	readme, err := os.ReadFile(filepath.Join(root, "README.md"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(readme), "Not yet chosen") {
		t.Fatal("README still says the license is 'Not yet chosen'")
	}
}
