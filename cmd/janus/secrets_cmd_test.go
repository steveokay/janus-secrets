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
		_, _ = w.Write([]byte(`{"secrets":{"API_KEY":{"value_version":2,"created_at":"2026-07-05T00:00:00Z","origin":"own","type":"json"}}}`))
	})
	mux.HandleFunc("/v1/configs/c1/secrets/API_KEY", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("raw") == "true" {
			_, _ = w.Write([]byte(`{"key":"API_KEY","value":"RAWVAL"}`))
			return
		}
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

func TestSecretsGetRawFlag(t *testing.T) {
	t.Setenv("JANUS_CONFIG_DIR", t.TempDir())
	t.Setenv("JANUS_ADDR", "")
	t.Setenv("JANUS_TOKEN", "")
	ts := secretsServer(t)
	defer ts.Close()

	out, _, err := runCmd(t, newSecretsCmd(), "get", "API_KEY", "--raw",
		"--address", ts.URL, "--project", "acme", "--env", "dev", "--config", "dev")
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimRight(out, "\n") != "RAWVAL" {
		t.Fatalf("get --raw output = %q, want RAWVAL (raw=true not forwarded?)", out)
	}
}

func TestSecretsListShowsOrigin(t *testing.T) {
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
	if !strings.Contains(out, "ORIGIN") || !strings.Contains(out, "own") {
		t.Fatalf("list output missing ORIGIN column / origin value: %q", out)
	}
}

func TestSecretsListShowsTypeColumn(t *testing.T) {
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
	if !strings.Contains(out, "TYPE") {
		t.Fatalf("list output missing TYPE column header: %q", out)
	}
	if !strings.Contains(out, "json") {
		t.Fatalf("list output missing json type value for API_KEY: %q", out)
	}
}

func TestSecretsListTypeDefaultsToStringWhenEmpty(t *testing.T) {
	t.Setenv("JANUS_CONFIG_DIR", t.TempDir())
	t.Setenv("JANUS_ADDR", "")
	t.Setenv("JANUS_TOKEN", "")

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
		_, _ = w.Write([]byte(`{"secrets":{"LEGACY":{"value_version":1,"created_at":"2026-07-05T00:00:00Z","origin":"own","type":""}}}`))
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	out, _, err := runCmd(t, newSecretsCmd(), "list",
		"--address", ts.URL, "--project", "acme", "--env", "dev", "--config", "dev")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "LEGACY") {
		t.Fatalf("list output missing LEGACY row: %q", out)
	}
	found := false
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, "LEGACY") && strings.Contains(line, "string") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected empty type to render as 'string' in list row: %q", out)
	}
}
