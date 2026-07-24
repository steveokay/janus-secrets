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

// glVar mirrors GitLab's variable representation for the fake server's store.
type glVar struct {
	Key              string `json:"key"`
	Value            string `json:"value"`
	Masked           bool   `json:"masked"`
	Protected        bool   `json:"protected"`
	EnvironmentScope string `json:"environment_scope"`
}

// glCaptures records what the fake GitLab API saw.
type glCaptures struct {
	mu       sync.Mutex
	store    map[string]glVar // key -> variable
	posts    []string         // keys POSTed (create)
	puts     []string         // keys PUT (update)
	deletes  []string         // keys DELETEd
	tokens   []string         // PRIVATE-TOKEN header values seen
	basePath string           // last project variables path prefix seen
}

// newGitLabTestServer stands up a fake GitLab CI/CD variables API. It models
// create-vs-update: POST creates (409 if exists), PUT updates (404 if absent).
func newGitLabTestServer(t *testing.T) (gitlabProvider, *glCaptures, *httptest.Server) {
	t.Helper()
	caps := &glCaptures{store: map[string]glVar{}}

	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		caps.mu.Lock()
		defer caps.mu.Unlock()
		caps.tokens = append(caps.tokens, r.Header.Get("PRIVATE-TOKEN"))

		// .../variables            (POST create)
		// .../variables/:key       (PUT update, DELETE remove)
		idx := strings.Index(r.URL.Path, "/variables")
		if idx < 0 {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		// Use the escaped path so a URL-encoded project path (g%2Fp) is
		// observed as-sent, matching how the real GitLab API routes it.
		ep := r.URL.EscapedPath()
		idxE := strings.Index(ep, "/variables")
		if idxE >= 0 {
			caps.basePath = ep[:idxE+len("/variables")]
		}
		rest := strings.TrimPrefix(r.URL.Path[idx+len("/variables"):], "/")

		switch r.Method {
		case http.MethodPost:
			var v glVar
			body, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(body, &v)
			if _, ok := caps.store[v.Key]; ok {
				w.WriteHeader(http.StatusConflict) // already exists
				return
			}
			caps.store[v.Key] = v
			caps.posts = append(caps.posts, v.Key)
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(v)
		case http.MethodPut:
			key := rest
			var v glVar
			body, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(body, &v)
			v.Key = key
			if _, ok := caps.store[key]; !ok {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			caps.store[key] = v
			caps.puts = append(caps.puts, key)
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(v)
		case http.MethodDelete:
			key := rest
			delete(caps.store, key)
			caps.deletes = append(caps.deletes, key)
			w.WriteHeader(http.StatusNoContent)
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	})
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)

	g := gitlabProvider{hc: srv.Client()}
	return g, caps, srv
}

func TestGitLabApplyCreatesAndUpdates(t *testing.T) {
	g, caps, srv := newGitLabTestServer(t)

	// First apply: both keys are new → POST.
	desired := map[string]string{"API_KEY": "s3cret", "DB_URL": "postgres://x"}
	res, err := g.Apply(context.Background(), Creds{Token: "glpat-x"},
		Addr{GitLabURL: srv.URL, Project: "42"}, desired, nil, true)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	sort.Strings(res.Applied)
	if len(res.Applied) != 2 || res.Applied[0] != "API_KEY" || res.Applied[1] != "DB_URL" {
		t.Fatalf("Applied = %v, want [API_KEY DB_URL]", res.Applied)
	}
	caps.mu.Lock()
	if len(caps.posts) != 2 {
		t.Errorf("posts = %v, want 2 creates", caps.posts)
	}
	if got := caps.store["API_KEY"].Value; got != "s3cret" {
		t.Errorf("stored API_KEY = %q, want s3cret", got)
	}
	// masked/protected must default false to avoid GitLab mask-regex rejections.
	if caps.store["API_KEY"].Masked || caps.store["API_KEY"].Protected {
		t.Errorf("masked/protected should default false, got %+v", caps.store["API_KEY"])
	}
	if !strings.Contains(caps.basePath, "/api/v4/projects/42/variables") {
		t.Errorf("basePath = %q, want /api/v4/projects/42/variables", caps.basePath)
	}
	caps.mu.Unlock()

	// Second apply on an existing key: POST 409 → falls back to PUT update.
	_, err = g.Apply(context.Background(), Creds{Token: "glpat-x"},
		Addr{GitLabURL: srv.URL, Project: "42"}, map[string]string{"API_KEY": "rotated"}, []string{"API_KEY"}, false)
	if err != nil {
		t.Fatalf("Apply update: %v", err)
	}
	caps.mu.Lock()
	if got := caps.store["API_KEY"].Value; got != "rotated" {
		t.Errorf("after update API_KEY = %q, want rotated", got)
	}
	if len(caps.puts) != 1 || caps.puts[0] != "API_KEY" {
		t.Errorf("puts = %v, want [API_KEY]", caps.puts)
	}
	caps.mu.Unlock()
}

