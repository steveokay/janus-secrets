package client

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// newTestClient wires a Client to a fake Janus httptest.Server.
func newTestClient(t *testing.T, h http.Handler) (*Client, *httptest.Server) {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	c, err := New(srv.URL, "janus_svc_faketoken", &http.Client{Timeout: 5 * time.Second})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return c, srv
}

func writeJSON(t *testing.T, w http.ResponseWriter, status int, v any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		t.Fatalf("encode: %v", err)
	}
}

func TestNewValidation(t *testing.T) {
	if _, err := New("", "t", &http.Client{}); err == nil {
		t.Fatal("empty endpoint should error")
	}
	if _, err := New("https://x", "t", nil); err == nil {
		t.Fatal("nil http client should error")
	}
}

func TestCreateProjectSendsSlugAndName(t *testing.T) {
	var gotAuth, gotBody string
	c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/projects" {
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
		gotAuth = r.Header.Get("Authorization")
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		writeJSON(t, w, http.StatusCreated, Project{ID: "p-1", Slug: "acme", Name: "Acme"})
	}))

	p, err := c.CreateProject(context.Background(), "acme", "Acme")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	if p.ID != "p-1" || p.Slug != "acme" || p.Name != "Acme" {
		t.Errorf("unexpected project %+v", p)
	}
	if gotAuth != "Bearer janus_svc_faketoken" {
		t.Errorf("auth header = %q", gotAuth)
	}
	if !strings.Contains(gotBody, `"slug":"acme"`) || !strings.Contains(gotBody, `"name":"Acme"`) {
		t.Errorf("body = %s", gotBody)
	}
}

func TestCreateProjectOmitsEmptyName(t *testing.T) {
	c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		if strings.Contains(string(b), "name") {
			t.Errorf("empty name should be omitted, got %s", b)
		}
		writeJSON(t, w, http.StatusCreated, Project{ID: "p-1", Slug: "acme"})
	}))
	if _, err := c.CreateProject(context.Background(), "acme", ""); err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
}

func TestUpdateAndDeleteProject(t *testing.T) {
	var sawPatch, sawDestroy bool
	c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPatch:
			sawPatch = true
			writeJSON(t, w, http.StatusOK, Project{ID: "p-1", Slug: "acme", Name: "Renamed"})
		case r.Method == http.MethodDelete:
			if r.URL.Query().Get("destroy") == "true" {
				sawDestroy = true
			}
			w.WriteHeader(http.StatusNoContent)
		}
	}))
	p, err := c.UpdateProject(context.Background(), "p-1", "Renamed")
	if err != nil || p.Name != "Renamed" {
		t.Fatalf("UpdateProject: %v %+v", err, p)
	}
	if !sawPatch {
		t.Error("expected PATCH")
	}
	if err := c.DeleteProject(context.Background(), "p-1", true); err != nil {
		t.Fatalf("DeleteProject: %v", err)
	}
	if !sawDestroy {
		t.Error("expected destroy=true query")
	}
}

func TestEnvironmentCRUDPaths(t *testing.T) {
	c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v1/projects/p-1/environments":
			writeJSON(t, w, http.StatusCreated, Environment{ID: "e-1", ProjectID: "p-1", Slug: "prod", Name: "Prod"})
		case r.Method == http.MethodGet && r.URL.Path == "/v1/projects/p-1/environments/e-1":
			writeJSON(t, w, http.StatusOK, Environment{ID: "e-1", ProjectID: "p-1", Slug: "prod", Name: "Prod"})
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
	}))
	e, err := c.CreateEnvironment(context.Background(), "p-1", "prod", "Prod")
	if err != nil || e.ID != "e-1" || e.ProjectID != "p-1" {
		t.Fatalf("CreateEnvironment: %v %+v", err, e)
	}
	g, err := c.GetEnvironment(context.Background(), "p-1", "e-1")
	if err != nil || g.Slug != "prod" {
		t.Fatalf("GetEnvironment: %v %+v", err, g)
	}
}

func TestCreateConfigInheritsFrom(t *testing.T) {
	var body map[string]any
	c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/projects/p-1/environments/e-1/configs" {
			t.Errorf("path %s", r.URL.Path)
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		parent := "cfg-parent"
		writeJSON(t, w, http.StatusCreated, Config{ID: "cfg-1", EnvironmentID: "e-1", Name: "prod", InheritsFrom: &parent})
	}))
	parent := "cfg-parent"
	cfg, err := c.CreateConfig(context.Background(), "p-1", "e-1", "prod", &parent)
	if err != nil {
		t.Fatalf("CreateConfig: %v", err)
	}
	if body["inherits_from"] != "cfg-parent" {
		t.Errorf("inherits_from = %v", body["inherits_from"])
	}
	if cfg.InheritsFrom == nil || *cfg.InheritsFrom != "cfg-parent" {
		t.Errorf("cfg.InheritsFrom = %v", cfg.InheritsFrom)
	}
}

