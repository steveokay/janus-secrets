package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func stubTokens(t *testing.T) (*httptest.Server, *map[string]any) {
	t.Helper()
	var mintBody map[string]any
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
	mux.HandleFunc("POST /v1/tokens", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&mintBody)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"token": "janus_svc_SECRET", "id": "tok1", "name": "ci",
			"scope": map[string]string{"kind": "config", "id": "c1"}, "access": "readwrite",
		})
	})
	mux.HandleFunc("GET /v1/tokens", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"tokens": []map[string]any{
			{"id": "tok1", "name": "ci", "scope_kind": "config", "scope_id": "c1", "access": "readwrite"},
		}})
	})
	mux.HandleFunc("DELETE /v1/tokens/tok1", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(204) })
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	return ts, &mintBody
}

func TestTokenMintScopeAndStdoutSplit(t *testing.T) {
	ts, mintBody := stubTokens(t)
	root := newRootCmd()
	var stdout, stderr bytes.Buffer
	root.SetOut(&stdout)
	root.SetErr(&stderr)
	root.SetArgs([]string{"token", "mint", "--name", "ci", "--config", "prod", "--access", "rw", "--ttl", "24h",
		"--address", ts.URL, "--token", "janus_svc_test", "--project", "acme", "--env", "prod"})
	if err := root.Execute(); err != nil {
		t.Fatalf("mint: %v", err)
	}
	if strings.TrimSpace(stdout.String()) != "janus_svc_SECRET" {
		t.Fatalf("token must be the only thing on stdout, got %q", stdout.String())
	}
	if (*mintBody)["access"] != "readwrite" {
		t.Fatalf("access mapping: %v", (*mintBody)["access"])
	}
	scope := (*mintBody)["scope"].(map[string]any)
	if scope["kind"] != "config" || scope["id"] != "c1" {
		t.Fatalf("scope mapping: %v", scope)
	}
	if (*mintBody)["ttl_seconds"].(float64) != 86400 {
		t.Fatalf("ttl mapping: %v", (*mintBody)["ttl_seconds"])
	}
}

func TestTokenListAndRevoke(t *testing.T) {
	ts, _ := stubTokens(t)
	a := []string{"--address", ts.URL, "--token", "janus_svc_test"}
	out, err := runCLI(t, "", append([]string{"token", "list"}, a...)...)
	if err != nil || !strings.Contains(out, "tok1") {
		t.Fatalf("list: %q %v", out, err)
	}
	if _, err := runCLI(t, "", append([]string{"token", "revoke", "tok1", "--yes"}, a...)...); err != nil {
		t.Fatalf("revoke: %v", err)
	}
}
