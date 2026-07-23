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

// cfCaptures records what the fake Cloudflare Workers Scripts secrets API saw.
type cfCaptures struct {
	mu       sync.Mutex
	store    map[string]string // secret name -> text
	puts     []string          // names PUT (upsert)
	deletes  []string          // names DELETEd
	tokens   []string          // Authorization header values seen
	basePath string            // last .../secrets path observed
}

// newCloudflareTestServer stands up a fake Cloudflare API. PUT .../secrets
// upserts by name and returns the {success:true,...} envelope; DELETE
// .../secrets/:name removes (404 if absent).
func newCloudflareTestServer(t *testing.T) (cloudflareProvider, *cfCaptures, *httptest.Server) {
	t.Helper()
	caps := &cfCaptures{store: map[string]string{}}

	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		caps.mu.Lock()
		defer caps.mu.Unlock()
		caps.tokens = append(caps.tokens, r.Header.Get("Authorization"))

		idx := strings.Index(r.URL.Path, "/secrets")
		if idx < 0 {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		caps.basePath = r.URL.Path[:idx+len("/secrets")]
		rest := strings.TrimPrefix(r.URL.Path[idx+len("/secrets"):], "/")

		switch r.Method {
		case http.MethodPut:
			var b cfSecretBody
			body, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(body, &b)
			caps.store[b.Name] = b.Text
			caps.puts = append(caps.puts, b.Name)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"success": true, "errors": []any{},
				"result": map[string]any{"name": b.Name, "type": b.Type},
			})
		case http.MethodDelete:
			name := rest
			if _, ok := caps.store[name]; !ok {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			delete(caps.store, name)
			caps.deletes = append(caps.deletes, name)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "errors": []any{}})
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	})
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)

	c := cloudflareProvider{hc: srv.Client(), baseURL: srv.URL}
	return c, caps, srv
}

func cfAddr() Addr { return Addr{AccountID: "acct123", ScriptName: "atlas-api"} }

func TestCloudflareApplyUpsertsSecrets(t *testing.T) {
	c, caps, _ := newCloudflareTestServer(t)
	desired := map[string]string{"API_KEY": "s3cret", "DB_URL": "postgres://x"}
	res, err := c.Apply(context.Background(), Creds{APIToken: "cf-token"}, cfAddr(), desired, nil, true)
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
	if !strings.HasSuffix(caps.basePath, "/accounts/acct123/workers/scripts/atlas-api/secrets") {
		t.Errorf("basePath = %q", caps.basePath)
	}
	for _, tok := range caps.tokens {
		if tok != "Bearer cf-token" {
			t.Errorf("auth header = %q, want Bearer cf-token", tok)
		}
	}
}

func TestCloudflarePrunesManagedKeys(t *testing.T) {
	c, caps, _ := newCloudflareTestServer(t)
	if _, err := c.Apply(context.Background(), Creds{APIToken: "t"}, cfAddr(),
		map[string]string{"OLD": "a", "KEEP": "b"}, nil, true); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if _, err := c.Apply(context.Background(), Creds{APIToken: "t"}, cfAddr(),
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
	for _, d := range caps.deletes {
		if d == "KEEP" {
			t.Errorf("KEEP wrongly deleted")
		}
	}
}

func TestCloudflarePruneFalseNoDelete(t *testing.T) {
	c, caps, _ := newCloudflareTestServer(t)
	if _, err := c.Apply(context.Background(), Creds{APIToken: "t"}, cfAddr(),
		map[string]string{"KEEP": "b"}, []string{"OLD", "KEEP"}, false); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	caps.mu.Lock()
	defer caps.mu.Unlock()
	if len(caps.deletes) != 0 {
		t.Errorf("expected no deletes, got %v", caps.deletes)
	}
}

func TestCloudflareDeleteMissingIsIdempotent(t *testing.T) {
	c, _, _ := newCloudflareTestServer(t)
	// Managed key ABSENT from store → DELETE returns 404 → treated as success.
	if _, err := c.Apply(context.Background(), Creds{APIToken: "t"}, cfAddr(),
		map[string]string{}, []string{"GONE"}, true); err != nil {
		t.Fatalf("Apply: %v", err)
	}
}

func TestCloudflareMissingConfig(t *testing.T) {
	c := cloudflareProvider{hc: &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		t.Fatal("no HTTP call should be made for invalid config")
		return nil, nil
	})}}
	cases := []struct {
		name  string
		creds Creds
		addr  Addr
	}{
		{"no token", Creds{}, cfAddr()},
		{"no account", Creds{APIToken: "t"}, Addr{ScriptName: "s"}},
		{"no script", Creds{APIToken: "t"}, Addr{AccountID: "a"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := c.Apply(context.Background(), tc.creds, tc.addr,
				map[string]string{"K": "v"}, nil, true)
			if err != ErrInvalidConfig {
				t.Errorf("err = %v, want ErrInvalidConfig", err)
			}
		})
	}
}

func TestCloudflareUnsafeIDsRejected(t *testing.T) {
	c := cloudflareProvider{hc: &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		t.Fatal("no HTTP call should be made for unsafe id")
		return nil, nil
	})}}
	cases := []Addr{
		{AccountID: "acct/../evil", ScriptName: "s"},
		{AccountID: "a", ScriptName: "s?x=1"},
		{AccountID: "a b", ScriptName: "s"},
	}
	for _, a := range cases {
		_, err := c.Apply(context.Background(), Creds{APIToken: "t"}, a,
			map[string]string{"K": "v"}, nil, false)
		if err != ErrInvalidConfig {
			t.Errorf("addr %+v: err = %v, want ErrInvalidConfig", a, err)
		}
	}
}

func TestCloudflareEnvelopeFailureSanitized(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK) // 200 but success:false
		_ = json.NewEncoder(w).Encode(map[string]any{
			"success": false,
			"errors":  []map[string]any{{"code": 10000, "message": "s3cret leaked in message"}},
		})
	}))
	defer srv.Close()
	c := cloudflareProvider{hc: srv.Client(), baseURL: srv.URL}
	_, err := c.Apply(context.Background(), Creds{APIToken: "cf-token"}, cfAddr(),
		map[string]string{"K": "s3cret"}, nil, false)
	if err == nil {
		t.Fatal("expected error on success:false envelope")
	}
	if strings.Contains(err.Error(), "s3cret") || strings.Contains(err.Error(), "cf-token") {
		t.Errorf("error leaked response/creds: %v", err)
	}
}

func TestCloudflareNon2xxIsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer srv.Close()
	c := cloudflareProvider{hc: srv.Client(), baseURL: srv.URL}
	_, err := c.Apply(context.Background(), Creds{APIToken: "cf-token"}, cfAddr(),
		map[string]string{"K": "s3cret"}, nil, false)
	if err == nil {
		t.Fatal("expected error on 403")
	}
	if strings.Contains(err.Error(), "cf-token") || strings.Contains(err.Error(), "s3cret") {
		t.Errorf("error leaked creds/value: %v", err)
	}
}
