package secretsync

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"

	"golang.org/x/crypto/nacl/box"
)

// ghTestServer sets up a fake GitHub API keyed off a real NaCl keypair so tests
// can decrypt what the provider seals. It returns the provider, the private key
// (for OpenAnonymous), the public key, and capture maps.
type ghCaptures struct {
	puts    map[string]map[string]string // NAME -> {"encrypted_value","key_id"}
	deletes []string                     // paths of DELETE requests
	putPath []string                     // full paths of PUT requests
	pkPath  string                       // path of the public-key GET
}

func newGHTestServer(t *testing.T) (githubProvider, *[32]byte, *[32]byte, *ghCaptures, *httptest.Server) {
	t.Helper()
	pub, priv, err := box.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	caps := &ghCaptures{puts: map[string]map[string]string{}}

	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/public-key"):
			caps.pkPath = r.URL.Path
			_ = json.NewEncoder(w).Encode(ghPublicKey{
				KeyID: "kid-1",
				Key:   base64.StdEncoding.EncodeToString(pub[:]),
			})
		case r.Method == http.MethodPut:
			caps.putPath = append(caps.putPath, r.URL.Path)
			name := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
			var body map[string]string
			_ = json.NewDecoder(r.Body).Decode(&body)
			caps.puts[name] = body
			w.WriteHeader(http.StatusCreated)
		case r.Method == http.MethodDelete:
			caps.deletes = append(caps.deletes, r.URL.Path)
			w.WriteHeader(http.StatusNoContent)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	})
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)

	g := githubProvider{hc: srv.Client(), baseURL: srv.URL}
	return g, priv, pub, caps, srv
}

func TestGitHubApplySealsAndPuts(t *testing.T) {
	g, priv, pub, caps, _ := newGHTestServer(t)

	desired := map[string]string{"API_KEY": "s3cret", "DB_URL": "postgres://x"}
	res, err := g.Apply(context.Background(), Creds{PAT: "ghp_x"},
		Addr{Owner: "o", Repo: "r"}, desired, nil, true)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}

	sort.Strings(res.Applied)
	if len(res.Applied) != 2 || res.Applied[0] != "API_KEY" || res.Applied[1] != "DB_URL" {
		t.Fatalf("Applied = %v, want [API_KEY DB_URL]", res.Applied)
	}

	for name, want := range desired {
		body, ok := caps.puts[name]
		if !ok {
			t.Fatalf("no PUT captured for %q", name)
		}
		if body["key_id"] != "kid-1" {
			t.Errorf("%s key_id = %q, want kid-1", name, body["key_id"])
		}
		sealed, err := base64.StdEncoding.DecodeString(body["encrypted_value"])
		if err != nil {
			t.Fatalf("%s decode encrypted_value: %v", name, err)
		}
		opened, ok := box.OpenAnonymous(nil, sealed, pub, priv)
		if !ok {
			t.Fatalf("%s OpenAnonymous failed", name)
		}
		if string(opened) != want {
			t.Errorf("%s decrypted = %q, want %q", name, opened, want)
		}
	}
}

func TestGitHubSkipsInvalidNames(t *testing.T) {
	g, _, _, caps, _ := newGHTestServer(t)

	desired := map[string]string{"bad-name": "v", "github_x": "v", "OK": "v"}
	res, err := g.Apply(context.Background(), Creds{PAT: "ghp_x"},
		Addr{Owner: "o", Repo: "r"}, desired, nil, false)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if res.Skipped["bad-name"] == "" {
		t.Errorf("bad-name not skipped")
	}
	if res.Skipped["github_x"] == "" {
		t.Errorf("github_x not skipped")
	}
	if len(res.Applied) != 1 || res.Applied[0] != "OK" {
		t.Fatalf("Applied = %v, want [OK]", res.Applied)
	}
	if _, ok := caps.puts["OK"]; !ok {
		t.Errorf("no PUT for OK")
	}
	if len(caps.puts) != 1 {
		t.Errorf("puts = %v, want only OK", caps.puts)
	}
}

func TestGitHubPrunesManagedKeys(t *testing.T) {
	g, _, _, caps, _ := newGHTestServer(t)

	res, err := g.Apply(context.Background(), Creds{PAT: "ghp_x"},
		Addr{Owner: "o", Repo: "r"}, map[string]string{"KEEP": "v"},
		[]string{"OLD", "KEEP"}, true)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if len(res.Applied) != 1 || res.Applied[0] != "KEEP" {
		t.Fatalf("Applied = %v, want [KEEP]", res.Applied)
	}
	var deletedOld, deletedKeep bool
	for _, p := range caps.deletes {
		if strings.HasSuffix(p, "/secrets/OLD") {
			deletedOld = true
		}
		if strings.HasSuffix(p, "/secrets/KEEP") {
			deletedKeep = true
		}
	}
	if !deletedOld {
		t.Errorf("OLD not deleted; deletes = %v", caps.deletes)
	}
	if deletedKeep {
		t.Errorf("KEEP wrongly deleted; deletes = %v", caps.deletes)
	}
}

func TestGitHubPruneFalseNoDelete(t *testing.T) {
	g, _, _, caps, _ := newGHTestServer(t)

	_, err := g.Apply(context.Background(), Creds{PAT: "ghp_x"},
		Addr{Owner: "o", Repo: "r"}, map[string]string{"KEEP": "v"},
		[]string{"OLD", "KEEP"}, false)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if len(caps.deletes) != 0 {
		t.Errorf("expected no DELETEs, got %v", caps.deletes)
	}
}

func TestGitHubEnvironmentPath(t *testing.T) {
	g, _, _, caps, _ := newGHTestServer(t)

	_, err := g.Apply(context.Background(), Creds{PAT: "ghp_x"},
		Addr{Owner: "o", Repo: "r", Environment: "prod"},
		map[string]string{"OK": "v"}, nil, false)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if !strings.Contains(caps.pkPath, "/environments/prod/") {
		t.Errorf("public-key path = %q, want /environments/prod/", caps.pkPath)
	}
	if len(caps.putPath) != 1 || !strings.Contains(caps.putPath[0], "/environments/prod/") {
		t.Errorf("put paths = %v, want /environments/prod/", caps.putPath)
	}
}

func TestGitHubMissingConfig(t *testing.T) {
	g, _, _, caps, _ := newGHTestServer(t)

	// Empty PAT.
	if _, err := g.Apply(context.Background(), Creds{},
		Addr{Owner: "o", Repo: "r"}, map[string]string{"OK": "v"}, nil, false); err != ErrInvalidConfig {
		t.Errorf("empty PAT: err = %v, want ErrInvalidConfig", err)
	}
	// Empty Owner.
	if _, err := g.Apply(context.Background(), Creds{PAT: "ghp_x"},
		Addr{Repo: "r"}, map[string]string{"OK": "v"}, nil, false); err != ErrInvalidConfig {
		t.Errorf("empty Owner: err = %v, want ErrInvalidConfig", err)
	}
	if caps.pkPath != "" || len(caps.putPath) != 0 || len(caps.deletes) != 0 {
		t.Errorf("expected no HTTP calls; pk=%q puts=%v deletes=%v", caps.pkPath, caps.putPath, caps.deletes)
	}
}
