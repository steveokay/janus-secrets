package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAPIClientAttachesBearerAndDecodes(t *testing.T) {
	var gotAuth, gotCookie string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		if c, err := r.Cookie("janus_session"); err == nil {
			gotCookie = c.Value
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"projects":[{"id":"p1","slug":"acme"}]}`))
	}))
	defer ts.Close()

	c := &apiClient{address: ts.URL, cred: credential{Bearer: "janus_svc_x"}, hc: http.DefaultClient}
	var out struct {
		Projects []struct{ ID, Slug string } `json:"projects"`
	}
	if err := c.call("GET", "/v1/projects", nil, &out); err != nil {
		t.Fatal(err)
	}
	if gotAuth != "Bearer janus_svc_x" {
		t.Fatalf("auth header = %q", gotAuth)
	}
	if gotCookie != "" {
		t.Fatalf("cookie should be empty for bearer creds, got %q", gotCookie)
	}
	if len(out.Projects) != 1 || out.Projects[0].Slug != "acme" {
		t.Fatalf("decode: %+v", out)
	}
}

func TestAPIClientRewritesAuthErrors(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":{"code":"unauthenticated","message":"no session"}}`))
	}))
	defer ts.Close()

	c := &apiClient{address: ts.URL, cred: credential{Cookie: "sess"}, hc: http.DefaultClient}
	err := c.call("GET", "/v1/projects", nil, nil)
	if err == nil || !strings.Contains(err.Error(), "janus login") {
		t.Fatalf("401 should mention janus login, got %v", err)
	}
}
