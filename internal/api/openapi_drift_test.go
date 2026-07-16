package api

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/steveokay/janus-secrets/internal/crypto"
	"gopkg.in/yaml.v3"
)

// normalizePath converts a chi route template to OpenAPI path style:
// strips trailing slashes (except root) and drops chi regex constraints,
// e.g. "/v1/x/{id:[0-9]+}/" -> "/v1/x/{id}".
func normalizePath(p string) string {
	var b strings.Builder
	for i := 0; i < len(p); i++ {
		if p[i] == '{' {
			j := strings.IndexByte(p[i:], '}')
			if j < 0 {
				b.WriteString(p[i:])
				break
			}
			seg := p[i : i+j+1] // "{id:[0-9]+}"
			name := seg[1 : len(seg)-1]
			if c := strings.IndexByte(name, ':'); c >= 0 {
				name = name[:c]
			}
			b.WriteString("{" + name + "}")
			i += j
			continue
		}
		b.WriteByte(p[i])
	}
	out := b.String()
	if len(out) > 1 {
		out = strings.TrimRight(out, "/")
	}
	return out
}

func collectRoutes(t *testing.T, r chi.Routes) map[string]bool {
	t.Helper()
	got := map[string]bool{}
	err := chi.Walk(r, func(method, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		p := normalizePath(route)
		if !strings.HasPrefix(p, "/v1/") {
			return nil
		}
		got[strings.ToUpper(method)+" "+p] = true
		return nil
	})
	if err != nil {
		t.Fatalf("chi.Walk: %v", err)
	}
	return got
}

func specRoutes(t *testing.T) map[string]bool {
	t.Helper()
	root := repoRootFromAPI(t)
	b, err := os.ReadFile(filepath.Join(root, "docs", "openapi.yaml"))
	if err != nil {
		return map[string]bool{}
	}
	var doc struct {
		Paths map[string]map[string]any `yaml:"paths"`
	}
	if err := yaml.Unmarshal(b, &doc); err != nil {
		t.Fatalf("parse openapi.yaml: %v", err)
	}
	out := map[string]bool{}
	for path, ops := range doc.Paths {
		for method := range ops {
			m := strings.ToUpper(method)
			switch m {
			case "GET", "POST", "PUT", "PATCH", "DELETE", "HEAD", "OPTIONS":
				out[m+" "+normalizePath(path)] = true
			}
		}
	}
	return out
}

func repoRootFromAPI(t *testing.T) string {
	t.Helper()
	dir, _ := os.Getwd()
	for i := 0; i < 10; i++ {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		dir = filepath.Dir(dir)
	}
	t.Fatal("go.mod not found")
	return ""
}

func TestOpenAPINoDrift(t *testing.T) {
	dsn := bootPostgres(t)
	srv, st, err := Boot(context.Background(), BootConfig{
		DatabaseURL: dsn, SealType: crypto.SealTypeShamir,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	routes, ok := srv.Handler().(chi.Routes)
	if !ok {
		t.Fatal("server handler is not chi.Routes")
	}
	got := collectRoutes(t, routes)
	spec := specRoutes(t)

	var missing, extra []string
	for k := range got {
		if !spec[k] {
			missing = append(missing, k)
		}
	}
	for k := range spec {
		if !got[k] {
			extra = append(extra, k)
		}
	}
	sort.Strings(missing)
	sort.Strings(extra)
	if len(missing) > 0 {
		t.Errorf("%d routes NOT documented in docs/openapi.yaml:\n  %s",
			len(missing), strings.Join(missing, "\n  "))
	}
	if len(extra) > 0 {
		t.Errorf("%d documented paths not registered in the router (phantom):\n  %s",
			len(extra), strings.Join(extra, "\n  "))
	}
}
