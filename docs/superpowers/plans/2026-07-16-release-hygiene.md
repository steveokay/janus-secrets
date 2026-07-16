# Release Hygiene Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make Janus releasable — Apache-2.0 licensing, a hand-authored OpenAPI 3.1 spec kept honest by a route-walking drift test, goreleaser-based multi-arch binary + GHCR image publishing on tag, and a production deployment guide.

**Architecture:** Mostly additive files (LICENSE, NOTICE, `docs/openapi.yaml`, `.goreleaser.yaml`, `Dockerfile.release`, `CHANGELOG.md`, `.github/workflows/release.yml`, `docs/guides/production-deployment.md`). One new package `internal/version` (build vars set via ldflags), one new authenticated endpoint `GET /v1/sys/version`, and one new test `internal/api/openapi_drift_test.go` that boots a full server and asserts every registered chi route is documented.

**Tech Stack:** Go 1.26.5 (toolchain), cobra CLI, go-chi/chi v5, `gopkg.in/yaml.v3` (already a dep), goreleaser, GitHub Actions, GHCR.

**Spec:** `docs/superpowers/specs/2026-07-16-release-hygiene-design.md`.

## Shared facts (verified against the code — trust these)

- `cmd/janus/main.go:11` declares `var version = "dev"`; `newVersionCmd()` (lines 56–64) prints `janus <version>`. Module path: `github.com/steveokay/janus-secrets`.
- `gopkg.in/yaml.v3 v3.0.1` and `github.com/go-chi/chi/v5 v5.3.0` are already dependencies.
- `internal/api/server.go`: `New(...)` builds `r := chi.NewRouter()`, mounts `/v1/sys` and all other routes (many conditional on non-nil services), stores `s.router = r`, and `func (s *Server) Handler() http.Handler { return s.router }`. The dynamic type is `*chi.Mux`, which implements `chi.Routes`.
- `/v1/sys` routes are registered inside `r.Route("/v1/sys", func(r chi.Router){ … })` (server.go:122). Authenticated sys routes use `r.With(RequireAuth(s.auth), s.requireInstance(authz.X, "action", "resource")).Method(...)`. Public sys routes (health/live/ready/seal-status) use bare `r.Get(...)`.
- `internal/api/boot_test.go`: `Boot(ctx, BootConfig{DatabaseURL: dsn, SealType: crypto.SealTypeShamir})` returns `(*Server, *store.Store, error)` fully wired (auth, authz, transit, rotation, sync, dynamic, projectKeys, masterKeys, promote all non-nil). `bootPostgres(t)` starts a throwaway Postgres via testcontainers and `t.Skip`s if docker is unavailable.
- `internal/api/harness_test.go`: `newShamirTestServer(t)` returns a `*Server`, `*httptest.Server`, `*memSealStore` wired with an in-memory seal store and **nil** services (only sys/minimal routes mount). `doJSON(t, method, url, body, out) int` issues a request and returns the status code. `errEnvelope` decodes `{error:{code,message}}`.
- The error envelope is `{"error":{"code":"…","message":"…"}}`.
- CI is `.github/workflows/ci.yml` (Go + web jobs).

---

### Task 1: Apache-2.0 licensing

**Files:**
- Create: `LICENSE`, `NOTICE`
- Modify: `README.md` (§License)
- Test: `internal/meta/license_test.go` (new package `meta`, repo-root-relative)

- [ ] **Step 1: Write the failing test**

Create `internal/meta/license_test.go`:

```go
package meta

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// repoRoot walks up from this test file to the module root (the dir with go.mod).
func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 10; i++ {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		dir = filepath.Dir(dir)
	}
	t.Fatal("go.mod not found walking up from test dir")
	return ""
}

func TestApacheLicensePresent(t *testing.T) {
	root := repoRoot(t)
	b, err := os.ReadFile(filepath.Join(root, "LICENSE"))
	if err != nil {
		t.Fatalf("LICENSE missing: %v", err)
	}
	if !strings.Contains(string(b), "Apache License") || !strings.Contains(string(b), "Version 2.0") {
		t.Fatal("LICENSE is not Apache-2.0")
	}
	if _, err := os.Stat(filepath.Join(root, "NOTICE")); err != nil {
		t.Fatalf("NOTICE missing: %v", err)
	}
	// The vendored Shamir MPL license must remain in place.
	if _, err := os.Stat(filepath.Join(root, "internal", "crypto", "shamir", "LICENSE")); err != nil {
		t.Fatalf("vendored shamir LICENSE removed: %v", err)
	}
	readme, err := os.ReadFile(filepath.Join(root, "README.md"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(readme), "Not yet chosen") {
		t.Fatal("README still says the license is 'Not yet chosen'")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/meta/ -run TestApacheLicensePresent`