func TestCreateConfigNoInherit(t *testing.T) {
	c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		if _, ok := body["inherits_from"]; ok {
			t.Errorf("inherits_from should be omitted, body=%v", body)
		}
		writeJSON(t, w, http.StatusCreated, Config{ID: "cfg-1", EnvironmentID: "e-1", Name: "root"})
	}))
	if _, err := c.CreateConfig(context.Background(), "p-1", "e-1", "root", nil); err != nil {
		t.Fatalf("CreateConfig: %v", err)
	}
}

func TestSecretSetGetDelete(t *testing.T) {
	const key = "DATABASE_URL"
	const want = "postgres://placeholder-fixture/db"
	var putBody map[string]string
	var sawRaw bool
	c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodPut:
			_ = json.NewDecoder(r.Body).Decode(&putBody)
			writeJSON(t, w, http.StatusOK, map[string]any{"version": 2, "id": "v-2"})
		case http.MethodGet:
			if r.URL.Query().Get("raw") == "true" {
				sawRaw = true
			}
			writeJSON(t, w, http.StatusOK, map[string]string{"key": key, "value": want})
		case http.MethodDelete:
			writeJSON(t, w, http.StatusOK, map[string]any{"version": 3, "id": "v-3"})
		}
	}))
	if err := c.SetSecret(context.Background(), "cfg-1", key, want); err != nil {
		t.Fatalf("SetSecret: %v", err)
	}
	if putBody["value"] != want {
		t.Errorf("put value mismatch")
	}
	got, err := c.GetSecret(context.Background(), "cfg-1", key)
	if err != nil {
		t.Fatalf("GetSecret: %v", err)
	}
	if got != want {
		t.Errorf("got %q want %q", got, want)
	}
	if !sawRaw {
		t.Error("expected raw=true on reveal")
	}
	if err := c.DeleteSecret(context.Background(), "cfg-1", key); err != nil {
		t.Fatalf("DeleteSecret: %v", err)
	}
}

func TestMintToken(t *testing.T) {
	var body map[string]any
	c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/tokens" || r.Method != http.MethodPost {
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		writeJSON(t, w, http.StatusOK, MintedToken{
			Token:  "janus_svc_minted_placeholder",
			ID:     "tok-1",
			Name:   "ci",
			Scope:  TokenScope{Kind: "config", ID: "cfg-1"},
			Access: "read",
		})
	}))
	tk, err := c.MintToken(context.Background(), "ci", "config", "cfg-1", "read")
	if err != nil {
		t.Fatalf("MintToken: %v", err)
	}
	if tk.Token != "janus_svc_minted_placeholder" || tk.ID != "tok-1" {
		t.Errorf("token %+v", tk)
	}
	scope := body["scope"].(map[string]any)
	if scope["kind"] != "config" || scope["id"] != "cfg-1" {
		t.Errorf("scope %v", scope)
	}
	if body["access"] != "read" {
		t.Errorf("access %v", body["access"])
	}
}

func TestGetTokenMetaPaginates(t *testing.T) {
	c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("cursor") == "" {
			next := "page2"
			writeJSON(t, w, http.StatusOK, map[string]any{
				"tokens":      []TokenMeta{{ID: "tok-0", Name: "other"}},
				"next_cursor": next,
			})
			return
		}
		writeJSON(t, w, http.StatusOK, map[string]any{
			"tokens":      []TokenMeta{{ID: "tok-1", Name: "ci", ScopeKind: "config", ScopeID: "cfg-1", Access: "read"}},
			"next_cursor": nil,
		})
	}))
	m, err := c.GetTokenMeta(context.Background(), "tok-1")
	if err != nil {
		t.Fatalf("GetTokenMeta: %v", err)
	}
	if m.Name != "ci" || m.ScopeKind != "config" {
		t.Errorf("meta %+v", m)
	}
}

func TestGetTokenMetaNotFound(t *testing.T) {
	c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, http.StatusOK, map[string]any{"tokens": []TokenMeta{}, "next_cursor": nil})
	}))
	_, err := c.GetTokenMeta(context.Background(), "missing")
	if !IsNotFound(err) {
		t.Fatalf("expected not found, got %v", err)
	}
}

func TestErrorEnvelopeMapping(t *testing.T) {
	c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		_, _ = io.WriteString(w, `{"error":{"code":"forbidden","message":"denied by policy"}}`)
	}))
	_, err := c.GetProject(context.Background(), "p-1")
	if err == nil {
		t.Fatal("expected error")
	}
	if IsNotFound(err) {
		t.Fatal("403 should not be not-found")
	}
	if !strings.Contains(err.Error(), "forbidden") || !strings.Contains(err.Error(), "denied by policy") {
		t.Errorf("error = %v", err)
	}
}

func TestNotFoundDrift(t *testing.T) {
	c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_, _ = io.WriteString(w, `{"error":{"code":"not_found","message":"gone"}}`)
	}))
	_, err := c.GetProject(context.Background(), "p-1")
	if !IsNotFound(err) {
		t.Fatalf("expected IsNotFound, got %v", err)
	}
}
