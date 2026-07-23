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

// netlifyCaptures records what the fake Netlify Env Variables API saw.
type netlifyCaptures struct {
	mu       sync.Mutex
	store    map[string]string // key -> value
	posts    []string          // keys POSTed (create)
	puts     []string          // keys PUT (replace-on-conflict)
	deletes  []string          // keys DELETEd
	tokens   []string          // Authorization header values seen
	siteIDs  []string          // site_id query values seen
	lastPath string
}

// newNetlifyTestServer stands up a fake Netlify accounts env API.
//   - POST   /api/v1/accounts/{acct}/env         → create (409 if key exists)
//   - PUT    /api/v1/accounts/{acct}/env/{key}   → replace values
//   - GET    /api/v1/accounts/{acct}/env         → list [{key}]
//   - DELETE /api/v1/accounts/{acct}/env/{key}   → remove (404 if absent)
func newNetlifyTestServer(t *testing.T) (netlifyProvider, *netlifyCaptures, *httptest.Server) {
	t.Helper()
	caps := &netlifyCaptures{store: map[string]string{}}

	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		caps.mu.Lock()
		defer caps.mu.Unlock()
		caps.tokens = append(caps.tokens, r.Header.Get("Authorization"))
		caps.siteIDs = append(caps.siteIDs, r.URL.Query().Get("site_id"))
		caps.lastPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")

		idx := strings.Index(r.URL.Path, "/env")
		if idx < 0 {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		key := strings.TrimPrefix(r.URL.Path[idx+len("/env"):], "/")

		switch r.Method {
		case http.MethodPost:
			var arr []netlifyEnvVar
			body, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(body, &arr)
			for _, ev := range arr {
				if _, exists := caps.store[ev.Key]; exists {
					w.WriteHeader(http.StatusConflict)
					_ = json.NewEncoder(w).Encode(map[string]any{"code": 409, "message": "key exists"})
					return
				}
			}
			for _, ev := range arr {
				val := ""
				if len(ev.Values) > 0 {
					val = ev.Values[0].Value
				}
				caps.store[ev.Key] = val
				caps.posts = append(caps.posts, ev.Key)
			}
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(arr)
		case http.MethodPut:
			var ev netlifyEnvVar
			body, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(body, &ev)
			val := ""
			if len(ev.Values) > 0 {
				val = ev.Values[0].Value
			}
			caps.store[key] = val
			caps.puts = append(caps.puts, key)
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(ev)
		case http.MethodGet:
			out := make([]map[string]any, 0, len(caps.store))
			for k := range caps.store {
				out = append(out, map[string]any{"key": k})
			}
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(out)
		case http.MethodDelete:
			if _, ok := caps.store[key]; !ok {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			delete(caps.store, key)
			caps.deletes = append(caps.deletes, key)
			w.WriteHeader(http.StatusNoContent)
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	})
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)

	p := netlifyProvider{hc: srv.Client(), baseURL: srv.URL}
	return p, caps, srv
}

func netlifyAddr() Addr { return Addr{NetlifyAccountID: "acct_abc", NetlifySiteID: "site_xyz"} }

func TestNetlifyApplyCreatesSecrets(t *testing.T) {
	p, caps, _ := newNetlifyTestServer(t)
	desired := map[string]string{"API_KEY": "s3cret", "DB_URL": "postgres://x"}
	res, err := p.Apply(context.Background(), Creds{APIToken: "nf-token"}, netlifyAddr(), desired, nil, true)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	sort.Strings(res.Applied)
	if len(res.Applied) != 2 || res.Applied[0] != "API_KEY" || res.Applied[1] != "DB_URL" {
		t.Fatalf("Applied = %v", res.Applied)
	}
	caps.mu.Lock()
	defer caps.mu.Unlock()
	if got := caps.store["API_KEY"]; got != "s3cret" {
		t.Errorf("stored API_KEY = %q, want s3cret", got)
	}
	for _, tok := range caps.tokens {
		if tok != "Bearer nf-token" {
			t.Errorf("auth header = %q, want Bearer nf-token", tok)
		}
	}
	for _, sid := range caps.siteIDs {
		if sid != "site_xyz" {
			t.Errorf("site_id = %q, want site_xyz", sid)
		}
	}
}

func TestNetlifyUpsertFallsBackToPUT(t *testing.T) {
	p, caps, _ := newNetlifyTestServer(t)
	// First apply creates via POST.
	if _, err := p.Apply(context.Background(), Creds{APIToken: "t"}, netlifyAddr(),
		map[string]string{"K": "v1"}, nil, false); err != nil {
		t.Fatalf("seed: %v", err)
	}
	// Second apply of the same key → POST 409 → PUT replace.
	if _, err := p.Apply(context.Background(), Creds{APIToken: "t"}, netlifyAddr(),
		map[string]string{"K": "v2"}, nil, false); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	caps.mu.Lock()
	defer caps.mu.Unlock()
	if caps.store["K"] != "v2" {
		t.Errorf("value = %q, want v2 (PUT replace)", caps.store["K"])
	}
	if len(caps.puts) == 0 {
		t.Errorf("expected a PUT fallback, saw none")
	}
}

