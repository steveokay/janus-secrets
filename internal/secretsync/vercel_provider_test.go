package secretsync

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"sync"
	"testing"
)

// vercelCaptures records what the fake Vercel Env Variables API saw.
type vercelCaptures struct {
	mu       sync.Mutex
	store    map[string]string // env var id -> key (managed set)
	values   map[string]string // key -> value
	upserts  []string          // keys POSTed (upsert)
	deletes  []string          // env-var ids DELETEd
	tokens   []string          // Authorization header values seen
	lastPath string            // last request path (sans query)
	teamIDs  []string          // teamId query values seen on writes
}

// newVercelTestServer stands up a fake Vercel API.
//   - POST /v10/projects/{id}/env?upsert=true  → create-or-update by key
//   - GET  /v10/projects/{id}/env              → list {envs:[{id,key}]}
//   - DELETE /v10/projects/{id}/env/{envId}    → remove (404 if absent)
func newVercelTestServer(t *testing.T) (vercelProvider, *vercelCaptures, *httptest.Server) {
	t.Helper()
	caps := &vercelCaptures{store: map[string]string{}, values: map[string]string{}}
	// id counter for created env vars.
	next := 0

	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		caps.mu.Lock()
		defer caps.mu.Unlock()
		caps.tokens = append(caps.tokens, r.Header.Get("Authorization"))
		caps.lastPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")

		// .../env  or  .../env/{envId}
		idx := strings.Index(r.URL.Path, "/env")
		if idx < 0 {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		rest := strings.TrimPrefix(r.URL.Path[idx+len("/env"):], "/")

		switch r.Method {
		case http.MethodPost:
			caps.teamIDs = append(caps.teamIDs, r.URL.Query().Get("teamId"))
			var b vercelEnvBody
			body, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(body, &b)
			// find or create id for this key.
			id := ""
			for eid, k := range caps.store {
				if k == b.Key {
					id = eid
					break
				}
			}
			if id == "" {
				next++
				id = "env_" + b.Key
			}
			caps.store[id] = b.Key
			caps.values[b.Key] = b.Value
			caps.upserts = append(caps.upserts, b.Key)
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"created": map[string]any{"id": id, "key": b.Key, "type": b.Type},
				"failed":  []any{},
			})
		case http.MethodGet:
			envs := make([]map[string]any, 0, len(caps.store))
			for eid, k := range caps.store {
				envs = append(envs, map[string]any{"id": eid, "key": k})
			}
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"envs":       envs,
				"pagination": map[string]any{"count": len(envs), "next": nil, "prev": nil},
			})
		case http.MethodDelete:
			id := rest
			key, ok := caps.store[id]
			if !ok {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			delete(caps.store, id)
			delete(caps.values, key)
			caps.deletes = append(caps.deletes, id)
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(map[string]any{})
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	})
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)

	p := vercelProvider{hc: srv.Client(), baseURL: srv.URL}
	return p, caps, srv
}

func vercelAddr() Addr { return Addr{VercelProject: "prj_atlas"} }

func TestVercelApplyUpsertsSecrets(t *testing.T) {
	p, caps, _ := newVercelTestServer(t)
	desired := map[string]string{"API_KEY": "s3cret", "DB_URL": "postgres://x"}
	res, err := p.Apply(context.Background(), Creds{APIToken: "vc-token"}, vercelAddr(), desired, nil, true)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	sort.Strings(res.Applied)
	if len(res.Applied) != 2 || res.Applied[0] != "API_KEY" || res.Applied[1] != "DB_URL" {
		t.Fatalf("Applied = %v", res.Applied)
	}
	caps.mu.Lock()
	defer caps.mu.Unlock()
	if got := caps.values["API_KEY"]; got != "s3cret" {
		t.Errorf("stored API_KEY = %q, want s3cret", got)
	}
	for _, tok := range caps.tokens {
		if tok != "Bearer vc-token" {
			t.Errorf("auth header = %q, want Bearer vc-token", tok)
		}
	}
}

func TestVercelPrunesManagedKeys(t *testing.T) {
	p, caps, _ := newVercelTestServer(t)
	if _, err := p.Apply(context.Background(), Creds{APIToken: "t"}, vercelAddr(),
		map[string]string{"OLD": "a", "KEEP": "b"}, nil, true); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if _, err := p.Apply(context.Background(), Creds{APIToken: "t"}, vercelAddr(),
		map[string]string{"KEEP": "b"}, []string{"OLD", "KEEP"}, true); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	caps.mu.Lock()
	defer caps.mu.Unlock()
	if _, ok := caps.values["OLD"]; ok {
		t.Errorf("OLD not pruned; values = %v", caps.values)
	}
	if _, ok := caps.values["KEEP"]; !ok {
		t.Errorf("KEEP wrongly removed")
	}
}

