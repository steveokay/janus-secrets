package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSecretsDiff(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/projects", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"projects": []map[string]string{{"id": "p1", "slug": "acme"}}})
	})
	mux.HandleFunc("GET /v1/projects/p1/environments", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"environments": []map[string]string{{"id": "e1", "slug": "prod"}}})
	})
	mux.HandleFunc("GET /v1/projects/p1/environments/e1/configs", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"configs": []map[string]any{{"id": "c1", "name": "prod"}}})
	})
	var gotQuery string
	mux.HandleFunc("GET /v1/configs/c1/versions/diff", func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		_ = json.NewEncoder(w).Encode(map[string]any{"a": 3, "b": 4, "added": []string{"NEW_KEY"}, "changed": []string{"DB_URL"}, "removed": []string{}})
	})
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	out, err := runCLI(t, "", "secrets", "diff", "3", "4", "--address", ts.URL, "--token", "janus_svc_test", "--project", "acme", "--env", "prod", "--config", "prod")
	if err != nil {
		t.Fatalf("diff: %v", err)
	}
	if !strings.Contains(gotQuery, "a=3") || !strings.Contains(gotQuery, "b=4") {
		t.Fatalf("query = %q", gotQuery)
	}
	if !strings.Contains(out, "NEW_KEY") || !strings.Contains(out, "DB_URL") {
		t.Fatalf("output missing keys: %q", out)
	}
}