func TestNetlifyPrunesManagedKeys(t *testing.T) {
	p, caps, _ := newNetlifyTestServer(t)
	if _, err := p.Apply(context.Background(), Creds{APIToken: "t"}, netlifyAddr(),
		map[string]string{"OLD": "a", "KEEP": "b"}, nil, true); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if _, err := p.Apply(context.Background(), Creds{APIToken: "t"}, netlifyAddr(),
		map[string]string{"KEEP": "b"}, []string{"OLD", "KEEP"}, true); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	caps.mu.Lock()
	defer caps.mu.Unlock()
	if _, ok := caps.store["OLD"]; ok {
		t.Errorf("OLD not pruned; store = %v", caps.store)
	}
	if _, ok := caps.store["KEEP"]; !ok {
		t.Errorf("KEEP wrongly removed")
	}
}

func TestNetlifyPruneFalseNoDelete(t *testing.T) {
	p, caps, _ := newNetlifyTestServer(t)
	if _, err := p.Apply(context.Background(), Creds{APIToken: "t"}, netlifyAddr(),
		map[string]string{"KEEP": "b"}, []string{"OLD", "KEEP"}, false); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	caps.mu.Lock()
	defer caps.mu.Unlock()
	if len(caps.deletes) != 0 {
		t.Errorf("expected no deletes, got %v", caps.deletes)
	}
}

func TestNetlifyDeleteMissingIsIdempotent(t *testing.T) {
	p, _, _ := newNetlifyTestServer(t)
	if _, err := p.Apply(context.Background(), Creds{APIToken: "t"}, netlifyAddr(),
		map[string]string{}, []string{"GONE"}, true); err != nil {
		t.Fatalf("Apply: %v", err)
	}
}

func TestNetlifyMissingConfig(t *testing.T) {
	p := netlifyProvider{hc: &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		t.Fatal("no HTTP call should be made for invalid config")
		return nil, nil
	})}}
	cases := []struct {
		name  string
		creds Creds
		addr  Addr
	}{
		{"no token", Creds{}, netlifyAddr()},
		{"no account", Creds{APIToken: "t"}, Addr{NetlifySiteID: "s"}},
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

func TestNetlifyUnsafeIDsRejected(t *testing.T) {
	p := netlifyProvider{hc: &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		t.Fatal("no HTTP call should be made for unsafe id")
		return nil, nil
	})}}
	cases := []Addr{
		{NetlifyAccountID: "acct/../evil"},
		{NetlifyAccountID: "a?x=1"},
		{NetlifyAccountID: "a b"},
	}
	for _, a := range cases {
		_, err := p.Apply(context.Background(), Creds{APIToken: "t"}, a,
			map[string]string{"K": "v"}, nil, false)
		if err != ErrInvalidConfig {
			t.Errorf("addr %+v: err = %v, want ErrInvalidConfig", a, err)
		}
	}
}

// An unsafe KEY must not smuggle into the PUT/DELETE path; it is skipped, not sent.
func TestNetlifyUnsafeKeySkipped(t *testing.T) {
	p, caps, _ := newNetlifyTestServer(t)
	// Seed a key, then prune a managed key with an unsafe name.
	if _, err := p.Apply(context.Background(), Creds{APIToken: "t"}, netlifyAddr(),
		map[string]string{"KEEP": "b"}, []string{"KEEP", "bad/../key"}, true); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	caps.mu.Lock()
	defer caps.mu.Unlock()
	for _, d := range caps.deletes {
		if strings.Contains(d, "..") {
			t.Errorf("unsafe key was sent to DELETE: %q", d)
		}
	}
}

func TestNetlifyNon2xxIsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer srv.Close()
	p := netlifyProvider{hc: srv.Client(), baseURL: srv.URL}
	_, err := p.Apply(context.Background(), Creds{APIToken: "nf-token"}, netlifyAddr(),
		map[string]string{"K": "s3cret"}, nil, false)
	if err == nil {
		t.Fatal("expected error on 403")
	}
	if strings.Contains(err.Error(), "nf-token") || strings.Contains(err.Error(), "s3cret") {
		t.Errorf("error leaked creds/value: %v", err)
	}
}

func TestNetlifyName(t *testing.T) {
	if (netlifyProvider{}).Name() != ProviderNetlify {
		t.Errorf("Name = %q", (netlifyProvider{}).Name())
	}
}