func TestVercelPruneFalseNoDelete(t *testing.T) {
	p, caps, _ := newVercelTestServer(t)
	if _, err := p.Apply(context.Background(), Creds{APIToken: "t"}, vercelAddr(),
		map[string]string{"KEEP": "b"}, []string{"OLD", "KEEP"}, false); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	caps.mu.Lock()
	defer caps.mu.Unlock()
	if len(caps.deletes) != 0 {
		t.Errorf("expected no deletes, got %v", caps.deletes)
	}
}

func TestVercelDeleteMissingIsIdempotent(t *testing.T) {
	p, _, _ := newVercelTestServer(t)
	// Managed key absent → list has no matching env id → nothing to delete → success.
	if _, err := p.Apply(context.Background(), Creds{APIToken: "t"}, vercelAddr(),
		map[string]string{}, []string{"GONE"}, true); err != nil {
		t.Fatalf("Apply: %v", err)
	}
}

func TestVercelTeamIDForwarded(t *testing.T) {
	p, caps, _ := newVercelTestServer(t)
	a := Addr{VercelProject: "prj_atlas", VercelTeamID: "team_123"}
	if _, err := p.Apply(context.Background(), Creds{APIToken: "t"}, a,
		map[string]string{"K": "v"}, nil, false); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	caps.mu.Lock()
	defer caps.mu.Unlock()
	found := false
	for _, tid := range caps.teamIDs {
		if tid == "team_123" {
			found = true
		}
	}
	if !found {
		t.Errorf("teamId not forwarded; saw %v", caps.teamIDs)
	}
}

func TestVercelMissingConfig(t *testing.T) {
	p := vercelProvider{hc: &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		t.Fatal("no HTTP call should be made for invalid config")
		return nil, nil
	})}}
	cases := []struct {
		name  string
		creds Creds
		addr  Addr
	}{
		{"no token", Creds{}, vercelAddr()},
		{"no project", Creds{APIToken: "t"}, Addr{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := p.Apply(context.Background(), tc.creds, tc.addr,
				map[string]string{"K": "v"}, nil, true)
			if err != ErrInvalidConfig {
				t.Errorf("err = %v, want ErrInvalidConfig", err)
			}
		})
	}
}

func TestVercelUnsafeIDsRejected(t *testing.T) {
	p := vercelProvider{hc: &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		t.Fatal("no HTTP call should be made for unsafe id")
		return nil, nil
	})}}
	cases := []Addr{
		{VercelProject: "prj/../evil"},
		{VercelProject: "p?x=1"},
		{VercelProject: "a b"},
		{VercelProject: "ok", VercelTeamID: "team/../x"},
	}
	for _, a := range cases {
		_, err := p.Apply(context.Background(), Creds{APIToken: "t"}, a,
			map[string]string{"K": "v"}, nil, false)
		if err != ErrInvalidConfig {
			t.Errorf("addr %+v: err = %v, want ErrInvalidConfig", a, err)
		}
	}
}

func TestVercelInvalidTargetRejected(t *testing.T) {
	p := vercelProvider{hc: &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		t.Fatal("no HTTP call should be made for invalid target")
		return nil, nil
	})}}
	a := Addr{VercelProject: "ok", VercelTargets: []string{"prod"}} // must be production/preview/development
	_, err := p.Apply(context.Background(), Creds{APIToken: "t"}, a,
		map[string]string{"K": "v"}, nil, false)
	if err != ErrInvalidConfig {
		t.Errorf("err = %v, want ErrInvalidConfig", err)
	}
}

func TestVercelFailedEntrySanitized(t *testing.T) {
	// POST returns 201 but the body reports the key in the failed[] array.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"created": []any{},
			"failed": []map[string]any{
				{"error": map[string]any{"code": "ENV_ALREADY_EXISTS", "message": "s3cret leaked in message"}},
			},
		})
	}))
	defer srv.Close()
	p := vercelProvider{hc: srv.Client(), baseURL: srv.URL}
	_, err := p.Apply(context.Background(), Creds{APIToken: "vc-token"}, vercelAddr(),
		map[string]string{"K": "s3cret"}, nil, false)
	if err == nil {
		t.Fatal("expected error on failed[] entry")
	}
	if strings.Contains(err.Error(), "s3cret") || strings.Contains(err.Error(), "vc-token") {
		t.Errorf("error leaked response/creds: %v", err)
	}
}

func TestVercelNon2xxIsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer srv.Close()
	p := vercelProvider{hc: srv.Client(), baseURL: srv.URL}
	_, err := p.Apply(context.Background(), Creds{APIToken: "vc-token"}, vercelAddr(),
		map[string]string{"K": "s3cret"}, nil, false)
	if err == nil {
		t.Fatal("expected error on 403")
	}
	if strings.Contains(err.Error(), "vc-token") || strings.Contains(err.Error(), "s3cret") {
		t.Errorf("error leaked creds/value: %v", err)
	}
}

func TestVercelName(t *testing.T) {
	if (vercelProvider{}).Name() != ProviderVercel {
		t.Errorf("Name = %q", (vercelProvider{}).Name())
	}
}