Expected: FAIL — `LICENSE missing`.

- [ ] **Step 3: Create `LICENSE`**

Write the **verbatim Apache License 2.0** text to `/LICENSE`. Source the canonical text from https://www.apache.org/licenses/LICENSE-2.0.txt (the standard ~202-line text beginning `                                 Apache License` / `                           Version 2.0, January 2004`). Fill the copyright placeholder in the appendix boilerplate is NOT required (Apache keeps the appendix generic); do not modify the license body.

- [ ] **Step 4: Create `NOTICE`**

```
Janus — self-hosted, single-tenant secrets manager
Copyright 2026 The Janus Authors

This product includes software developed by The Janus Authors,
licensed under the Apache License, Version 2.0 (see LICENSE).

This product bundles a vendored copy of a Shamir Secret Sharing
implementation in internal/crypto/shamir/, which is licensed under the
Mozilla Public License, Version 2.0 (MPL-2.0). The original license and
per-file headers are retained in that directory
(see internal/crypto/shamir/LICENSE). MPL-2.0 is file-level copyleft and
is compatible with distribution of this project under Apache-2.0.
```

- [ ] **Step 5: Update `README.md` §License**

Locate the `## License` section (currently: "Not yet chosen. The vendored `internal/crypto/shamir/` package is under MPL-2.0 …"). Replace its body with:

```markdown
Janus is licensed under the **Apache License, Version 2.0** — see
[`LICENSE`](LICENSE) and [`NOTICE`](NOTICE).

The vendored `internal/crypto/shamir/` package is licensed under
**MPL-2.0** (see its `LICENSE`); its per-file headers are retained. MPL-2.0
is file-level copyleft and compatible with Apache-2.0 distribution.
```

- [ ] **Step 6: Run test to verify it passes**

Run: `go test ./internal/meta/ -run TestApacheLicensePresent`
Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add LICENSE NOTICE README.md internal/meta/license_test.go
git commit -m "chore: license Janus under Apache-2.0 (LICENSE + NOTICE)"
```

---

### Task 2: `internal/version` package + CLI wiring

**Files:**
- Create: `internal/version/version.go`, `internal/version/version_test.go`
- Modify: `cmd/janus/main.go`

- [ ] **Step 1: Write the failing test**

Create `internal/version/version_test.go`:

```go
package version

import "testing"

func TestDefaults(t *testing.T) {
	// Unset (non-release) builds report "dev"; ldflags override at release time.
	if Version == "" {
		t.Fatal("Version must have a non-empty default")
	}
	if String() == "" {
		t.Fatal("String() must render something")
	}
}

func TestStringFormat(t *testing.T) {
	Version, Commit, Date = "1.2.3", "abc1234", "2026-07-16T00:00:00Z"
	got := String()
	for _, want := range []string{"1.2.3", "abc1234", "2026-07-16T00:00:00Z"} {
		if !contains(got, want) {
			t.Fatalf("String()=%q missing %q", got, want)
		}
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || indexOf(s, sub) >= 0)
}
func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/version/`
Expected: FAIL — package/symbols undefined.

- [ ] **Step 3: Create `internal/version/version.go`**

```go
// Package version carries build metadata injected at release time via
// -ldflags "-X github.com/steveokay/janus-secrets/internal/version.Version=…".
package version

import "fmt"

// These are overridden at build time by goreleaser ldflags. Non-release
// builds report the "dev" defaults.
var (
	Version = "dev"
	Commit  = "none"
	Date    = "unknown"
)

// String renders a single human line: "<version> (commit <commit>, built <date>)".
func String() string {
	return fmt.Sprintf("%s (commit %s, built %s)", Version, Commit, Date)
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/version/`
Expected: PASS.

- [ ] **Step 5: Wire the CLI to `internal/version`**

In `cmd/janus/main.go`: delete the line `var version = "dev"` (line 11) and add the import `"github.com/steveokay/janus-secrets/internal/version"`. Replace `newVersionCmd`'s body:

```go
func newVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the janus version",
		Run: func(cmd *cobra.Command, _ []string) {
			fmt.Fprintln(cmd.OutOrStdout(), "janus", version.String())
		},
	}
}
```

- [ ] **Step 6: Verify build + existing CLI tests**

Run: `go build ./... && go test ./cmd/janus/ -run 'Version'`
Expected: build clean; any existing version test still passes (adjust it if it asserted the exact old string `janus dev` — the new output is `janus dev (commit none, built unknown)`; update that assertion to check for `"janus"` and `"dev"` substrings if present).

- [ ] **Step 7: Commit**

```bash
git add internal/version/ cmd/janus/main.go
git commit -m "feat(version): internal/version build-info package; wire janus version"
```

---

### Task 3: `GET /v1/sys/version` endpoint (authenticated)

**Files:**
- Create: `internal/api/version_handler.go`
- Modify: `internal/api/server.go` (mount the route)
- Test: `internal/api/version_handler_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/api/version_handler_test.go`:

```go
package api

