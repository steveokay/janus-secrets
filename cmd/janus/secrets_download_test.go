package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
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
