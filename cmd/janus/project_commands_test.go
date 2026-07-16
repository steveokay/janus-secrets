package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// stubProjectKEK scripts the owner-only project KEK API for CLI tests and
// records the paths it received on the wire.
func stubProjectKEK(t *testing.T) (*httptest.Server, *[]string) {
	t.Helper()
	var paths []string
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/projects/{pid}/kek/rotate", func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		_ = json.NewEncoder(w).Encode(map[string]any{"kek_version": 2})
	})
	mux.HandleFunc("POST /v1/projects/{pid}/kek/rewrap", func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"rewrapped": 7, "retired_versions": []int{1}, "remaining": 0,
		})
	})
	mux.HandleFunc("GET /v1/projects/{pid}/kek", func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"current_version": 2,
			"pending":         []map[string]any{{"version": 1, "dek_count": 3}},
		})
	})
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	return ts, &paths
}

func TestProjectCmdStructure(t *testing.T) {
	cmd := newProjectCmd()
	if cmd.Use != "project" {
		t.Fatalf("Use = %q", cmd.Use)
	}
	want := map[string]bool{"rotate-kek": false, "rewrap": false, "kek-status": false}
	for _, sub := range cmd.Commands() {
		if _, ok := want[sub.Name()]; ok {
			want[sub.Name()] = true
		}
	}
	for name, found := range want {
		if !found {
			t.Errorf("missing subcommand %q", name)
		}
	}
}

func TestProjectRotateKEK(t *testing.T) {
	ts, paths := stubProjectKEK(t)
	out, err := runCLI(t, "", "project", "rotate-kek", "p1", "--address", ts.URL, "--token", "janus_svc_test")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "2") {
		t.Fatalf("output missing new KEK version 2: %q", out)
	}
	if len(*paths) != 1 || (*paths)[0] != "/v1/projects/p1/kek/rotate" {
		t.Fatalf("wire paths = %v, want [/v1/projects/p1/kek/rotate]", *paths)
	}
}

func TestProjectRewrap(t *testing.T) {
	ts, paths := stubProjectKEK(t)
	out, err := runCLI(t, "", "project", "rewrap", "p1", "--address", ts.URL, "--token", "janus_svc_test")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "7") {
		t.Fatalf("output missing rewrapped count 7: %q", out)
	}
	if len(*paths) != 1 || (*paths)[0] != "/v1/projects/p1/kek/rewrap" {
		t.Fatalf("wire paths = %v, want [/v1/projects/p1/kek/rewrap]", *paths)
	}
}

func TestProjectKEKStatus(t *testing.T) {
	ts, paths := stubProjectKEK(t)
	out, err := runCLI(t, "", "project", "kek-status", "p1", "--address", ts.URL, "--token", "janus_svc_test")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "2") {
		t.Fatalf("output missing current version 2: %q", out)
	}
	if len(*paths) != 1 || (*paths)[0] != "/v1/projects/p1/kek" {
		t.Fatalf("wire paths = %v, want [/v1/projects/p1/kek]", *paths)
	}
}

func stubProjectCRUD(t *testing.T) (*httptest.Server, *[]string) {
	t.Helper()
	var paths []string
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/projects", func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, "POST "+r.URL.Path)
		w.WriteHeader(201)
		_ = json.NewEncoder(w).Encode(map[string]string{"id": "p1", "slug": "acme", "name": "Acme", "created_at": "t"})
	})
	mux.HandleFunc("GET /v1/projects", func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, "GET "+r.URL.Path)
		_ = json.NewEncoder(w).Encode(map[string]any{"projects": []map[string]string{{"id": "p1", "slug": "acme", "name": "Acme", "created_at": "t"}}})
	})
	mux.HandleFunc("DELETE /v1/projects/p1", func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, "DELETE "+r.URL.Path)
		w.WriteHeader(204)
	})
	mux.HandleFunc("POST /v1/projects/p1/restore", func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, "POST "+r.URL.Path)
		_ = json.NewEncoder(w).Encode(map[string]string{"id": "p1", "slug": "acme"})
	})
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	return ts, &paths
}

func TestProjectCreateListDeleteRestore(t *testing.T) {
	ts, paths := stubProjectCRUD(t)
	a := []string{"--address", ts.URL, "--token", "janus_svc_test"}
	out, err := runCLI(t, "", append([]string{"project", "create", "--slug", "acme", "--name", "Acme"}, a...)...)
	if err != nil || !strings.Contains(out, "acme") {
		t.Fatalf("create: %q %v", out, err)
	}
	out, err = runCLI(t, "", append([]string{"project", "list"}, a...)...)
	if err != nil || !strings.Contains(out, "acme") {
		t.Fatalf("list: %q %v", out, err)
	}
	if _, err = runCLI(t, "", append([]string{"project", "delete", "acme", "--yes"}, a...)...); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err = runCLI(t, "", append([]string{"project", "restore", "acme"}, a...)...); err != nil {
		t.Fatalf("restore: %v", err)
	}
	for _, want := range []string{"POST /v1/projects", "GET /v1/projects", "DELETE /v1/projects/p1", "POST /v1/projects/p1/restore"} {
		found := false
		for _, p := range *paths {
			if p == want {
				found = true
			}
		}
		if !found {
			t.Fatalf("missing call %q; saw %v", want, *paths)
		}
	}
}