func TestGitLabEnvironmentScope(t *testing.T) {
	g, caps, srv := newGitLabTestServer(t)
	_, err := g.Apply(context.Background(), Creds{Token: "t"},
		Addr{GitLabURL: srv.URL, Project: "g%2Fp", EnvironmentScope: "production"},
		map[string]string{"K": "v"}, nil, false)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	caps.mu.Lock()
	defer caps.mu.Unlock()
	if got := caps.store["K"].EnvironmentScope; got != "production" {
		t.Errorf("environment_scope = %q, want production", got)
	}
	if !strings.Contains(caps.basePath, "/api/v4/projects/g%2Fp/variables") {
		t.Errorf("basePath = %q, want project g%%2Fp", caps.basePath)
	}
}

func TestGitLabPrunesManagedKeys(t *testing.T) {
	g, caps, srv := newGitLabTestServer(t)
	// Seed OLD and KEEP.
	if _, err := g.Apply(context.Background(), Creds{Token: "t"},
		Addr{GitLabURL: srv.URL, Project: "1"}, map[string]string{"OLD": "a", "KEEP": "b"}, nil, true); err != nil {
		t.Fatalf("seed: %v", err)
	}
	// Now KEEP only, prune OLD (managed).
	_, err := g.Apply(context.Background(), Creds{Token: "t"},
		Addr{GitLabURL: srv.URL, Project: "1"}, map[string]string{"KEEP": "b"}, []string{"OLD", "KEEP"}, true)
	if err != nil {
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
	deletedKeep := false
	for _, d := range caps.deletes {
		if d == "KEEP" {
			deletedKeep = true
		}
	}
	if deletedKeep {
		t.Errorf("KEEP wrongly deleted; deletes = %v", caps.deletes)
	}
}

func TestGitLabPruneFalseNoDelete(t *testing.T) {
	g, caps, srv := newGitLabTestServer(t)
	if _, err := g.Apply(context.Background(), Creds{Token: "t"},
		Addr{GitLabURL: srv.URL, Project: "1"}, map[string]string{"KEEP": "b"}, nil, true); err != nil {
		t.Fatalf("seed: %v", err)
	}
	_, err := g.Apply(context.Background(), Creds{Token: "t"},
		Addr{GitLabURL: srv.URL, Project: "1"}, map[string]string{"KEEP": "b"}, []string{"OLD", "KEEP"}, false)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	caps.mu.Lock()
	defer caps.mu.Unlock()
	if len(caps.deletes) != 0 {
		t.Errorf("expected no deletes, got %v", caps.deletes)
	}
}

func TestGitLabDefaultBaseURL(t *testing.T) {
	// When GitLabURL is empty, the provider should target gitlab.com — but we
	// can't hit the network in a hermetic test, so assert URL construction via
	// a stub transport that captures the request URL and short-circuits.
	var gotURL string
	g := gitlabProvider{hc: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		gotURL = r.URL.String()
		// Respond 201 so create path succeeds without a second call.
		return &http.Response{StatusCode: http.StatusCreated, Body: io.NopCloser(strings.NewReader(`{}`)), Header: http.Header{}}, nil
	})}}
	_, err := g.Apply(context.Background(), Creds{Token: "t"},
		Addr{Project: "77"}, map[string]string{"K": "v"}, nil, false)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if !strings.HasPrefix(gotURL, "https://gitlab.com/api/v4/projects/77/variables") {
		t.Errorf("gotURL = %q, want gitlab.com default base", gotURL)
	}
}

