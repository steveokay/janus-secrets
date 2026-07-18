package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func downloadServer() *httptest.Server {
	mux := http.NewServeMux()
	addResolveRoutes(mux)
	mux.HandleFunc("/v1/configs/c1/secrets", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("reveal") != "true" {
			http.Error(w, "expected reveal", 400)
			return
		}
		_, _ = w.Write([]byte(`{"version":3,"secrets":{"API_KEY":"s3cr3t","DB":"x y"}}`))
	})
	return httptest.NewServer(mux)
}

func TestDownloadStdout(t *testing.T) {
	t.Setenv("JANUS_CONFIG_DIR", t.TempDir())
	t.Setenv("JANUS_ADDR", "")
	t.Setenv("JANUS_TOKEN", "")
	ts := downloadServer()
	defer ts.Close()

	out, _, err := runCmd(t, newSecretsCmd(), "download", "--format", "env",
		"--address", ts.URL, "--project", "acme", "--env", "dev", "--config", "dev")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "API_KEY=s3cr3t") || !strings.Contains(out, "DB='x y'") {
		t.Fatalf("download stdout = %q", out)
	}
}

func TestDownloadOutputRequiresPlain(t *testing.T) {
	t.Setenv("JANUS_CONFIG_DIR", t.TempDir())
	t.Setenv("JANUS_ADDR", "")
	t.Setenv("JANUS_TOKEN", "")
	ts := downloadServer()
	defer ts.Close()
	target := filepath.Join(t.TempDir(), "out.env")

	_, _, err := runCmd(t, newSecretsCmd(), "download", "--format", "env", "--output", target,
		"--address", ts.URL, "--project", "acme", "--env", "dev", "--config", "dev")
	if err == nil || !strings.Contains(err.Error(), "--plain") {
		t.Fatalf("want --plain refusal, got %v", err)
	}
	if _, statErr := os.Stat(target); !os.IsNotExist(statErr) {
		t.Fatal("file must not be written without --plain")
	}

	_, _, err = runCmd(t, newSecretsCmd(), "download", "--format", "env", "--output", target, "--plain",
		"--address", ts.URL, "--project", "acme", "--env", "dev", "--config", "dev")
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(target)
	if err != nil || !strings.Contains(string(b), "API_KEY=s3cr3t") {
		t.Fatalf("file contents: %s (%v)", b, err)
	}
}

// TestDownloadOutputForces0600OnPreexistingFile guards the High finding from the
// M9 adversarial review: os.WriteFile only applies perm on create, so writing over
// a pre-existing looser-mode file would have leaked secrets at 0644. writeSecretFile
// (temp+O_EXCL 0600+rename) must yield 0600 regardless of the prior file.
func TestDownloadOutputForces0600OnPreexistingFile(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX file modes are cosmetic on Windows")
	}
	t.Setenv("JANUS_CONFIG_DIR", t.TempDir())
	t.Setenv("JANUS_ADDR", "")
	t.Setenv("JANUS_TOKEN", "")
	ts := downloadServer()
	defer ts.Close()

	target := filepath.Join(t.TempDir(), "preexisting.env")
	// Pre-create the target world/group-readable, as a committed placeholder might be.
	if err := os.WriteFile(target, []byte("PLACEHOLDER=1\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, _, err := runCmd(t, newSecretsCmd(), "download", "--format", "env", "--output", target, "--plain",
		"--address", ts.URL, "--project", "acme", "--env", "dev", "--config", "dev")
	if err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(target)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Fatalf("download over pre-existing file left mode %v, want 0600", fi.Mode().Perm())
	}
	b, err := os.ReadFile(target)
	if err != nil || !strings.Contains(string(b), "API_KEY=s3cr3t") || strings.Contains(string(b), "PLACEHOLDER") {
		t.Fatalf("file should be replaced with secrets, got: %s (%v)", b, err)
	}
}

func TestMaterializeSecrets_WritesOnePerKey(t *testing.T) {
	dir := t.TempDir()
	err := materializeSecrets(dir, map[string]string{
		"vigil-cloud.secrets.backup.txt": "line1\nline2\n",
		"API_KEY":                        "v1",
	})
	if err != nil {
		t.Fatalf("materialize: %v", err)
	}
	b, err := os.ReadFile(filepath.Join(dir, "vigil-cloud.secrets.backup.txt"))
	if err != nil || string(b) != "line1\nline2\n" {
		t.Errorf("file contents = %q err=%v", b, err)
	}
}

func TestMaterializeSecrets_RefusesTraversal(t *testing.T) {
	dir := t.TempDir()
	for _, bad := range []string{"..", "../escape", "a/b", "a\\b", "."} {
		if err := materializeSecrets(dir, map[string]string{bad: "x"}); err == nil {
			t.Errorf("materialize key %q should refuse traversal", bad)
		}
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(dir), "escape")); err == nil {
		t.Errorf("a file escaped the output dir")
	}
}
