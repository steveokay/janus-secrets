package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func stubConfigCRUD(t *testing.T) (*httptest.Server, *[]string) {
	t.Helper()
	var paths []string
	// Stateful: a soft-deleted config leaves the live list and appears in /v1/trash.
	deleted := false
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/projects", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"projects": []map[string]string{{"id": "p1", "slug": "acme"}}})
	})
	mux.HandleFunc("GET /v1/projects/p1/environments", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"environments": []map[string]string{{"id": "e1", "slug": "prod"}}})
	})
	mux.HandleFunc("POST /v1/projects/p1/environments/e1/configs", func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, "POST "+r.URL.Path)
		_ = json.NewEncoder(w).Encode(map[string]any{"id": "c1", "name": "prod", "environment_id": "e1"})
	})
	mux.HandleFunc("GET /v1/projects/p1/environments/e1/configs", func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, "GET "+r.URL.Path)
		list := []map[string]any{}
		if !deleted {
			list = append(list, map[string]any{"id": "c1", "name": "prod", "environment_id": "e1"})
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"configs": list})
	})
	mux.HandleFunc("GET /v1/trash", func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, "GET "+r.URL.Path)
		configs := []map[string]any{}
		if deleted {
			configs = append(configs, map[string]any{"id": "c1", "name": "prod", "environment_id": "e1"})
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"projects": []any{}, "environments": []any{}, "configs": configs})
	})
	mux.HandleFunc("DELETE /v1/configs/c1", func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, "DELETE "+r.URL.Path)
		deleted = true
		w.WriteHeader(204)
	})
	mux.HandleFunc("POST /v1/configs/c1/restore", func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, "POST "+r.URL.Path)
		deleted = false
		_ = json.NewEncoder(w).Encode(map[string]string{"id": "c1"})
	})
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	return ts, &paths
}

func TestConfigCreateListDeleteRestore(t *testing.T) {
	ts, paths := stubConfigCRUD(t)
	a := []string{"--address", ts.URL, "--token", "janus_svc_test", "--project", "acme", "--env", "prod"}
	if _, err := runCLI(t, "", append([]string{"config", "create", "--name", "prod"}, a...)...); err != nil {
		t.Fatalf("create: %v", err)
	}
	out, err := runCLI(t, "", append([]string{"config", "list"}, a...)...)
	if err != nil || !strings.Contains(out, "prod") {
		t.Fatalf("list: %q %v", out, err)
	}
	if _, err := runCLI(t, "", append([]string{"config", "delete", "prod", "--yes"}, a...)...); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := runCLI(t, "", append([]string{"config", "restore", "prod"}, a...)...); err != nil {
		t.Fatalf("restore: %v", err)
	}
	for _, want := range []string{"POST /v1/projects/p1/environments/e1/configs", "DELETE /v1/configs/c1", "GET /v1/trash", "POST /v1/configs/c1/restore"} {
		found := false
		for _, p := range *paths {
			if p == want {
				found = true
			}
		}
		if !found {
			t.Fatalf("missing %q; saw %v", want, *paths)
		}
	}
}
