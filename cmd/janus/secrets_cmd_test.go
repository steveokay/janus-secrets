package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// secretsServer serves resolve routes plus masked list and single reveal for c1.
func secretsServer(t *testing.T) *httptest.Server {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/projects", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"projects":[{"id":"p1","slug":"acme"}]}`))
	})
	mux.HandleFunc("/v1/projects/p1/environments", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"environments":[{"id":"e1","slug":"dev"}]}`))
	})
	mux.HandleFunc("/v1/projects/p1/environments/e1/configs", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"configs":[{"id":"c1","name":"dev"}]}`))
	})
	mux.HandleFunc("/v1/configs/c1/secrets", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"version":3,"secrets":{"API_KEY":{"value_version":2,"created_at":"2026-07-05T00:00:00Z"}}}`))
	})
	mux.HandleFunc("/v1/configs/c1/secrets/API_KEY", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"key":"API_KEY","value":"s3cr3t","value_version":2}`))
	})
	return httptest.NewServer(mux)
}

func runCmd(t *testing.T, c *cobra.Command, args ...string) (string, string, error) {
	t.Helper()
	var out, errb strings.Builder
	c.SetArgs(args)
	c.SetOut(&out)
	c.SetErr(&errb)
	err := c.Execute()
	return out.String(), errb.String(), err
}

func TestSecretsListMasked(t *testing.T) {
	t.Setenv("JANUS_CONFIG_DIR", t.TempDir())
	t.Setenv("JANUS_ADDR", "")
	t.Setenv("JANUS_TOKEN", "")
	ts := secretsServer(t)
	defer ts.Close()

	out, _, err := runCmd(t, newSecretsCmd(), "list",
		"--address", ts.URL, "--project", "acme", "--env", "dev", "--config", "dev")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "API_KEY") || strings.Contains(out, "s3cr3t") {
		t.Fatalf("masked list output wrong: %q", out)
	}
}

func TestSecretsGetPrintsRawValue(t *testing.T) {
	t.Setenv("JANUS_CONFIG_DIR", t.TempDir())
	t.Setenv("JANUS_ADDR", "")
	t.Setenv("JANUS_TOKEN", "")
	ts := secretsServer(t)
	defer ts.Close()

	out, _, err := runCmd(t, newSecretsCmd(), "get", "API_KEY",
		"--address", ts.URL, "--project", "acme", "--env", "dev", "--config", "dev")
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimRight(out, "\n") != "s3cr3t" {
		t.Fatalf("get output = %q, want s3cr3t", out)
	}
}