import (
	"net/http/httptest"
	"testing"
)

// The version endpoint returns build metadata and must not be anonymous.
// newShamirTestServer wires a nil auth service, so the route is mounted in the
// unauthenticated branch there; to exercise the auth requirement we assert the
// handler itself renders the version package fields when called directly.
func TestVersionHandlerRendersBuildInfo(t *testing.T) {
	srv, ts, _ := newShamirTestServer(t)
	_ = ts
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/v1/sys/version", nil)
	srv.handleVersion(rec, req)
	if rec.Code != 200 {
		t.Fatalf("status=%d", rec.Code)
	}
	var body struct{ Version, Commit, Date string }
	decodeBody(t, rec.Body.Bytes(), &body)
	if body.Version == "" {
		t.Fatalf("empty version in %s", rec.Body.String())
	}
}
```

Add this helper to the same file (small, local):

```go
import "encoding/json"

func decodeBody(t *testing.T, b []byte, out any) {
	t.Helper()
	if err := json.Unmarshal(b, out); err != nil {
		t.Fatalf("decode %q: %v", string(b), err)
	}
}
```

(Combine the two import blocks into one at the top of the file.)

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/api/ -run TestVersionHandlerRendersBuildInfo`
Expected: FAIL — `srv.handleVersion` undefined.

- [ ] **Step 3: Create the handler**

Create `internal/api/version_handler.go`:

```go
package api

import (
	"net/http"

	"github.com/steveokay/janus-secrets/internal/version"
)

// handleVersion returns build metadata (version/commit/date). It is mounted
// under RequireAuth in production so build details are not exposed anonymously.
func (s *Server) handleVersion(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{
		"version": version.Version,
		"commit":  version.Commit,
		"date":    version.Date,
	})
}
```

(`writeJSON` is the existing response helper used across `internal/api`.)

- [ ] **Step 4: Mount the route**

In `internal/api/server.go`, inside `r.Route("/v1/sys", …)`: in the **authenticated** branch (`if s.auth != nil && s.authz != nil { … }`), add next to the other authed sys routes:

```go
r.With(RequireAuth(s.auth)).Get("/version", s.handleVersion)
```

And in the **else** branch (nil-auth unit-test servers, which register bare routes), add:

```go
r.Get("/version", s.handleVersion)
```

so `newShamirTestServer` (nil auth) mounts it unauthenticated for the direct-call test and the drift test.

- [ ] **Step 5: Run test to verify it passes**

Run: `go test ./internal/api/ -run TestVersionHandlerRendersBuildInfo`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/api/version_handler.go internal/api/version_handler_test.go internal/api/server.go
git commit -m "feat(api): authenticated GET /v1/sys/version build-info endpoint"
```

---

### Task 4: OpenAPI drift-guard test (write first — it enumerates the work)

**Files:**
- Create: `internal/api/openapi_drift_test.go`

This test boots a full server, walks every registered route, and asserts each is documented in `docs/openapi.yaml`. Writing it first makes its failure output the authoritative checklist for Task 5.

- [ ] **Step 1: Write the drift test**

Create `internal/api/openapi_drift_test.go`:

```go
package api

