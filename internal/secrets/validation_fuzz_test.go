package secrets

import (
	"path/filepath"
	"strings"
	"testing"
)

// FuzzValidateKey asserts the SECURITY invariant behind validateKey rather than
// merely that it doesn't panic: every key it accepts must be safe to materialize
// as a file. `janus secrets download --format files` writes one file per key at
// <dir>/<key>, so an accepted key that could traverse ("..", "a/b", an absolute
// path) would be a path-traversal primitive.
func FuzzValidateKey(f *testing.F) {
	for _, seed := range []string{
		"API_KEY", "config.prod.json", "a-b_c.1", "x",
		"", ".", "..", "../etc/passwd", "a/b", `a\b`, "/abs", `C:\win`,
		"key with space", "kéy", "a\x00b", "a\nb", strings.Repeat("k", 255),
		strings.Repeat("k", 256), "....", `..\..`, "./x", "..;/x", "%2e%2e",
	} {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, key string) {
		if err := validateKey(key); err != nil {
			return // rejected: nothing to prove
		}

		// --- Accepted. Everything below must hold. ---
		if key == "" {
			t.Fatal("accepted an empty key")
		}
		if len(key) > 255 {
			t.Fatalf("accepted an over-long key (%d bytes)", len(key))
		}
		if key == "." || key == ".." {
			t.Fatalf("accepted the traversal-significant key %q", key)
		}
		if strings.ContainsAny(key, `/\`) {
			t.Fatalf("accepted a key containing a path separator: %q", key)
		}
		// The key must be exactly one path element, unchanged by cleaning.
		if got := filepath.Base(key); got != key {
			t.Fatalf("filepath.Base(%q) = %q — key is not a single path element", key, got)
		}
		if got := filepath.Clean(key); got != key {
			t.Fatalf("filepath.Clean(%q) = %q — key is not already clean", key, got)
		}
		if filepath.IsAbs(key) {
			t.Fatalf("accepted an absolute path: %q", key)
		}
		// Joining into a directory must stay inside that directory.
		joined := filepath.ToSlash(filepath.Clean(filepath.Join("/out", key)))
		if !strings.HasPrefix(joined, "/out/") {
			t.Fatalf("key %q escapes the output dir: %q", key, joined)
		}
		// No control characters or NUL (filename hazards / log-injection).
		for i := 0; i < len(key); i++ {
			if key[i] < 0x20 || key[i] == 0x7f {
				t.Fatalf("accepted a key with control byte 0x%02x: %q", key[i], key)
			}
		}
	})
}
