package main

import (
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