func TestGitLabMissingConfig(t *testing.T) {
	g := gitlabProvider{hc: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		t.Fatal("no HTTP call should be made for invalid config")
		return nil, nil
	})}}
	cases := []struct {
		name  string
		creds Creds
		addr  Addr
	}{
		{"empty token", Creds{}, Addr{Project: "1"}},
		{"empty project", Creds{Token: "t"}, Addr{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := g.Apply(context.Background(), tc.creds, tc.addr,
				map[string]string{"K": "v"}, nil, true)
			if err != ErrInvalidConfig {
				t.Errorf("err = %v, want ErrInvalidConfig", err)
			}
		})
	}
}

func TestGitLabBadBaseURLRejected(t *testing.T) {
	g := gitlabProvider{hc: http.DefaultClient}
	_, err := g.Apply(context.Background(), Creds{Token: "t"},
		Addr{GitLabURL: "ht!tp://bad url", Project: "1"}, map[string]string{"K": "v"}, nil, false)
	if err != ErrInvalidConfig {
		t.Errorf("err = %v, want ErrInvalidConfig for malformed URL", err)
	}
}

func TestGitLabNon2xxIsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer srv.Close()
	g := gitlabProvider{hc: srv.Client()}
	_, err := g.Apply(context.Background(), Creds{Token: "t"},
		Addr{GitLabURL: srv.URL, Project: "1"}, map[string]string{"K": "v"}, nil, false)
	if err == nil {
		t.Fatal("expected error on 403")
	}
	// Error must be value-free (no secret value / token).
	if strings.Contains(err.Error(), "glpat") || strings.Contains(err.Error(), "s3cret") {
		t.Errorf("error leaked creds/value: %v", err)
	}
}

// TestGitLabProjectValidation asserts the project-id charset guard accepts a
// numeric id and a URL-encoded namespace/project path, and rejects any value
// that could inject path/query/fragment into the request URL. It checks both
// enforcement points: validateInput (config create/update time) and the
// URL-building variablesBase (defensive). No HTTP call is made for a bad id.
func TestGitLabProjectValidation(t *testing.T) {
	cases := []struct {
		name    string
		project string
		wantOK  bool
	}{
		{"numeric id", "42", true},
		{"encoded namespace path", "group%2Fproj", true},
		{"dotted encoded path", "acme.co%2Fweb-app_v2", true},
		{"query injection", "42/variables?x=y", false},
		{"path traversal", "42/../admin", false},
		{"whitespace", "42 foo", false},
		{"raw slash", "group/proj", false},
		{"fragment", "42#frag", false},
		{"ampersand", "42&private_token=x", false},
		{"empty", "", false},
	}

	guard := gitlabProvider{hc: &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		t.Fatal("no HTTP call should be made for an invalid project id")
		return nil, nil
	})}}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// validateInput enforcement (creds present, only project varies).
			err := validateInput(ProviderGitLab, Addr{Project: tc.project}, Creds{Token: "t"})
			if tc.wantOK && err != nil {
				t.Errorf("validateInput(%q) = %v, want nil", tc.project, err)
			}
			if !tc.wantOK && err != ErrInvalidConfig {
				t.Errorf("validateInput(%q) = %v, want ErrInvalidConfig", tc.project, err)
			}

			// variablesBase defensive enforcement.
			url, uerr := guard.variablesBase(Addr{Project: tc.project})
			if tc.wantOK {
				if uerr != nil {
					t.Errorf("variablesBase(%q) = %v, want nil", tc.project, uerr)
				}
				want := "/api/v4/projects/" + tc.project + "/variables"
				if !strings.HasSuffix(url, want) {
					t.Errorf("variablesBase(%q) url = %q, want suffix %q", tc.project, url, want)
				}
			} else if uerr != ErrInvalidConfig {
				t.Errorf("variablesBase(%q) = %v, want ErrInvalidConfig", tc.project, uerr)
			}
		})
	}
}

// roundTripFunc adapts a func to http.RoundTripper.
type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
