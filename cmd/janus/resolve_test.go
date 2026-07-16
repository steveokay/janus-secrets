package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func newResolveServer() *httptest.Server {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/projects", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"projects":[{"id":"p1","slug":"acme"},{"id":"p2","slug":"other"}]}`))
	})
	mux.HandleFunc("/v1/projects/p1/environments", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"environments":[{"id":"e1","slug":"dev"}]}`))
	})
	mux.HandleFunc("/v1/projects/p1/environments/e1/configs", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"configs":[{"id":"c1","name":"dev"}]}`))
	})
	return httptest.NewServer(mux)
}

func TestResolveConfigIDHappyPath(t *testing.T) {
	ts := newResolveServer()
	defer ts.Close()
	c := &apiClient{address: ts.URL, hc: http.DefaultClient}
	cid, err := c.resolveConfigID("acme", "dev", "dev")
	if err != nil {
		t.Fatal(err)
	}
	if cid != "c1" {
		t.Fatalf("cid = %q, want c1", cid)
	}
}

func TestResolveConfigIDErrorsNameTheLevel(t *testing.T) {
	ts := newResolveServer()
	defer ts.Close()
	c := &apiClient{address: ts.URL, hc: http.DefaultClient}

	if _, err := c.resolveConfigID("nope", "dev", "dev"); err == nil || !strings.Contains(err.Error(), "project") {
		t.Fatalf("want project error, got %v", err)
	}
	if _, err := c.resolveConfigID("acme", "nope", "dev"); err == nil || !strings.Contains(err.Error(), "environment") {
		t.Fatalf("want environment error, got %v", err)
	}
	if _, err := c.resolveConfigID("acme", "dev", "nope"); err == nil || !strings.Contains(err.Error(), "config") {
		t.Fatalf("want config error, got %v", err)
	}
}

func TestResolveProjectAndEnvID(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/projects", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"projects": []map[string]string{{"id": "p1", "slug": "acme"}}})
	})
	mux.HandleFunc("GET /v1/projects/p1/environments", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"environments": []map[string]string{{"id": "e1", "slug": "prod"}}})
	})
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	c, err := newAPIClient(ts.URL, "janus_svc_test")
	if err != nil {
		t.Fatal(err)
	}
	pid, err := c.resolveProjectID("acme")
	if err != nil || pid != "p1" {
		t.Fatalf("resolveProjectID = %q, %v", pid, err)
	}
	if _, err := c.resolveProjectID("nope"); err == nil {
		t.Fatal("expected error for unknown project")
	}
	gotP, eid, err := c.resolveEnvID("acme", "prod")
	if err != nil || gotP != "p1" || eid != "e1" {
		t.Fatalf("resolveEnvID = %q %q %v", gotP, eid, err)
	}
	if _, _, err := c.resolveEnvID("acme", "staging"); err == nil {
		t.Fatal("expected error for unknown env")
	}
}
