package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestParseSetArgs(t *testing.T) {
	// Inline KEY=VALUE pairs.
	ch, err := parseSetArgs([]string{"A=1", "B=2"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(ch) != 2 || ch[0].Key != "A" || ch[0].Value != "1" || ch[1].Key != "B" {
		t.Fatalf("pairs: %+v", ch)
	}
	// Single KEY with value from stdin.
	ch, err = parseSetArgs([]string{"TOKEN"}, strings.NewReader("from-stdin\n"))
	if err != nil {
		t.Fatal(err)
	}
	if len(ch) != 1 || ch[0].Key != "TOKEN" || ch[0].Value != "from-stdin" {
		t.Fatalf("stdin value: %+v", ch)
	}
	// KEY VALUE positional.
	ch, err = parseSetArgs([]string{"K", "V"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(ch) != 1 || ch[0].Key != "K" || ch[0].Value != "V" {
		t.Fatalf("positional: %+v", ch)
	}
}

func TestSecretsSetBatchesOneVersion(t *testing.T) {
	t.Setenv("JANUS_CONFIG_DIR", t.TempDir())
	t.Setenv("JANUS_ADDR", "")
	t.Setenv("JANUS_TOKEN", "")

	var gotBody map[string]any
	mux := http.NewServeMux()
	addResolveRoutes(mux) // helper defined in secrets_write_cmd_test.go below
	mux.HandleFunc("/v1/configs/c1/secrets", func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b, &gotBody)
		_, _ = w.Write([]byte(`{"version":4,"id":"cv4","created_at":"2026-07-05T00:00:00Z"}`))
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	_, _, err := runCmd(t, newSecretsCmd(), "set", "A=1", "B=2",
		"--address", ts.URL, "--project", "acme", "--env", "dev", "--config", "dev", "--message", "batch")
	if err != nil {
		t.Fatal(err)
	}
	changes, _ := gotBody["changes"].([]any)
	if len(changes) != 2 {
		t.Fatalf("want 2 changes in one request, got %v", gotBody)
	}
	if gotBody["message"] != "batch" {
		t.Fatalf("message not sent: %v", gotBody)
	}
}

func addResolveRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/v1/projects", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"projects":[{"id":"p1","slug":"acme"}]}`))
	})
	mux.HandleFunc("/v1/projects/p1/environments", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"environments":[{"id":"e1","slug":"dev"}]}`))
	})
	mux.HandleFunc("/v1/projects/p1/environments/e1/configs", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"configs":[{"id":"c1","name":"dev"}]}`))
	})
}