import (
	"context"
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

// routeKey is "METHOD path".
func collectRoutes(t *testing.T, r chi.Routes) map[string]bool {
	t.Helper()
	got := map[string]bool{}
	err := chi.Walk(r, func(method, route string, _ chi.Handler, _ ...func(chi.Handler) chi.Handler) error {
		p := normalizePath(route)
		// Only the documented API surface: /v1/*. The SPA fallback and asset
		// routes are not part of the API contract.
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

// specRoutes parses docs/openapi.yaml -> set of "METHOD path".
func specRoutes(t *testing.T) map[string]bool {
	t.Helper()
	root := repoRootFromAPI(t)
	b, err := os.ReadFile(filepath.Join(root, "docs", "openapi.yaml"))
	if err != nil {
		// Missing file => zero documented routes; the diff below lists everything.
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
	dsn := bootPostgres(t) // t.Skip if docker unavailable
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
```

- [ ] **Step 2: Run the drift test — capture the full route list**

Run: `go test ./internal/api/ -run TestOpenAPINoDrift -v`
Expected: FAIL, printing every `METHOD /v1/...` route as "NOT documented". **Save this list** — it is the exact inventory Task 5 must document. (If docker is unavailable the test skips; run it where testcontainers works, e.g. CI or the dev machine with Docker Desktop.)

- [ ] **Step 3: Commit the test (red)**

```bash
git add internal/api/openapi_drift_test.go
git commit -m "test(api): OpenAPI drift guard — assert every /v1 route is documented"
```

---

### Task 5: Author `docs/openapi.yaml` until the drift test passes

**Files:**
- Create: `docs/openapi.yaml`

Use the route inventory from Task 4 Step 2 as the checklist. Document every `METHOD /v1/...` route. Follow the template below exactly for structure; replicate the per-path pattern for each route group (projects, environments, configs, secrets, versions, tokens, auth, oidc, audit, transit, rotation, sync, dynamic, sys, trash, promote, pipeline, kek, metrics).

- [ ] **Step 1: Create the spec skeleton + components**

Create `docs/openapi.yaml`:

```yaml
openapi: 3.1.0
info:
  title: Janus API
  version: "0.1.0"
  description: >
    Self-hosted, single-tenant secrets manager. All routes are under /v1.
    Authenticate with a service token (Authorization: Bearer janus_svc_…) or a
    UI session cookie. Secret values are never returned in list/metadata views.
  license:
    name: Apache-2.0
    url: https://www.apache.org/licenses/LICENSE-2.0
servers:
  - url: /
components:
  securitySchemes:
    bearerAuth:
      type: http
      scheme: bearer
      description: Service token (janus_svc_… / janus_user_…).
    sessionCookie:
      type: apiKey
      in: cookie
      name: janus_session
  schemas:
    Error:
      type: object
      required: [error]
      properties:
        error:
          type: object
          required: [code, message]
          properties:
            code: { type: string, example: forbidden }
            message: { type: string, example: insufficient permissions }
    Project:
      type: object
      properties:
        id: { type: string, format: uuid }
        slug: { type: string, example: acme-web }
        name: { type: string, example: Acme Web }
        created_at: { type: string, format: date-time }
    Environment:
      type: object
      properties:
        id: { type: string, format: uuid }
        project_id: { type: string, format: uuid }
        slug: { type: string, example: prod }
        name: { type: string, example: Production }
        created_at: { type: string, format: date-time }
    Config:
      type: object
      properties:
        id: { type: string, format: uuid }
        environment_id: { type: string, format: uuid }
        name: { type: string, example: prod }
        inherits_from: { type: [string, "null"], format: uuid }
        created_at: { type: string, format: date-time }
    TokenMeta:
      type: object
      description: Token metadata. The raw token is returned only once, at mint.
      properties:
        id: { type: string, example: tok_… }
        name: { type: string, example: ci-deploy }
        scope_kind: { type: string, enum: [config, environment] }
        scope_id: { type: string, format: uuid }
        access: { type: string, enum: [read, readwrite] }
        expires_at: { type: [string, "null"], format: date-time }
  parameters:
    Limit:
      name: limit
      in: query
      required: false
      schema: { type: integer, minimum: 1 }
      description: Opt-in cursor pagination page size. Omit for the full list.
    Cursor:
      name: cursor
      in: query
      required: false
      schema: { type: string }
  responses:
    Unauthorized:
      description: Missing or invalid credentials.
      content:
        application/json:
          schema: { $ref: '#/components/schemas/Error' }
    Forbidden:
      description: Authenticated but not authorized.
      content:
        application/json:
          schema: { $ref: '#/components/schemas/Error' }
    NotFound:
      description: Resource not found.
      content:
        application/json:
          schema: { $ref: '#/components/schemas/Error' }
security:
  - bearerAuth: []
  - sessionCookie: []
tags:
  - { name: projects }
  - { name: environments }
  - { name: configs }
  - { name: secrets }
  - { name: tokens }
  - { name: auth }
  - { name: audit }
  - { name: transit }
  - { name: rotation }
  - { name: sync }
  - { name: dynamic }
  - { name: sys }
  - { name: trash }
  - { name: promote }
  - { name: pipeline }
  - { name: kek }
  - { name: metrics }
paths: {}
```

- [ ] **Step 2: Document the first route group as the worked template (projects)**

Replace `paths: {}` with fully-worked project routes; this is the pattern to replicate for every other group:

```yaml
paths:
  /v1/projects:
    get:
      tags: [projects]
      summary: List projects
      parameters:
        - $ref: '#/components/parameters/Limit'
        - $ref: '#/components/parameters/Cursor'
      responses:
        "200":
          description: Projects (metadata only).
          content:
            application/json:
              schema:
                type: object
                properties:
                  projects:
                    type: array
                    items: { $ref: '#/components/schemas/Project' }
                  next_cursor: { type: [string, "null"] }
        "401": { $ref: '#/components/responses/Unauthorized' }
    post:
      tags: [projects]
      summary: Create a project
      requestBody:
        required: true
        content:
          application/json:
            schema:
              type: object
              required: [slug, name]
              properties:
                slug: { type: string, example: acme-web }
                name: { type: string, example: Acme Web }
      responses:
        "201":
          description: Created.
          content:
            application/json:
              schema: { $ref: '#/components/schemas/Project' }
        "401": { $ref: '#/components/responses/Unauthorized' }
        "403": { $ref: '#/components/responses/Forbidden' }
  /v1/projects/{pid}:
    parameters:
      - { name: pid, in: path, required: true, schema: { type: string, format: uuid } }
    get:
      tags: [projects]
      summary: Get a project
      responses:
        "200":
          description: OK
          content:
            application/json:
              schema: { $ref: '#/components/schemas/Project' }
        "404": { $ref: '#/components/responses/NotFound' }
    delete:
      tags: [projects]
      summary: Soft-delete a project
      responses:
        "204": { description: Deleted. }
        "403": { $ref: '#/components/responses/Forbidden' }
  /v1/projects/{pid}/restore:
    parameters:
      - { name: pid, in: path, required: true, schema: { type: string, format: uuid } }
    post:
      tags: [projects]
      summary: Restore a soft-deleted project
      responses:
        "200": { description: Restored. }
        "404": { $ref: '#/components/responses/NotFound' }
```

- [ ] **Step 3: Document every remaining route group**

For each `METHOD /v1/...` from the Task 4 inventory not yet covered, add a path item following the template. Keep it faithful but concise: each operation needs `tags`, `summary`, path/query params, a primary success response, and at least the relevant `401`/`403`/`404` refs. Reuse `#/components/schemas/*` where a schema exists; for bodies/responses without a dedicated schema, an inline `type: object` with the key fields is sufficient. **Never put a real secret value, token, or credential in an example** — for secret-bearing request fields use `writeOnly: true` and a placeholder like `"<secret value>"`; document the token-mint response's `token` field as returned once.

Route groups to cover (from server.go registrations): `/v1/projects/{pid}/environments` (+`/{eid}`, `/restore`), `/v1/projects/{pid}/environments/{eid}/configs`, `/v1/configs/{cid}` (+`/restore`, `/secrets`, `/secrets/{key}`, `/versions`, `/versions/diff`), `/v1/tokens` (+`/{id}`), `/v1/auth/*` (login, logout, me, password, oidc/status|login|callback|federate), `/v1/audit` (+`/verify`, `/export`), `/v1/transit/*`, `/v1/rotation/*`, `/v1/sync/*`, `/v1/dynamic/*`, `/v1/sys/*` (health, live, ready, seal-status, init, unseal, unseal/reset, restore, seal, backup, version, oidc, oidc/federation[/bindings], master-key[/rotate,/rekey/*]), `/v1/trash`, `/v1/promote*`, `/v1/pipeline*`, `/v1/projects/{pid}/kek*`, `/v1/metrics/*`.

- [ ] **Step 4: Run the drift test until green**

Run: `go test ./internal/api/ -run TestOpenAPINoDrift -v`
Iterate: the failure lists any route still missing (or any phantom spec path). Add/fix until it passes.
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add docs/openapi.yaml
git commit -m "docs(api): hand-authored OpenAPI 3.1 spec for all /v1 endpoints"
```

---

### Task 6: goreleaser config + release Dockerfile

**Files:**
- Create: `.goreleaser.yaml`, `Dockerfile.release`

- [ ] **Step 1: Create `Dockerfile.release`**

goreleaser builds the binary; this image just wraps it. The web assets are already embedded in the binary at build time (release workflow runs `npm run build` before goreleaser — Task 8).

```dockerfile
# Release image: wraps the goreleaser-built binary. Web assets are embedded in
# the binary via go:embed at build time. The multi-stage dev Dockerfile (used by
# `docker compose up --build`) is separate and unchanged.
FROM gcr.io/distroless/static-debian12:nonroot
COPY janus /usr/local/bin/janus
EXPOSE 8200
ENTRYPOINT ["/usr/local/bin/janus"]
CMD ["server"]
```

- [ ] **Step 2: Create `.goreleaser.yaml`**

```yaml
version: 2
project_name: janus
before:
  hooks:
    - go mod tidy
builds:
  - id: janus
    main: ./cmd/janus
    binary: janus
    env:
      - CGO_ENABLED=0
    goos: [linux, darwin, windows]
    goarch: [amd64, arm64]
    ldflags:
      - -s -w
      - -X github.com/steveokay/janus-secrets/internal/version.Version={{.Version}}
      - -X github.com/steveokay/janus-secrets/internal/version.Commit={{.ShortCommit}}
      - -X github.com/steveokay/janus-secrets/internal/version.Date={{.Date}}
archives:
  - id: janus
    formats: [tar.gz]
    format_overrides:
      - goos: windows
        formats: [zip]
    files:
      - LICENSE
      - NOTICE
      - README.md
checksum:
  name_template: checksums.txt
  algorithm: sha256
dockers:
  - id: janus-amd64
    goos: linux
    goarch: amd64
    dockerfile: Dockerfile.release
    image_templates:
      - "ghcr.io/steveokay/janus:{{ .Version }}-amd64"
    use: buildx
    build_flag_templates:
      - "--platform=linux/amd64"
  - id: janus-arm64
    goos: linux
    goarch: arm64
    dockerfile: Dockerfile.release
    image_templates:
      - "ghcr.io/steveokay/janus:{{ .Version }}-arm64"
    use: buildx
    build_flag_templates:
      - "--platform=linux/arm64"
docker_manifests:
  - name_template: "ghcr.io/steveokay/janus:{{ .Version }}"
    image_templates:
      - "ghcr.io/steveokay/janus:{{ .Version }}-amd64"
      - "ghcr.io/steveokay/janus:{{ .Version }}-arm64"
  - name_template: "ghcr.io/steveokay/janus:latest"
    image_templates:
      - "ghcr.io/steveokay/janus:{{ .Version }}-amd64"
      - "ghcr.io/steveokay/janus:{{ .Version }}-arm64"
changelog:
  use: github
  sort: asc
  groups:
    - { title: Features, regexp: "^feat", order: 0 }
    - { title: Fixes, regexp: "^fix", order: 1 }
    - { title: Others, order: 999 }
```

- [ ] **Step 3: Validate the config (no build)**

Run: `goreleaser check`
Expected: `configuration is valid`. (If goreleaser is not installed locally, this is validated in CI by Task 8; note that in the commit message. Install via `go install github.com/goreleaser/goreleaser/v2@latest` if available.)

- [ ] **Step 4: Snapshot build smoke (optional, if goreleaser present)**

Run: `goreleaser build --snapshot --clean --single-target`
Expected: a `dist/` binary is produced. This confirms ldflags/build wiring. Delete `dist/` after (it is gitignored; add `dist/` to `.gitignore` if not already present).

- [ ] **Step 5: Commit**

```bash
git add .goreleaser.yaml Dockerfile.release .gitignore
git commit -m "build: goreleaser config + release Dockerfile (multi-arch binaries + GHCR image)"
```

---

### Task 7: CHANGELOG

**Files:**
- Create: `CHANGELOG.md`

- [ ] **Step 1: Create `CHANGELOG.md`**

```markdown
# Changelog

All notable changes to this project are documented here. The format is based on
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and this project
adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

## [0.1.0] - 2026-07-16

First tagged release. Feature-complete across build Phases 1–3.

### Added
- **Core (Phase 1):** envelope-encryption key hierarchy with Shamir and cloud-KMS
  unseal; PostgreSQL store + migrations; Project → Environment → Config → Secret
  model with two-level (config + per-key) versioning, soft-delete/restore, config
  inheritance, and cross-config secret references; password + service-token auth;
  RBAC (viewer/developer/admin/owner); hash-chained audit log; REST API; `janus`
  CLI with `run` secret injection.
- **Transit + UI (Phase 2):** transit engine (encrypt/decrypt/sign/verify/rewrap,
  key versioning); React SPA (Nocturne design) covering projects, the secret
  editor, audit viewer, token/member management, transit, settings, operations,
  and an integrations hub; OIDC login and CI federation; reads-24h usage metrics.
- **Rotation + dynamic (Phase 3):** scheduled static rotation (Postgres + webhook);
  sync integrations (GitHub Actions, Kubernetes Secrets); dynamic Postgres
  credentials with a lease manager.
- **Hardening & depth:** project-KEK and master-key rotation; cursor pagination;
  Idempotency-Key middleware; HTTP timeouts/body caps; trash/restore, per-key
  history, and audit expand/timeline UI; a self-sufficient CLI control plane
  (project/env/config CRUD, token mint/list/revoke, whoami, completion, diff).
- **Release:** Apache-2.0 license; OpenAPI 3.1 spec; goreleaser multi-arch
  binaries + GHCR image; production deployment guide.

[Unreleased]: https://github.com/steveokay/janus-secrets/compare/v0.1.0...HEAD
[0.1.0]: https://github.com/steveokay/janus-secrets/releases/tag/v0.1.0
```

- [ ] **Step 2: Commit**

```bash
git add CHANGELOG.md
git commit -m "docs: add CHANGELOG (Keep a Changelog), seed 0.1.0"
```

---

### Task 8: Release workflow + CI goreleaser check

**Files:**
- Create: `.github/workflows/release.yml`
- Modify: `.github/workflows/ci.yml`

- [ ] **Step 1: Create `.github/workflows/release.yml`**

```yaml
name: release
on:
  push:
    tags: ["v*"]
permissions:
  contents: write
  packages: write
  id-token: write
jobs:
  goreleaser:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
        with:
          fetch-depth: 0
      - uses: actions/setup-go@v5
        with:
          go-version: "1.26.5"
      - uses: actions/setup-node@v4
        with:
          node-version: "20"
      - name: Build web assets (embedded into the binary)
        run: |
          cd web
          npm ci
          npm run build
      - uses: docker/setup-qemu-action@v3
      - uses: docker/setup-buildx-action@v3
      - name: Log in to GHCR
        uses: docker/login-action@v3
        with:
          registry: ghcr.io
          username: ${{ github.actor }}
          password: ${{ secrets.GITHUB_TOKEN }}
      - uses: goreleaser/goreleaser-action@v6
        with:
          version: "~> v2"
          args: release --clean
        env:
          GITHUB_TOKEN: ${{ secrets.GITHUB_TOKEN }}
```

- [ ] **Step 2: Add a `goreleaser check` job to `ci.yml`**

Open `.github/workflows/ci.yml` and add a job (mirroring the existing jobs' `runs-on`/checkout style):

```yaml
  goreleaser-check:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with:
          go-version: "1.26.5"
      - uses: goreleaser/goreleaser-action@v6
        with:
          version: "~> v2"
          args: check
```

(Match the exact indentation/`jobs:` structure of the existing file — read it first and append under `jobs:`.)

- [ ] **Step 3: Verify workflow YAML is well-formed**

Run: `go run ./... 2>/dev/null; python -c "import yaml,sys; yaml.safe_load(open('.github/workflows/release.yml')); yaml.safe_load(open('.github/workflows/ci.yml')); print('yaml ok')"`
Expected: `yaml ok`. (If python is unavailable, visually verify indentation; the `goreleaser check` job runs on the PR to catch config errors.)

- [ ] **Step 4: Commit**

```bash
git add .github/workflows/release.yml .github/workflows/ci.yml
git commit -m "ci: release workflow (goreleaser -> binaries + GHCR) + PR goreleaser check"
```

---

### Task 9: Production deployment guide

**Files:**
- Create: `docs/guides/production-deployment.md`
- Modify: `README.md` (link), `docs/README.md` (link)

- [ ] **Step 1: Gather the `JANUS_*` env var inventory**

Run: `grep -rhoE 'JANUS_[A-Z_]+' cmd/ internal/ | sort -u`
Use the result to fill the configuration table in the guide (do not invent variables — list only those that appear in the code).

- [ ] **Step 2: Create `docs/guides/production-deployment.md`**

Write the guide with these sections (prose + concrete examples; keep it accurate to the codebase):

1. **Overview** — single node + Postgres; no HA by design; server is TLS-less and expects a reverse proxy.
2. **TLS termination** — a Caddy example and an nginx `location / { proxy_pass http://janus:8200; }` example terminating TLS in front of Janus.
3. **Configuration** — a table of the `JANUS_*` variables from Step 1 (name, meaning, default), grouped (address/DB, unseal, HTTP timeouts, scheduler ticks).
4. **Unseal in production** — Shamir (n-of-m ceremony via `janus unseal`) vs cloud-KMS auto-unseal (`JANUS_SEAL_TYPE`); server boots sealed and returns 503 until unsealed.
5. **Running the image** — `docker pull ghcr.io/steveokay/janus:<tag>` and a minimal compose snippet (app + Postgres); reference `docker-compose.yml` for the full local stack.
6. **Sizing** — rough CPU/memory/DB-connection guidance for a single node.
7. **Backups** — link `docs/ops/backup-restore.md` for the key-preserving app dump; note Postgres-level backup/WAL archiving for the underlying store.
8. **Upgrades** — migrations run on startup; pin an image tag; back up first; roll forward; no rolling/HA story.
9. **Monitoring** — `GET /v1/sys/health` and authenticated `GET /v1/sys/version`; reads-24h metric; note Prometheus `/metrics` is not yet available (tracked as gaps §7.6).

- [ ] **Step 3: Link the guide**

- In `README.md`, add a link to `docs/guides/production-deployment.md` (near existing deployment/docs references).
- In `docs/README.md`, add links to both `guides/production-deployment.md` and `openapi.yaml`.

- [ ] **Step 4: Commit**

```bash
git add docs/guides/production-deployment.md README.md docs/README.md
git commit -m "docs: production deployment guide; link OpenAPI spec + guide"
```

---

### Task 10: Full verification + tracker + memory

**Files:** `gaps.md` (mark done); verification only otherwise.

- [ ] **Step 1: Full build + tests**

Run: `go build ./... && go test ./...`
Expected: all PASS (the api package runs testcontainers Postgres — the drift test skips if docker is absent; run where Docker is available so it executes).

- [ ] **Step 2: Race + leak**

Run: `go test -race ./cmd/janus/... && go test ./internal/api/ -run 'Leak' && go test ./cmd/janus/ -run 'Leak'`
Expected: all PASS (no secret values leaked; the new version endpoint and OpenAPI examples carry none).

- [ ] **Step 3: Security gates**

Run: `gosec ./... ; govulncheck ./...`
Expected: no new gosec findings; govulncheck clean under the pinned toolchain (the local `go1.25.11` stdlib-TLS artifact `GO-2026-5856` is a known local-only false positive — the repo pins `toolchain go1.26.5`).

- [ ] **Step 4: goreleaser config check**

Run: `goreleaser check` (if installed)
Expected: `configuration is valid`. (Otherwise rely on the CI `goreleaser-check` job.)

- [ ] **Step 5: Mark `gaps.md` §7 done**

In `gaps.md` §7, strike §7.2 (OpenAPI), §7.3 (LICENSE), §7.4 (release machinery), §7.5 (production deployment guide) with `**[DONE 2026-07-16]**` and a one-line note each. Update the suggested-priority list item #10 to done. Leave §7.6 (Prometheus), §7.7 (compose limits), §7.8 (CONTRIBUTING) open.

- [ ] **Step 6: Commit**

```bash
git add gaps.md
git commit -m "docs(gaps): mark §7.2-7.5 release hygiene done"
```

---

## Self-Review

**Spec coverage:**
- Component 1 Licensing → Task 1 (LICENSE, NOTICE, README, Shamir untouched).
- Component 2 OpenAPI + drift guard → Tasks 4 (test) + 5 (spec).
- Component 3 Release machinery: version wiring → Tasks 2 (package/CLI) + 3 (endpoint); goreleaser/Dockerfile → Task 6; CHANGELOG → Task 7; release workflow + CI check → Task 8.
- Component 4 Deployment guide → Task 9.
- Verification/tracker → Task 10.

**Placeholder scan:** No TBD/TODO. The one deliberately un-inlined artifact is the ~90-endpoint body of `docs/openapi.yaml` (Task 5) — this is intentional: the drift test (Task 4) generates the exact route inventory and gates completeness automatically, and Task 5 supplies the full skeleton + a fully-worked group as the replicable template. The Apache license text is referenced by canonical URL rather than pasted (202 lines of boilerplate); the test asserts its identifying markers.

**Type consistency:** `internal/version.{Version,Commit,Date}` and `String()` are defined in Task 2 and consumed by Task 3 (handler), Task 6 (ldflags paths use the full module path `github.com/steveokay/janus-secrets/internal/version`), and Task 2 (CLI). `handleVersion` is defined in Task 3 and mounted in the same task. `repoRoot`/`repoRootFromAPI` helpers are per-package (meta vs api) to avoid cross-package test imports. The drift test's `normalizePath` is used for both router and spec sides so the comparison is apples-to-apples.

**Open item to confirm during implementation:** the exact set of `/v1` routes (Task 4 Step 2 output) — trust the drift test's enumeration over any hand-list. If `Boot` mounts a route group conditionally on a service the test wiring leaves nil, it will not appear; `Boot` with a real Postgres wires the full production set, so this matches what ships.
