# API Hardening Implementation Plan — Pagination, Idempotency, Server Timeouts

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Bring the REST surface up to CLAUDE.md's stated conventions: opt-in cursor pagination on the 7 table-backed list endpoints, a generic `Idempotency-Key` mechanism over all mutating verbs, and hardened `http.Server` timeouts + a global request-body cap.

**Architecture:** Three independent, additive, backward-compatible parts on one branch. Part 1 adds `ListPage` store variants (keyset on `created_at DESC, id DESC`) behind unchanged `List` delegates, an opaque base64url cursor codec, and a `next_cursor` field. Part 2 generalizes the promotion idempotency pattern into a global chi middleware that stores **status only, never response bodies** (so once-shown secrets never persist), retiring the bespoke promotion path. Part 3 adds `JANUS_HTTP_*` timeout/body-limit config.

**Tech Stack:** Go (stdlib `net/http` + `chi`), PostgreSQL via `pgx`, `golang-migrate`, testcontainers for store/e2e tests.

**Spec:** `docs/superpowers/specs/2026-07-15-api-hardening-design.md`

**Conventions (from the repo, must follow):**
- TDD: write the failing test, confirm it fails, implement minimal, confirm pass, commit.
- Stage explicit paths in `git add` — NEVER `git add -A`.
- Commit trailer: `Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>`.
- Store tests run against real Postgres via testcontainers (existing `internal/store` test harness); `go test ./...` spins them up.
- After any subagent edit, the LSP may show stale `undefined` for new symbols — trust `go build ./...` / `go test`, not LSP diagnostics.
- Never run `make migrate` and never touch the running dev container (ports 8210/5433).
- Value-free rule: no secret value in logs, errors, audit, or (now) idempotency storage.

---

## Part 1 — Cursor pagination (opt-in)

### Task 1: Store keyset helpers

**Files:**
- Create: `internal/store/pagination.go`
- Test: `internal/store/pagination_test.go`

- [ ] **Step 1: Write the failing test**

```go
// internal/store/pagination_test.go
package store

import (
	"testing"
	"time"
)

func TestKeyset(t *testing.T) {
	ts := time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)
	// nil cursor → empty predicate, no args
	if sql, args := keyset(nil, 1); sql != "" || args != nil {
		t.Fatalf("nil cursor: got %q %v", sql, args)
	}
	// cursor → predicate with placeholders starting at argN, id cast to uuid
	sql, args := keyset(&Cursor{CreatedAt: ts, ID: "abc"}, 3)
	if sql != "(created_at, id) < ($3, $4::uuid)" {
		t.Fatalf("sql = %q", sql)
	}
	if len(args) != 2 || args[0] != ts || args[1] != "abc" {
		t.Fatalf("args = %v", args)
	}
}

func TestLimitSQL(t *testing.T) {
	if sql, args := limitSQL(0, 5); sql != "" || args != nil {
		t.Fatalf("limit 0: got %q %v", sql, args)
	}
	if sql, args := limitSQL(-3, 5); sql != "" || args != nil {
		t.Fatalf("negative limit: got %q %v", sql, args)
	}
	sql, args := limitSQL(50, 2)
	if sql != " LIMIT $2" {
		t.Fatalf("sql = %q", sql)
	}
	if len(args) != 1 || args[0] != 50 {
		t.Fatalf("args = %v", args)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/store/ -run 'TestKeyset|TestLimitSQL' -v`
Expected: FAIL — `undefined: keyset`, `undefined: limitSQL`, `undefined: Cursor`.

- [ ] **Step 3: Write minimal implementation**

```go
// internal/store/pagination.go
package store

import (
	"fmt"
	"time"
)

// Cursor is a keyset position for (created_at DESC, id DESC) pagination.
// It is the opaque continuation token the API layer encodes for clients.
type Cursor struct {
	CreatedAt time.Time
	ID        string
}

// keyset returns an SQL predicate (and its args) selecting rows strictly after
// `after` in (created_at DESC, id DESC) order, using positional placeholders
// beginning at argN. Because both sort columns descend, a single lexicographic
// row-value comparison is a correct strict keyset. Returns ("", nil) when after
// is nil (first page). The id placeholder is cast to uuid to match the pk type.
func keyset(after *Cursor, argN int) (string, []any) {
	if after == nil {
		return "", nil
	}
	return fmt.Sprintf("(created_at, id) < ($%d, $%d::uuid)", argN, argN+1),
		[]any{after.CreatedAt, after.ID}
}

// limitSQL returns " LIMIT $argN" (and its arg) when limit > 0, else ("", nil)
// so a non-positive limit produces an unbounded query (the legacy List path).
func limitSQL(limit, argN int) (string, []any) {
	if limit <= 0 {
		return "", nil
	}
	return fmt.Sprintf(" LIMIT $%d", argN), []any{limit}
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/store/ -run 'TestKeyset|TestLimitSQL' -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/store/pagination.go internal/store/pagination_test.go
git commit -m "feat(store): keyset pagination helpers (Cursor, keyset, limitSQL)"
```

---

### Task 2: API cursor codec

**Files:**
- Create: `internal/api/pagination.go`
- Test: `internal/api/pagination_test.go`

- [ ] **Step 1: Write the failing test**

```go
// internal/api/pagination_test.go
package api

import (
	"net/http/httptest"
	"testing"
	"time"
)

func TestParsePageParams(t *testing.T) {
	// no params → unbounded, no cursor
	pp, err := parsePageParams(httptest.NewRequest("GET", "/v1/projects", nil))
	if err != nil || pp.limit != 0 || pp.after != nil {
		t.Fatalf("no params: %+v err=%v", pp, err)
	}
	// valid limit
	pp, err = parsePageParams(httptest.NewRequest("GET", "/v1/projects?limit=25", nil))
	if err != nil || pp.limit != 25 {
		t.Fatalf("limit=25: %+v err=%v", pp, err)
	}
	// out-of-range limit → error
	if _, err := parsePageParams(httptest.NewRequest("GET", "/v1/projects?limit=0", nil)); err == nil {
		t.Fatal("limit=0 should error")
	}
	if _, err := parsePageParams(httptest.NewRequest("GET", "/v1/projects?limit=201", nil)); err == nil {
		t.Fatal("limit=201 should error")
	}
	// round-trip cursor
	ts := time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)
	tok := encodeCursor(ts, "id-1")
	pp, err = parsePageParams(httptest.NewRequest("GET", "/v1/projects?limit=10&cursor="+tok, nil))
	if err != nil || pp.after == nil || !pp.after.CreatedAt.Equal(ts) || pp.after.ID != "id-1" {
		t.Fatalf("cursor round-trip: %+v err=%v", pp, err)
	}
	// malformed cursor → error
	if _, err := parsePageParams(httptest.NewRequest("GET", "/v1/projects?cursor=!!notbase64!!", nil)); err == nil {
		t.Fatal("bad cursor should error")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/api/ -run TestParsePageParams -v`
Expected: FAIL — `undefined: parsePageParams`, `undefined: encodeCursor`.

- [ ] **Step 3: Write minimal implementation**

```go
// internal/api/pagination.go
package api

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/steveokay/janus-secrets/internal/store"
)

var (
	errBadLimit  = errors.New("limit must be an integer 1-200")
	errBadCursor = errors.New("cursor is malformed")
)

// pageParams is the parsed pagination request. limit==0 means unbounded (no
// limit param supplied — the backward-compatible path); after==nil is the first
// page.
type pageParams struct {
	limit int
	after *store.Cursor
}

// cursorPayload is the JSON encoded inside the opaque base64url cursor token.
type cursorPayload struct {
	T time.Time `json:"t"`
	I string    `json:"i"`
}

// parsePageParams reads ?limit and ?cursor. Missing limit → 0 (unbounded).
func parsePageParams(r *http.Request) (pageParams, error) {
	var pp pageParams
	if v := r.URL.Query().Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > 200 {
			return pp, errBadLimit
		}
		pp.limit = n
	}
	if v := r.URL.Query().Get("cursor"); v != "" {
		c, err := decodeCursor(v)
		if err != nil {
			return pp, errBadCursor
		}
		pp.after = c
	}
	return pp, nil
}

// encodeCursor produces the opaque continuation token for a row's keyset
// position.
func encodeCursor(createdAt time.Time, id string) string {
	b, _ := json.Marshal(cursorPayload{T: createdAt, I: id})
	return base64.RawURLEncoding.EncodeToString(b)
}

func decodeCursor(s string) (*store.Cursor, error) {
	raw, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return nil, err
	}
	var p cursorPayload
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, err
	}
	if p.I == "" {
		return nil, errBadCursor
	}
	return &store.Cursor{CreatedAt: p.T, ID: p.I}, nil
}

// nextCursor returns the encoded continuation token when the page was full
// (len == limit and limit > 0), else nil — computed from the last RAW scanned
// row's keyset position (createdAt,id), independent of any post-filtering.
func nextCursor(limit, rawLen int, lastCreatedAt time.Time, lastID string) *string {
	if limit <= 0 || rawLen < limit {
		return nil
	}
	tok := encodeCursor(lastCreatedAt, lastID)
	return &tok
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/api/ -run TestParsePageParams -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/api/pagination.go internal/api/pagination_test.go
git commit -m "feat(api): opaque cursor codec + page-param parsing"
```

---

### Task 3: Projects list pagination

**Files:**
- Modify: `internal/store/projects.go:52-69` (add `ListPage`, make `List` delegate)
- Modify: `internal/api/projects_handlers.go:51-64` (`handleProjectList`)
- Test: `internal/store/projects_test.go` (add), `internal/api/projects_handlers_test.go` (add if present; else store test suffices)

- [ ] **Step 1: Write the failing store test**

Add to `internal/store/projects_test.go` (uses the existing testcontainer store fixture — mirror the setup already used by other tests in that file, e.g. `st := newTestStore(t)`):

```go
func TestProjectRepo_ListPage(t *testing.T) {
	st := newTestStore(t) // existing helper in this package's tests
	repo := NewProjectRepo(st)
	ctx := context.Background()
	// Seed 5 projects; created_at defaults to now() so insertion order == time order.
	for i := 0; i < 5; i++ {
		if _, err := repo.Create(ctx, fmt.Sprintf("proj-%d", i), fmt.Sprintf("Proj %d", i), []byte("wrapped-kek-000"), 1); err != nil {
			t.Fatal(err)
		}
	}
	// Unbounded returns all 5, newest first.
	all, err := repo.ListPage(ctx, 0, nil)
	if err != nil || len(all) != 5 {
		t.Fatalf("unbounded: len=%d err=%v", len(all), err)
	}
	// Page of 2, then continue via cursor until exhausted; assert no dupes, full coverage.
	seen := map[string]bool{}
	var after *Cursor
	pages := 0
	for {
		page, err := repo.ListPage(ctx, 2, after)
		if err != nil {
			t.Fatal(err)
		}
		pages++
		for _, p := range page {
			if seen[p.ID] {
				t.Fatalf("duplicate id %s across pages", p.ID)
			}
			seen[p.ID] = true
		}
		if len(page) < 2 {
			break
		}
		last := page[len(page)-1]
		after = &Cursor{CreatedAt: last.CreatedAt, ID: last.ID}
	}
	if len(seen) != 5 {
		t.Fatalf("covered %d of 5", len(seen))
	}
}
```

> **Note on `Create` signature:** confirm the actual `ProjectRepo.Create` signature in `internal/store/projects.go` and adjust the seed call to match (name/slug/wrapped_kek/kek_version). If the test helper for seeding differs in this package, use the package's existing seeding idiom.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/store/ -run TestProjectRepo_ListPage -v`
Expected: FAIL — `undefined: (*ProjectRepo).ListPage`.

- [ ] **Step 3: Implement `ListPage`, make `List` delegate**

Replace the existing `List` (`internal/store/projects.go:52-69`) with:

```go
// ListPage returns non-deleted projects in (created_at DESC, id DESC) order.
// limit<=0 is unbounded; after==nil is the first page.
func (r *ProjectRepo) ListPage(ctx context.Context, limit int, after *Cursor) ([]*Project, error) {
	q := `SELECT ` + projectCols + ` FROM projects WHERE deleted_at IS NULL`
	var args []any
	if ks, ksArgs := keyset(after, len(args)+1); ks != "" {
		q += " AND " + ks
		args = append(args, ksArgs...)
	}
	q += " ORDER BY created_at DESC, id DESC"
	if ls, lArgs := limitSQL(limit, len(args)+1); ls != "" {
		q += ls
		args = append(args, lArgs...)
	}
	rows, err := r.s.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, mapError(err)
	}
	defer rows.Close()
	var out []*Project
	for rows.Next() {
		p, err := scanProject(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, mapError(rows.Err())
}

// List returns all non-deleted projects, newest first (unbounded; kept for
// existing internal callers).
func (r *ProjectRepo) List(ctx context.Context) ([]*Project, error) {
	return r.ListPage(ctx, 0, nil)
}
```

- [ ] **Step 4: Run store test to verify it passes**

Run: `go test ./internal/store/ -run TestProjectRepo_ListPage -v`
Expected: PASS.

- [ ] **Step 5: Paginate the handler**

Replace `handleProjectList` body (`internal/api/projects_handlers.go:51-64`) so it parses page params, calls `ListPage`, and emits `next_cursor`. Keep the existing authz check and the existing `out` view-mapping loop exactly as they are; only the fetch + response change:

```go
func (s *Server) handleProjectList(w http.ResponseWriter, r *http.Request) {
	// ... keep existing authz gate unchanged ...
	pp, err := parsePageParams(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, CodeValidation, err.Error())
		return
	}
	ps, err := store.NewProjectRepo(s.st).ListPage(r.Context(), pp.limit, pp.after)
	if err != nil {
		writeError(w, http.StatusInternalServerError, CodeInternal, "internal error")
		return
	}
	out := make([]projectView, 0, len(ps)) // keep the existing view type + mapping loop
	for _, p := range ps {
		out = append(out, /* existing mapping */)
	}
	var next *string
	if len(ps) > 0 {
		last := ps[len(ps)-1]
		next = nextCursor(pp.limit, len(ps), last.CreatedAt, last.ID)
	}
	writeJSON(w, http.StatusOK, map[string]any{"projects": out, "next_cursor": next})
}
```

> Preserve the current view struct/mapping from the existing handler verbatim; only wrap the fetch and add `next_cursor`. `next` is `nil` (JSON `null`) when unbounded or the last page — non-breaking for existing web callers.

- [ ] **Step 6: Verify build + existing handler tests pass**

Run: `go build ./... && go test ./internal/api/ -run 'Project' -v`
Expected: PASS (existing project handler tests still green; `next_cursor: null` added).

- [ ] **Step 7: Commit**

```bash
git add internal/store/projects.go internal/store/projects_test.go internal/api/projects_handlers.go
git commit -m "feat(api): cursor pagination for GET /v1/projects"
```

---

### Task 4: Environments + Configs list pagination

**Files:**
- Modify: `internal/store/environments.go:56` (`ListByProject` → add `ListByProjectPage`, delegate)
- Modify: `internal/store/configs.go:57` (`ListByEnvironment` → add `ListByEnvironmentPage`, delegate)
- Modify: `internal/api/environments_handlers.go:54` (`handleEnvList`)
- Modify: `internal/api/configs_handlers.go:99` (`handleConfigList`)
- Test: `internal/store/environments_test.go`, `internal/store/configs_test.go`

- [ ] **Step 1: Write failing store tests**

Add `TestEnvironmentRepo_ListByProjectPage` and `TestConfigRepo_ListByEnvironmentPage` mirroring Task 3's page-walk assertion (seed 5 under one parent, page by 2, assert 3 pages / no dupes / full coverage). Use each package test file's existing seeding helpers for a project→environment→config chain.

- [ ] **Step 2: Run to verify they fail**

Run: `go test ./internal/store/ -run 'ListByProjectPage|ListByEnvironmentPage' -v`
Expected: FAIL — undefined methods.

- [ ] **Step 3: Implement the paged store methods + delegates**

`internal/store/environments.go` — replace `ListByProject`:

```go
// ListByProjectPage returns non-deleted environments for a project in
// (created_at DESC, id DESC) order. limit<=0 unbounded; after==nil first page.
func (r *EnvironmentRepo) ListByProjectPage(ctx context.Context, projectID string, limit int, after *Cursor) ([]*Environment, error) {
	q := `SELECT ` + envCols + ` FROM environments WHERE project_id = $1::uuid AND deleted_at IS NULL`
	args := []any{projectID}
	if ks, ksArgs := keyset(after, len(args)+1); ks != "" {
		q += " AND " + ks
		args = append(args, ksArgs...)
	}
	q += " ORDER BY created_at DESC, id DESC"
	if ls, lArgs := limitSQL(limit, len(args)+1); ls != "" {
		q += ls
		args = append(args, lArgs...)
	}
	rows, err := r.s.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, mapError(err)
	}
	defer rows.Close()
	var out []*Environment
	for rows.Next() {
		e, err := scanEnvironment(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, mapError(rows.Err())
}

func (r *EnvironmentRepo) ListByProject(ctx context.Context, projectID string) ([]*Environment, error) {
	return r.ListByProjectPage(ctx, projectID, 0, nil)
}
```

> Use the actual row-scan function this repo uses (match the existing `ListByProject` — e.g. `scanEnvironment` or an inline scan). Keep it identical to the original loop.

`internal/store/configs.go` — replace `ListByEnvironment` analogously (table `configs`, filter `environment_id = $1::uuid AND deleted_at IS NULL`, cols `configCols`, scan `scanConfig` or the existing inline scan), adding `ListByEnvironmentPage(ctx, environmentID string, limit int, after *Cursor)` and delegating.

- [ ] **Step 4: Run to verify store tests pass**

Run: `go test ./internal/store/ -run 'ListByProjectPage|ListByEnvironmentPage' -v`
Expected: PASS.

- [ ] **Step 5: Paginate both handlers**

`handleEnvList` (`environments_handlers.go:54`) and `handleConfigList` (`configs_handlers.go:99`): keep their existing authz + path-param extraction (`pid` / `eid`) and view mapping; swap the fetch to the paged method and add `next_cursor`, following Task 3 Step 5's shape. Response keys stay `"environments"` / `"configs"`.

- [ ] **Step 6: Build + handler tests**

Run: `go build ./... && go test ./internal/api/ -run 'Env|Config' -v`
Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add internal/store/environments.go internal/store/configs.go internal/store/environments_test.go internal/store/configs_test.go internal/api/environments_handlers.go internal/api/configs_handlers.go
git commit -m "feat(api): cursor pagination for environments + configs lists"
```

---

### Task 5: Tokens + Users list pagination (service-wrapped)

**Files:**
- Modify: `internal/store/service_tokens.go:85` (`List` → add `ListPage`, delegate)
- Modify: `internal/store/users.go:70` (`List` → add `ListPage`, delegate; note current order is `created_at ASC` → change to `created_at DESC, id DESC`)
- Modify: `internal/auth/tokens.go:171` (`ListTokens` → add `ListTokensPage`, delegate)
- Modify: `internal/auth/service.go:200` (`ListUsers` → add `ListUsersPage`, delegate)
- Modify: `internal/api/tokens_handlers.go:72` (`handleTokenList`)
- Modify: `internal/api/users_handlers.go:37` (`handleUserList`)
- Test: `internal/store/service_tokens_test.go`, `internal/store/users_test.go`

**Design note (tokens):** `handleTokenList` post-filters each token by per-scope authz (`s.can(TokenRead, res)`), so the visible set is a subset of the scanned page. Pagination therefore keys on the **raw scan position**: the service page method returns the raw page (mapped, unfiltered) plus a `*store.Cursor` derived from the last raw row when the raw fetch was full; the handler applies its existing visibility filter to that page and passes the service-provided cursor straight through. A page may contain fewer visible tokens than `limit`; the client keeps paging until `next_cursor` is null.

- [ ] **Step 1: Write failing store tests**

Add `TestServiceTokenRepo_ListPage` and `TestUserRepo_ListPage` mirroring the Task 3 page-walk (seed 5, page by 2, assert coverage + no dupes + DESC order). For `service_tokens` there is no `deleted_at` filter; for `users` likewise none.

- [ ] **Step 2: Run to verify they fail**

Run: `go test ./internal/store/ -run 'ServiceTokenRepo_ListPage|UserRepo_ListPage' -v`
Expected: FAIL — undefined methods.

- [ ] **Step 3: Implement store `ListPage` + delegates**

`internal/store/service_tokens.go` — replace `List`:

```go
// ListPage returns service tokens in (created_at DESC, id DESC) order.
// limit<=0 unbounded; after==nil first page.
func (r *ServiceTokenRepo) ListPage(ctx context.Context, limit int, after *Cursor) ([]*ServiceToken, error) {
	q := `SELECT ` + svcTokenCols + ` FROM service_tokens`
	var args []any
	if ks, ksArgs := keyset(after, len(args)+1); ks != "" {
		q += " WHERE " + ks
		args = append(args, ksArgs...)
	}
	q += " ORDER BY created_at DESC, id DESC"
	if ls, lArgs := limitSQL(limit, len(args)+1); ls != "" {
		q += ls
		args = append(args, lArgs...)
	}
	rows, err := r.s.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, mapError(err)
	}
	defer rows.Close()
	var out []*ServiceToken
	for rows.Next() {
		t, err := scanServiceToken(rows) // match the existing List's scan
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, mapError(rows.Err())
}

func (r *ServiceTokenRepo) List(ctx context.Context) ([]*ServiceToken, error) {
	return r.ListPage(ctx, 0, nil)
}
```

> Note the `WHERE`/`AND` seam: this table has no base filter, so the keyset uses `WHERE`; tables with an existing filter (projects/env/config) use `AND`. Use the actual scan function from the current `List`.

`internal/store/users.go` — replace `List` analogously (table `users`, cols `userCols`, scan `scanUser` / existing inline). **Change the order from `created_at ASC` to `created_at DESC, id DESC`** for keyset correctness. Add `ListPage` + delegate.

- [ ] **Step 4: Run store tests**

Run: `go test ./internal/store/ -run 'ServiceTokenRepo_ListPage|UserRepo_ListPage' -v`
Expected: PASS.

- [ ] **Step 5: Add service page methods (return cursor)**

`internal/auth/tokens.go` — add, and make `ListTokens` delegate. **Preserve the existing `ListTokens` row→`TokenMeta` mapping**; extract it into the loop below verbatim:

```go
// ListTokensPage returns a raw page of token metadata plus the keyset cursor
// for the next page (nil on the last page). Callers apply their own visibility
// filter; the cursor tracks the raw scan position, not the filtered count.
func (s *Service) ListTokensPage(ctx context.Context, limit int, after *store.Cursor) ([]TokenMeta, *store.Cursor, error) {
	rows, err := s.tokens.ListPage(ctx, limit, after)
	if err != nil {
		return nil, nil, err
	}
	out := make([]TokenMeta, 0, len(rows))
	for _, t := range rows {
		out = append(out, /* the exact TokenMeta mapping the current ListTokens uses */)
	}
	var next *store.Cursor
	if limit > 0 && len(rows) == limit {
		last := rows[len(rows)-1]
		next = &store.Cursor{CreatedAt: last.CreatedAt, ID: last.ID}
	}
	return out, next, nil
}

func (s *Service) ListTokens(ctx context.Context) ([]TokenMeta, error) {
	metas, _, err := s.ListTokensPage(ctx, 0, nil)
	return metas, err
}
```

> Open the current `ListTokens` (auth/tokens.go:171) and move its per-row mapping into the loop above unchanged. `import "github.com/steveokay/janus-secrets/internal/store"` if not already imported.

`internal/auth/service.go` — add `ListUsersPage(ctx, limit int, after *store.Cursor) ([]UserInfo, *store.Cursor, error)` the same way, delegating `ListUsers`. Preserve the current `UserInfo` mapping.

- [ ] **Step 6: Paginate both handlers**

`handleTokenList` (`tokens_handlers.go:72`): parse page params, call `s.auth.ListTokensPage(ctx, pp.limit, pp.after)`, keep the existing per-token `resolveScopeResource` + `s.can(TokenRead, res)` filter loop to build `out`, then emit `map[string]any{"tokens": out, "next_cursor": encodeIfNotNil(next)}` where `next` is the service-returned `*store.Cursor`. Add a tiny local helper or inline:

```go
	var nextTok *string
	if next != nil {
		t := encodeCursor(next.CreatedAt, next.ID)
		nextTok = &t
	}
	writeJSON(w, http.StatusOK, map[string]any{"tokens": out, "next_cursor": nextTok})
```

`handleUserList` (`users_handlers.go:37`): keep the `UserManage` gate; call `ListUsersPage`; emit `{"users": users, "next_cursor": nextTok}` with the same encode-if-not-nil pattern.

- [ ] **Step 7: Build + handler tests**

Run: `go build ./... && go test ./internal/api/ -run 'Token|User' -v && go test ./internal/auth/... -v`
Expected: PASS.

- [ ] **Step 8: Commit**

```bash
git add internal/store/service_tokens.go internal/store/users.go internal/store/service_tokens_test.go internal/store/users_test.go internal/auth/tokens.go internal/auth/service.go internal/api/tokens_handlers.go internal/api/users_handlers.go
git commit -m "feat(api): cursor pagination for tokens + users lists"
```

---

### Task 6: Members + Transit keys list pagination (service-wrapped)

**Files:**
- Modify: `internal/store/role_bindings.go:87` (`ListForScope` → add `ListForScopePage`, delegate)
- Modify: `internal/store/transit.go:146` (`List` → add `ListPage`, delegate; current order `name ASC` → `created_at DESC, id DESC`)
- Modify: `internal/authz/management.go:27` (`ListMembers` → add `ListMembersPage`, delegate)
- Modify: `internal/transit/lifecycle.go:78` (`List` → add `ListPage`, delegate)
- Modify: `internal/api/members_handlers.go:71` (`membersList`)
- Modify: `internal/api/transit_handlers.go:50` (`handleTransitList`)
- Test: `internal/store/role_bindings_test.go`, `internal/store/transit_test.go`

- [ ] **Step 1: Write failing store tests**

Add `TestRoleBindingRepo_ListForScopePage` (seed 5 instance-scope bindings, page by 2, assert coverage) and `TestTransitRepo_ListPage` (seed 5 keys, page by 2). Use existing seeding helpers.

- [ ] **Step 2: Run to verify they fail**

Run: `go test ./internal/store/ -run 'ListForScopePage|TransitRepo_ListPage' -v`
Expected: FAIL — undefined methods.

- [ ] **Step 3: Implement paged store methods + delegates**

`internal/store/role_bindings.go` — `ListForScope` currently builds one of three queries by `level` (instance / project / environment) with no ORDER BY. Add `ListForScopePage(ctx, level, scopeID string, limit int, after *Cursor)` that builds the same base WHERE per level, then appends the keyset (`AND (created_at,id) < (...)`), `ORDER BY created_at DESC, id DESC`, and the limit. Preserve the existing scan. Delegate:

```go
func (r *RoleBindingRepo) ListForScope(ctx context.Context, level, scopeID string) ([]*RoleBinding, error) {
	return r.ListForScopePage(ctx, level, scopeID, 0, nil)
}
```

Concrete builder (instance has no scope arg; project/environment bind `$1`):

```go
func (r *RoleBindingRepo) ListForScopePage(ctx context.Context, level, scopeID string, limit int, after *Cursor) ([]*RoleBinding, error) {
	var q string
	var args []any
	switch level {
	case "instance":
		q = `SELECT ` + roleBindingCols + ` FROM role_bindings WHERE scope_level = 'instance'`
	case "project":
		q = `SELECT ` + roleBindingCols + ` FROM role_bindings WHERE scope_level = 'project' AND project_id = $1::uuid`
		args = append(args, scopeID)
	case "environment":
		q = `SELECT ` + roleBindingCols + ` FROM role_bindings WHERE scope_level = 'environment' AND environment_id = $1::uuid`
		args = append(args, scopeID)
	default:
		return nil, ErrNotFound // match the current default behavior in ListForScope
	}
	if ks, ksArgs := keyset(after, len(args)+1); ks != "" {
		q += " AND " + ks
		args = append(args, ksArgs...)
	}
	q += " ORDER BY created_at DESC, id DESC"
	if ls, lArgs := limitSQL(limit, len(args)+1); ls != "" {
		q += ls
		args = append(args, lArgs...)
	}
	rows, err := r.s.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, mapError(err)
	}
	defer rows.Close()
	var out []*RoleBinding
	for rows.Next() {
		b, err := scanRoleBinding(rows) // match current scan
		if err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, mapError(rows.Err())
}
```

> Match the current `ListForScope`'s exact `default:` behavior and scan function; only add keyset/order/limit.

`internal/store/transit.go` — replace `List` with `ListPage(ctx, limit int, after *Cursor)` (table `transit_keys`, cols `transitKeyCols`, **order changed from `name ASC` to `created_at DESC, id DESC`**), delegate `List`.

- [ ] **Step 4: Run store tests**

Run: `go test ./internal/store/ -run 'ListForScopePage|TransitRepo_ListPage' -v`
Expected: PASS.

- [ ] **Step 5: Add service page methods (return cursor)**

`internal/authz/management.go` — add `ListMembersPage(ctx, level, scopeID string, limit int, after *store.Cursor) ([]Member, *store.Cursor, error)` delegating to `ListForScopePage`, preserving the current `Member` mapping; compute `next` from the last store row when `limit>0 && len==limit`. Make `ListMembers` delegate `(members, _, err)`.

`internal/transit/lifecycle.go` — add `ListPage(ctx, limit int, after *store.Cursor) ([]KeyMeta, *store.Cursor, error)` delegating to the store `ListPage`, preserving the `KeyMeta` mapping; make `List` delegate.

- [ ] **Step 6: Paginate both handlers**

`membersList` (`members_handlers.go:71`): keep the `MemberRead` gate + scopeID derivation; call `s.authz.ListMembersPage(ctx, spec.level, scopeID, pp.limit, pp.after)`; emit `{"members": members, "next_cursor": nextTok}` (encode-if-not-nil).

`handleTransitList` (`transit_handlers.go:50`): keep the `TransitRead` gate; call `s.transit.ListPage(...)`; keep the `transitMeta(m)` mapping loop; emit `{"keys": out, "next_cursor": nextTok}`.

- [ ] **Step 7: Build + handler tests**

Run: `go build ./... && go test ./internal/api/ -run 'Member|Transit' -v && go test ./internal/authz/... ./internal/transit/... -v`
Expected: PASS.

- [ ] **Step 8: Commit**

```bash
git add internal/store/role_bindings.go internal/store/transit.go internal/store/role_bindings_test.go internal/store/transit_test.go internal/authz/management.go internal/transit/lifecycle.go internal/api/members_handlers.go internal/api/transit_handlers.go
git commit -m "feat(api): cursor pagination for members + transit keys lists"
```

---

## Part 2 — Generic idempotency middleware

### Task 7: Migration 000020 (idempotency table + pagination indexes; drop promotion_idempotency)

**Files:**
- Create: `migrations/000020_api_hardening.up.sql`
- Create: `migrations/000020_api_hardening.down.sql`

- [ ] **Step 1: Write the up migration**

```sql
-- migrations/000020_api_hardening.up.sql
-- Generic idempotency: one row per (Idempotency-Key, actor). status_code 0 =
-- claimed-but-pending. Bodies are NEVER stored — only the status code — so no
-- once-shown secret can persist here.
CREATE TABLE idempotency (
    idempotency_key text        NOT NULL,
    actor           text        NOT NULL,
    endpoint        text        NOT NULL,
    request_hash    text        NOT NULL,
    status_code     integer     NOT NULL DEFAULT 0,
    created_at      timestamptz NOT NULL DEFAULT now(),
    completed_at    timestamptz,
    PRIMARY KEY (idempotency_key, actor)
);

-- The bespoke promotion idempotency table is superseded by the generic one.
DROP TABLE IF EXISTS promotion_idempotency;

-- Keyset pagination covering indexes (created_at DESC, id DESC); partial on the
-- soft-deleting tables to match the WHERE deleted_at IS NULL scan.
CREATE INDEX idx_projects_page      ON projects       (created_at DESC, id DESC) WHERE deleted_at IS NULL;
CREATE INDEX idx_environments_page  ON environments   (created_at DESC, id DESC) WHERE deleted_at IS NULL;
CREATE INDEX idx_configs_page       ON configs        (created_at DESC, id DESC) WHERE deleted_at IS NULL;
CREATE INDEX idx_service_tokens_page ON service_tokens (created_at DESC, id DESC);
CREATE INDEX idx_users_page         ON users          (created_at DESC, id DESC);
CREATE INDEX idx_role_bindings_page ON role_bindings  (created_at DESC, id DESC);
CREATE INDEX idx_transit_keys_page  ON transit_keys   (created_at DESC, id DESC);
```

- [ ] **Step 2: Write the down migration**

```sql
-- migrations/000020_api_hardening.down.sql
DROP INDEX IF EXISTS idx_transit_keys_page;
DROP INDEX IF EXISTS idx_role_bindings_page;
DROP INDEX IF EXISTS idx_users_page;
DROP INDEX IF EXISTS idx_service_tokens_page;
DROP INDEX IF EXISTS idx_configs_page;
DROP INDEX IF EXISTS idx_environments_page;
DROP INDEX IF EXISTS idx_projects_page;

DROP TABLE IF EXISTS idempotency;

-- Recreate the promotion idempotency table (original 000017 shape).
CREATE TABLE promotion_idempotency (
    idempotency_key text        NOT NULL,
    actor           text        NOT NULL,
    request_hash    text        NOT NULL,
    response        jsonb,
    created_at      timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (idempotency_key, actor)
);
```

> Confirm the original `promotion_idempotency` DDL in `migrations/000017_*.up.sql` and reproduce it exactly in the down file (column types, defaults, PK).

- [ ] **Step 3: Verify migrations apply cleanly in a test container**

Run the store test suite (it applies all migrations on container boot):
Run: `go test ./internal/store/ -run TestKeyset -v`
Expected: PASS (migrations 000001–000020 apply without error; a failure here surfaces bad SQL).

- [ ] **Step 4: Commit**

```bash
git add migrations/000020_api_hardening.up.sql migrations/000020_api_hardening.down.sql
git commit -m "feat(store): migration 000020 — idempotency table + pagination indexes"
```

---

### Task 8: Generic `IdempotencyRepo`

**Files:**
- Create: `internal/store/idempotency.go`
- Delete: the `PromotionIdempotencyRepo` + `IdempotencyRecord` block in `internal/store/promotion.go:147-199` (moved/generalized here)
- Test: `internal/store/idempotency_test.go`

- [ ] **Step 1: Write the failing test**

```go
// internal/store/idempotency_test.go
package store

import (
	"context"
	"testing"
)

func TestIdempotencyRepo_ClaimCompleteReplayRelease(t *testing.T) {
	st := newTestStore(t)
	repo := NewIdempotencyRepo(st)
	ctx := context.Background()
	const key, actor, ep, hash = "k1", "actor-1", "POST /v1/projects", "hash-abc"

	// First claim wins.
	claimed, existing, err := repo.Claim(ctx, key, actor, ep, hash)
	if err != nil || !claimed || existing != nil {
		t.Fatalf("first claim: claimed=%v existing=%v err=%v", claimed, existing, err)
	}
	// Second claim (same key+actor) loses; existing is pending (status 0).
	claimed, existing, err = repo.Claim(ctx, key, actor, ep, hash)
	if err != nil || claimed || existing == nil || existing.StatusCode != 0 {
		t.Fatalf("second claim: claimed=%v existing=%+v err=%v", claimed, existing, err)
	}
	// Complete stores the status.
	if err := repo.Complete(ctx, key, actor, 200); err != nil {
		t.Fatal(err)
	}
	claimed, existing, err = repo.Claim(ctx, key, actor, ep, hash)
	if err != nil || claimed || existing.StatusCode != 200 {
		t.Fatalf("after complete: %+v err=%v", existing, err)
	}
	// Release deletes so the key is reusable.
	if err := repo.Release(ctx, key, actor); err != nil {
		t.Fatal(err)
	}
	claimed, _, err = repo.Claim(ctx, key, actor, ep, hash)
	if err != nil || !claimed {
		t.Fatalf("after release, re-claim should win: claimed=%v err=%v", claimed, err)
	}
	// A different actor with the same key is independent.
	claimed, _, err = repo.Claim(ctx, key, "actor-2", ep, hash)
	if err != nil || !claimed {
		t.Fatalf("different actor: claimed=%v err=%v", claimed, err)
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/store/ -run TestIdempotencyRepo -v`
Expected: FAIL — `undefined: NewIdempotencyRepo`.

- [ ] **Step 3: Implement the repo**

```go
// internal/store/idempotency.go
package store

import "context"

// IdemRecord is a stored idempotency entry. StatusCode 0 means claimed but not
// yet completed (in flight). No response body is ever stored.
type IdemRecord struct {
	Endpoint    string
	RequestHash string
	StatusCode  int
}

// IdempotencyRepo stores at-most-once execution markers keyed by
// (idempotency_key, actor). It stores only the final HTTP status — never a
// response body — so once-shown secrets in a response can never persist here.
type IdempotencyRepo struct{ s *Store }

func NewIdempotencyRepo(s *Store) *IdempotencyRepo { return &IdempotencyRepo{s: s} }

// Claim atomically inserts a pending row. claimed=true means THIS caller won and
// must run the handler then Complete/Release. When claimed=false, existing is
// the current record (StatusCode 0 = still pending).
func (r *IdempotencyRepo) Claim(ctx context.Context, key, actor, endpoint, requestHash string) (claimed bool, existing *IdemRecord, err error) {
	ct, err := r.s.pool.Exec(ctx,
		`INSERT INTO idempotency (idempotency_key, actor, endpoint, request_hash)
		 VALUES ($1, $2, $3, $4) ON CONFLICT (idempotency_key, actor) DO NOTHING`,
		key, actor, endpoint, requestHash)
	if err != nil {
		return false, nil, mapError(err)
	}
	if ct.RowsAffected() == 1 {
		return true, nil, nil
	}
	var rec IdemRecord
	if err := r.s.pool.QueryRow(ctx,
		`SELECT endpoint, request_hash, status_code FROM idempotency
		 WHERE idempotency_key=$1 AND actor=$2`, key, actor).
		Scan(&rec.Endpoint, &rec.RequestHash, &rec.StatusCode); err != nil {
		return false, nil, mapError(err)
	}
	return false, &rec, nil
}

// Complete records the final status for a previously claimed row.
func (r *IdempotencyRepo) Complete(ctx context.Context, key, actor string, status int) error {
	return r.s.execAffectingOne(ctx,
		`UPDATE idempotency SET status_code=$3, completed_at=now()
		 WHERE idempotency_key=$1 AND actor=$2`, key, actor, status)
}

// Release deletes a claimed row so a failed request can be retried with the key.
func (r *IdempotencyRepo) Release(ctx context.Context, key, actor string) error {
	_, err := r.s.pool.Exec(ctx,
		`DELETE FROM idempotency WHERE idempotency_key=$1 AND actor=$2`, key, actor)
	return mapError(err)
}
```

- [ ] **Step 4: Remove the old promotion repo**

Delete `internal/store/promotion.go:147-199` (the `IdempotencyRecord` type, `PromotionIdempotencyRepo`, `NewPromotionIdempotencyRepo`, `Claim`, `Complete`, `Release`). Leave the rest of `promotion.go` intact.

- [ ] **Step 5: Run test to verify it passes**

Run: `go test ./internal/store/ -run TestIdempotencyRepo -v`
Expected: PASS. (Build will break in `internal/api/promotion_handlers.go` — fixed in Task 10; run the store package test in isolation here.)

- [ ] **Step 6: Commit**

```bash
git add internal/store/idempotency.go internal/store/idempotency_test.go internal/store/promotion.go
git commit -m "feat(store): generic IdempotencyRepo (status-only, no body)"
```

---

### Task 9: Idempotency middleware + shared principal resolver

**Files:**
- Modify: `internal/api/middleware_auth.go` (extract `resolvePrincipal`; reuse in `RequireAuth`)
- Create: `internal/api/idempotency.go` (the middleware)
- Modify: `internal/api/server.go:106-108` (wire the middleware globally)
- Test: `internal/api/idempotency_test.go`

- [ ] **Step 1: Extract the shared resolver from RequireAuth**

In `internal/api/middleware_auth.go`, factor the credential resolution (lines 46-56) into a helper and call it from `RequireAuth` (no behavior change):

```go
// resolvePrincipal authenticates via Bearer service token or session cookie,
// returning the Principal (and token scope, if any). It does NOT write a
// response; callers decide how to handle errors. Shared by RequireAuth and the
// idempotency middleware.
func resolvePrincipal(v authVerifier, r *http.Request) (auth.Principal, *auth.TokenScope, error) {
	if h := r.Header.Get("Authorization"); strings.HasPrefix(h, "Bearer ") {
		return v.VerifyServiceToken(r.Context(), strings.TrimPrefix(h, "Bearer "))
	}
	if c, cErr := r.Cookie(sessionCookieName); cErr == nil {
		p, err := v.VerifySession(r.Context(), c.Value)
		return p, nil, err
	}
	return auth.Principal{}, nil, auth.ErrUnauthenticated
}
```

Then in `RequireAuth`, replace the inline `if/else if/else` block (lines 50-56) with:

```go
			p, scope, err := resolvePrincipal(v, r)
```

Leave the `switch` on `err` unchanged.

- [ ] **Step 2: Write the failing middleware test**

```go
// internal/api/idempotency_test.go
package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/steveokay/janus-secrets/internal/auth"
)

// stubIdemStore records Claim/Complete/Release calls for the middleware test.
type stubIdemStore struct {
	claimed  bool
	existing *idemExisting
	claims   int
	completed int
	released  int
}
func (s *stubIdemStore) Claim(ctx context.Context, key, actor, endpoint, hash string) (bool, *idemExisting, error) {
	s.claims++
	return s.claimed, s.existing, nil
}
func (s *stubIdemStore) Complete(ctx context.Context, key, actor string, status int) error { s.completed++; return nil }
func (s *stubIdemStore) Release(ctx context.Context, key, actor string) error              { s.released++; return nil }

// stubVerifier returns a fixed principal for any Bearer token.
type stubVerifier struct{}
func (stubVerifier) VerifySession(ctx context.Context, c string) (auth.Principal, error) { return auth.Principal{}, auth.ErrUnauthenticated }
func (stubVerifier) VerifyServiceToken(ctx context.Context, raw string) (auth.Principal, *auth.TokenScope, error) {
	return auth.Principal{Kind: auth.KindServiceToken, ID: "tok-1"}, nil, nil
}

func TestIdempotency_PassthroughWithoutKey(t *testing.T) {
	store := &stubIdemStore{claimed: true}
	mw := idempotencyMiddleware(store, stubVerifier{})
	var ran bool
	h := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { ran = true; w.WriteHeader(201) }))
	req := httptest.NewRequest("POST", "/v1/projects", strings.NewReader("{}"))
	req.Header.Set("Authorization", "Bearer x")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if !ran || store.claims != 0 {
		t.Fatalf("no key → passthrough, no claim: ran=%v claims=%d", ran, store.claims)
	}
}

func TestIdempotency_ClaimThenComplete(t *testing.T) {
	store := &stubIdemStore{claimed: true}
	mw := idempotencyMiddleware(store, stubVerifier{})
	h := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(201) }))
	req := httptest.NewRequest("POST", "/v1/projects", strings.NewReader("{}"))
	req.Header.Set("Authorization", "Bearer x")
	req.Header.Set("Idempotency-Key", "k1")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if store.claims != 1 || store.completed != 1 || rec.Code != 201 {
		t.Fatalf("claim+complete on 2xx: claims=%d completed=%d code=%d", store.claims, store.completed, rec.Code)
	}
}

func TestIdempotency_ReleaseOnError(t *testing.T) {
	store := &stubIdemStore{claimed: true}
	mw := idempotencyMiddleware(store, stubVerifier{})
	h := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(500) }))
	req := httptest.NewRequest("POST", "/v1/projects", strings.NewReader("{}"))
	req.Header.Set("Authorization", "Bearer x")
	req.Header.Set("Idempotency-Key", "k1")
	h.ServeHTTP(httptest.NewRecorder(), req)
	if store.released != 1 || store.completed != 0 {
		t.Fatalf("non-2xx → release: released=%d completed=%d", store.released, store.completed)
	}
}

func TestIdempotency_ReplayCompleted(t *testing.T) {
	store := &stubIdemStore{claimed: false, existing: &idemExisting{Endpoint: "POST /v1/projects", RequestHash: hashBody([]byte("{}")), StatusCode: 201}}
	mw := idempotencyMiddleware(store, stubVerifier{})
	var ran bool
	h := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { ran = true }))
	req := httptest.NewRequest("POST", "/v1/projects", strings.NewReader("{}"))
	req.Header.Set("Authorization", "Bearer x")
	req.Header.Set("Idempotency-Key", "k1")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if ran || rec.Code != 201 || rec.Header().Get("Idempotency-Replayed") != "true" || !strings.Contains(rec.Body.String(), "idempotent_replay") {
		t.Fatalf("replay: ran=%v code=%d hdr=%q body=%s", ran, rec.Code, rec.Header().Get("Idempotency-Replayed"), rec.Body.String())
	}
}

func TestIdempotency_ConflictDifferentHash(t *testing.T) {
	store := &stubIdemStore{claimed: false, existing: &idemExisting{Endpoint: "POST /v1/projects", RequestHash: "different", StatusCode: 201}}
	mw := idempotencyMiddleware(store, stubVerifier{})
	h := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	req := httptest.NewRequest("POST", "/v1/projects", strings.NewReader("{}"))
	req.Header.Set("Authorization", "Bearer x")
	req.Header.Set("Idempotency-Key", "k1")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusConflict {
		t.Fatalf("hash mismatch → 409, got %d", rec.Code)
	}
}

func TestIdempotency_InProgress(t *testing.T) {
	store := &stubIdemStore{claimed: false, existing: &idemExisting{Endpoint: "POST /v1/projects", RequestHash: hashBody([]byte("{}")), StatusCode: 0}}
	mw := idempotencyMiddleware(store, stubVerifier{})
	h := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	req := httptest.NewRequest("POST", "/v1/projects", strings.NewReader("{}"))
	req.Header.Set("Authorization", "Bearer x")
	req.Header.Set("Idempotency-Key", "k1")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusConflict {
		t.Fatalf("pending → 409, got %d", rec.Code)
	}
}
```

- [ ] **Step 3: Run to verify it fails**

Run: `go test ./internal/api/ -run TestIdempotency -v`
Expected: FAIL — `undefined: idempotencyMiddleware`, `undefined: idemExisting`, `undefined: hashBody`.

- [ ] **Step 4: Implement the middleware**

```go
// internal/api/idempotency.go
package api

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"

	"github.com/steveokay/janus-secrets/internal/auth"
)

// idemExisting is the current record returned when a claim loses.
type idemExisting struct {
	Endpoint    string
	RequestHash string
	StatusCode  int // 0 = pending
}

// idemStore is the subset of *store.IdempotencyRepo the middleware needs (tests
// substitute a stub).
type idemStore interface {
	Claim(ctx context.Context, key, actor, endpoint, hash string) (bool, *idemExisting, error)
	Complete(ctx context.Context, key, actor string, status int) error
	Release(ctx context.Context, key, actor string) error
}

func hashBody(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func isMutating(m string) bool {
	return m == http.MethodPost || m == http.MethodPut || m == http.MethodDelete || m == http.MethodPatch
}

// idempotencyMiddleware honors a client-supplied Idempotency-Key on mutating
// /v1 requests. It stores only the final status (never the body), so once-shown
// secrets in a response never persist. Non-mutating methods, missing keys, and
// unauthenticated requests pass through untouched.
func idempotencyMiddleware(st idemStore, v authVerifier) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			key := r.Header.Get("Idempotency-Key")
			if key == "" || !isMutating(r.Method) {
				next.ServeHTTP(w, r)
				return
			}
			// Actor scoping needs the authenticated identity. This middleware is
			// mounted globally (before per-group RequireAuth), so resolve creds
			// here. On failure, pass through — downstream RequireAuth returns the
			// correct 401/503 and no idempotency row is created.
			p, _, err := resolvePrincipal(v, r)
			if err != nil || p.ID == "" {
				next.ServeHTTP(w, r)
				return
			}
			body, err := io.ReadAll(r.Body)
			if err != nil {
				// Body over the limit (MaxBytesReader) or read failure.
				writeError(w, http.StatusRequestEntityTooLarge, CodeValidation, "request body too large")
				return
			}
			r.Body = io.NopCloser(bytes.NewReader(body))
			endpoint := r.Method + " " + r.URL.Path
			hash := hashBody(body)

			claimed, existing, cerr := st.Claim(r.Context(), key, p.ID, endpoint, hash)
			if cerr != nil {
				writeError(w, http.StatusInternalServerError, CodeInternal, "internal error")
				return
			}
			if !claimed {
				switch {
				case existing.Endpoint != endpoint || existing.RequestHash != hash:
					writeError(w, http.StatusConflict, "idempotency_key_conflict",
						"Idempotency-Key reused with a different request")
				case existing.StatusCode == 0:
					writeError(w, http.StatusConflict, "idempotency_in_progress",
						"a request with this Idempotency-Key is still in progress")
				default:
					w.Header().Set("Idempotency-Replayed", "true")
					writeJSON(w, existing.StatusCode, map[string]any{"idempotent_replay": true})
				}
				return
			}
			// We won the claim: run the handler capturing only the status.
			sw := &statusWriter{ResponseWriter: w, status: http.StatusOK}
			next.ServeHTTP(sw, r)
			if sw.status >= 200 && sw.status < 300 {
				_ = st.Complete(r.Context(), key, p.ID, sw.status)
			} else {
				_ = st.Release(r.Context(), key, p.ID)
			}
		})
	}
}

var _ = auth.KindUser // keep the auth import if unused elsewhere; remove if not needed
```

> Remove the trailing `var _ =` line if the `auth` import is otherwise used; it's only a guard against an unused-import error while drafting.

- [ ] **Step 5: Adapt the concrete store to the `idemStore` interface**

`*store.IdempotencyRepo.Claim` returns `*store.IdemRecord`, but the middleware interface uses `*idemExisting`. Add a thin adapter in `internal/api/idempotency.go` so the real repo satisfies `idemStore`:

```go
// idemRepoAdapter adapts *store.IdempotencyRepo to the middleware's idemStore.
type idemRepoAdapter struct{ repo *store.IdempotencyRepo }

func (a idemRepoAdapter) Claim(ctx context.Context, key, actor, endpoint, hash string) (bool, *idemExisting, error) {
	claimed, rec, err := a.repo.Claim(ctx, key, actor, endpoint, hash)
	if err != nil || rec == nil {
		return claimed, nil, err
	}
	return claimed, &idemExisting{Endpoint: rec.Endpoint, RequestHash: rec.RequestHash, StatusCode: rec.StatusCode}, nil
}
func (a idemRepoAdapter) Complete(ctx context.Context, key, actor string, status int) error {
	return a.repo.Complete(ctx, key, actor, status)
}
func (a idemRepoAdapter) Release(ctx context.Context, key, actor string) error {
	return a.repo.Release(ctx, key, actor)
}
```

Add `"github.com/steveokay/janus-secrets/internal/store"` to the imports.

- [ ] **Step 6: Wire the middleware globally**

In `internal/api/server.go`, after `r.Use(RequireUnsealed(kr))` (line 108), add (guarded on the store + auth being wired, like other optional features):

```go
	if st != nil && authSvc != nil {
		r.Use(idempotencyMiddleware(idemRepoAdapter{repo: store.NewIdempotencyRepo(st)}, authSvc))
	}
```

> Use the actual local variable names in `Boot`/`New` for the store and auth service (recon shows `st` and `authSvc`). This runs before per-group `RequireAuth`, which is why the middleware resolves creds itself.

- [ ] **Step 7: Run to verify it passes**

Run: `go test ./internal/api/ -run TestIdempotency -v && go build ./...`
Expected: middleware tests PASS. (`go build` may still fail in `promotion_handlers.go` until Task 10 — that's expected; the middleware tests compile in isolation because they don't touch promotion.)

- [ ] **Step 8: Commit**

```bash
git add internal/api/middleware_auth.go internal/api/idempotency.go internal/api/idempotency_test.go internal/api/server.go
git commit -m "feat(api): global Idempotency-Key middleware (status-only replay)"
```

---

### Task 10: Retire promotion's bespoke idempotency

**Files:**
- Modify: `internal/api/promotion_handlers.go:248-386` (`handlePromoteApply` — remove inline claim/complete/release)
- Modify: promotion handler tests that assert the rich replay body

- [ ] **Step 1: Update the promotion idempotency test expectation**

Find the promotion test that exercises a replayed apply (asserts the `target_version`/`applied` body on replay). Change it to expect the generic replay contract instead: a second identical request with the same `Idempotency-Key` returns `{"idempotent_replay": true}` with header `Idempotency-Replayed: true` (the middleware now owns replay). If the test drives the handler directly (not through the router+middleware), convert it to assert that `handlePromoteApply` no longer performs its own claim (i.e., two direct calls both execute) — because idempotency now lives in the middleware, not the handler.

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/api/ -run 'Promote' -v`
Expected: FAIL — handler still contains the old idempotency code / old assertions.

- [ ] **Step 3: Strip the inline idempotency from `handlePromoteApply`**

Remove the `idemKey`/`idemActor`/`idem`/`Claim`/`existing`/replay block (promotion_handlers.go ~313-349) and the `Complete`/`Release` calls (~363-382). The handler still reads the body for its own apply logic — keep the `bodyBytes` read but drop the SHA256/idempotency usage. The apply now returns its normal `{target_version, applied, skipped}` JSON on the first (and, via the middleware, only) execution.

- [ ] **Step 4: Run to verify it passes**

Run: `go test ./internal/api/ -run 'Promote' -v && go build ./...`
Expected: PASS and the build is now green everywhere (the `store.PromotionIdempotencyRepo` reference is gone).

- [ ] **Step 5: Commit**

```bash
git add internal/api/promotion_handlers.go internal/api/promotion_handlers_test.go
git commit -m "refactor(api): retire promotion's bespoke idempotency (now middleware-owned)"
```

---

### Task 11: Idempotency value-free leak test

**Files:**
- Test: `internal/api/idempotency_leak_test.go` (new; full e2e against the testcontainer server)

- [ ] **Step 1: Write the leak test**

Drive the real router (the same e2e harness other `internal/api` tests use to boot a `*Server` against a testcontainer DB and authenticate as an admin/owner). Mint a service token via `POST /v1/tokens` **with** an `Idempotency-Key` header, capture the once-shown plaintext token from the response, then query the `idempotency` table directly and assert the token substring appears in NO column:

```go
func TestIdempotency_TokenNotPersisted(t *testing.T) {
	env := newAPITestEnv(t) // existing e2e harness: boots Server + DB, gives an owner session/token
	body := `{"name":"ci","scope_kind":"config","scope_id":"` + env.configID + `","access":"read"}`
	req := env.authedRequest("POST", "/v1/tokens", body)
	req.Header.Set("Idempotency-Key", "leak-key-1")
	resp := env.do(req)
	if resp.Code != http.StatusOK && resp.Code != http.StatusCreated {
		t.Fatalf("mint: %d %s", resp.Code, resp.Body.String())
	}
	var minted struct{ Token string `json:"token"` }
	_ = json.Unmarshal(resp.Body.Bytes(), &minted)
	if minted.Token == "" || !strings.HasPrefix(minted.Token, "janus_") {
		t.Fatalf("expected a plaintext token in the mint response, got %q", minted.Token)
	}
	// The idempotency row must NOT contain the token in any column.
	rows, err := env.store.Pool().Query(context.Background(),
		`SELECT idempotency_key, actor, endpoint, request_hash, status_code FROM idempotency`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	found := false
	for rows.Next() {
		var k, a, ep, h string
		var sc int
		if err := rows.Scan(&k, &a, &ep, &h, &sc); err != nil {
			t.Fatal(err)
		}
		found = true
		for _, col := range []string{k, a, ep, h} {
			if strings.Contains(col, minted.Token) {
				t.Fatalf("SECRET LEAK: minted token found in idempotency column %q", col)
			}
		}
	}
	if !found {
		t.Fatal("expected an idempotency row for the keyed mint")
	}
}
```

> Adapt `newAPITestEnv` / `authedRequest` / `env.store` / `Pool()` to the actual e2e helpers in `internal/api` tests. If a config-scoped token needs a seeded config, reuse the harness's existing seeding. If the dynamic-creds engine is wired in the harness, add an analogous assertion for a `POST …/creds` issued password; otherwise the token case is sufficient (both flow through the identical middleware, which never sees the body).

- [ ] **Step 2: Run to verify it passes**

Run: `go test ./internal/api/ -run TestIdempotency_TokenNotPersisted -v`
Expected: PASS — a keyed mint creates one idempotency row (status 200/201), and the plaintext token appears in none of its columns.

- [ ] **Step 3: Commit**

```bash
git add internal/api/idempotency_leak_test.go
git commit -m "test(api): idempotency storage is value-free (minted token never persisted)"
```

---

## Part 3 — Server hardening

### Task 12: HTTP timeouts (config + wiring)

**Files:**
- Modify: `internal/api/boot.go:23-55` (`BootConfig` — add fields)
- Modify: `internal/api/server.go:375-392` (`ListenAndServe` — set timeouts)
- Modify: `cmd/janus/server.go:30-94` (parse `JANUS_HTTP_*` durations)
- Test: `internal/api/server_test.go` (add a construction assertion)

- [ ] **Step 1: Write the failing test**

The `http.Server` is built inside `ListenAndServe`, which blocks. Refactor the server literal into a small helper so it's unit-testable, and assert the fields flow from config. Add to `internal/api/server_test.go`:

```go
func TestBuildHTTPServer_Timeouts(t *testing.T) {
	s := &Server{cfg: Config{
		ListenAddr:       ":9999",
		HTTPReadTimeout:  15 * time.Second,
		HTTPWriteTimeout: 0,
		HTTPIdleTimeout:  90 * time.Second,
	}}
	srv := s.buildHTTPServer()
	if srv.ReadTimeout != 15*time.Second || srv.IdleTimeout != 90*time.Second || srv.WriteTimeout != 0 {
		t.Fatalf("timeouts: read=%v write=%v idle=%v", srv.ReadTimeout, srv.WriteTimeout, srv.IdleTimeout)
	}
	if srv.ReadHeaderTimeout != 10*time.Second {
		t.Fatalf("ReadHeaderTimeout should stay 10s, got %v", srv.ReadHeaderTimeout)
	}
}
```

> `Config` is the internal server config populated from `BootConfig` in `Boot`. Check the exact field path (recon: `s.cfg.ListenAddr` exists, so `Config` holds `ListenAddr`); add the three timeout fields to that same `Config` struct and copy them from `BootConfig` in `Boot`.

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/api/ -run TestBuildHTTPServer_Timeouts -v`
Expected: FAIL — `undefined: (*Server).buildHTTPServer` and unknown `Config` fields.

- [ ] **Step 3: Add config fields + refactor ListenAndServe**

In `internal/api/boot.go`, add to `BootConfig`:

```go
	// HTTP server hardening. Zero on any field disables that timeout (Go's
	// default). cmd/janus applies production defaults (30s/0/120s); tests that
	// build BootConfig directly get zero (no timeouts).
	HTTPReadTimeout  time.Duration
	HTTPWriteTimeout time.Duration
	HTTPIdleTimeout  time.Duration
	HTTPMaxBodyBytes int64 // 0 = no limit
```

Copy them into the internal `Config` in `Boot` (find where `Config{...}` is built from `BootConfig` and add the four fields; also add the four fields to the `Config` struct definition). Then in `internal/api/server.go` replace the inline literal in `ListenAndServe` with a helper:

```go
func (s *Server) buildHTTPServer() *http.Server {
	return &http.Server{
		Addr:              s.cfg.ListenAddr,
		Handler:           s.router,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       s.cfg.HTTPReadTimeout,
		WriteTimeout:      s.cfg.HTTPWriteTimeout,
		IdleTimeout:       s.cfg.HTTPIdleTimeout,
	}
}

// ListenAndServe serves until ctx is canceled, then drains for up to 10s.
func (s *Server) ListenAndServe(ctx context.Context) error {
	srv := s.buildHTTPServer()
	errCh := make(chan error, 1)
	go func() { errCh <- srv.ListenAndServe() }()
	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return srv.Shutdown(shutdownCtx)
	}
}
```

- [ ] **Step 4: Parse env vars in cmd/janus**

In `cmd/janus/server.go`, following the existing `JANUS_SESSION_IDLE_TIMEOUT` idiom (lines 37-44), add before the `bc := api.BootConfig{...}` literal:

```go
	httpRead := 30 * time.Second
	if v := os.Getenv("JANUS_HTTP_READ_TIMEOUT"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil || d < 0 {
			return fmt.Errorf("invalid JANUS_HTTP_READ_TIMEOUT %q: use a Go duration like 30s, or 0 to disable", v)
		}
		httpRead = d
	}
	httpIdle := 120 * time.Second
	if v := os.Getenv("JANUS_HTTP_IDLE_TIMEOUT"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil || d < 0 {
			return fmt.Errorf("invalid JANUS_HTTP_IDLE_TIMEOUT %q: use a Go duration like 2m, or 0 to disable", v)
		}
		httpIdle = d
	}
	httpWrite := time.Duration(0) // disabled by default: audit export streams
	if v := os.Getenv("JANUS_HTTP_WRITE_TIMEOUT"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil || d < 0 {
			return fmt.Errorf("invalid JANUS_HTTP_WRITE_TIMEOUT %q: use a Go duration like 60s, or 0 to disable", v)
		}
		httpWrite = d
	}
	var httpMaxBody int64 = 10 << 20 // 10 MiB default
	if v := os.Getenv("JANUS_HTTP_MAX_BODY_BYTES"); v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil || n < 0 {
			return fmt.Errorf("invalid JANUS_HTTP_MAX_BODY_BYTES %q: use a non-negative byte count, or 0 to disable", v)
		}
		httpMaxBody = n
	}
```

Add to the `api.BootConfig{...}` literal:

```go
		HTTPReadTimeout:  httpRead,
		HTTPWriteTimeout: httpWrite,
		HTTPIdleTimeout:  httpIdle,
		HTTPMaxBodyBytes: httpMaxBody,
```

Add `"strconv"` to the imports if not present.

- [ ] **Step 5: Run to verify it passes + build**

Run: `go test ./internal/api/ -run TestBuildHTTPServer_Timeouts -v && go build ./cmd/...`
Expected: PASS and `cmd/janus` builds.

- [ ] **Step 6: Commit**

```bash
git add internal/api/boot.go internal/api/server.go internal/api/server_test.go cmd/janus/server.go
git commit -m "feat(server): configurable HTTP read/write/idle timeouts (JANUS_HTTP_*)"
```

---

### Task 13: Global request-body cap

**Files:**
- Create: `internal/api/bodylimit.go`
- Modify: `internal/api/server.go` (wire `bodyLimit` before the idempotency middleware)
- Test: `internal/api/bodylimit_test.go`

- [ ] **Step 1: Write the failing test**

```go
// internal/api/bodylimit_test.go
package api

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestBodyLimit(t *testing.T) {
	mw := bodyLimit(10) // 10-byte cap
	// Handler that reads the whole body and echoes read error as 400.
	h := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, err := io.ReadAll(r.Body); err != nil {
			w.WriteHeader(http.StatusRequestEntityTooLarge)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))

	// Under cap → OK.
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("POST", "/v1/projects", strings.NewReader("12345")))
	if rec.Code != http.StatusOK {
		t.Fatalf("under cap: got %d", rec.Code)
	}
	// Over cap → 413.
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("POST", "/v1/projects", strings.NewReader("this body is way over ten bytes")))
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("over cap: got %d", rec.Code)
	}
	// Restore endpoint is exempt even over cap.
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("POST", "/v1/sys/restore", strings.NewReader("this body is way over ten bytes")))
	if rec.Code != http.StatusOK {
		t.Fatalf("restore exempt: got %d", rec.Code)
	}

	// A 0 cap disables the limit.
	off := bodyLimit(0)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, err := io.ReadAll(r.Body); err != nil {
			w.WriteHeader(http.StatusRequestEntityTooLarge)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	rec = httptest.NewRecorder()
	off.ServeHTTP(rec, httptest.NewRequest("POST", "/v1/projects", strings.NewReader("this body is way over ten bytes")))
	if rec.Code != http.StatusOK {
		t.Fatalf("disabled cap: got %d", rec.Code)
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/api/ -run TestBodyLimit -v`
Expected: FAIL — `undefined: bodyLimit`.

- [ ] **Step 3: Implement the middleware**

```go
// internal/api/bodylimit.go
package api

import (
	"net/http"
	"strings"
)

// bodyLimit caps the request body at maxBytes via http.MaxBytesReader (a later
// read past the cap fails, and MaxBytesReader also writes a 413 to the
// ResponseWriter). maxBytes<=0 disables the cap. POST /v1/sys/restore is exempt:
// it streams a full instance backup (unbounded by design, with its own
// per-record 64MB bound).
func bodyLimit(maxBytes int64) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if maxBytes > 0 && !(r.Method == http.MethodPost && strings.HasPrefix(r.URL.Path, "/v1/sys/restore")) {
				r.Body = http.MaxBytesReader(w, r.Body, maxBytes)
			}
			next.ServeHTTP(w, r)
		})
	}
}
```

- [ ] **Step 4: Wire it before idempotency**

In `internal/api/server.go`, immediately after `r.Use(RequireUnsealed(kr))` (line 108) and **before** the idempotency `r.Use(...)` added in Task 9, insert:

```go
	if cfg.HTTPMaxBodyBytes > 0 {
		r.Use(bodyLimit(cfg.HTTPMaxBodyBytes))
	}
```

> Final global middleware order must be: `requestLogger` → `RequireUnsealed` → `bodyLimit` → `idempotency`. Use the actual config variable in scope (the internal `Config` copied from `BootConfig`; field `HTTPMaxBodyBytes`).

- [ ] **Step 5: Run to verify it passes + full build**

Run: `go test ./internal/api/ -run 'TestBodyLimit|TestIdempotency' -v && go build ./...`
Expected: PASS, whole tree builds.

- [ ] **Step 6: Commit**

```bash
git add internal/api/bodylimit.go internal/api/server.go internal/api/bodylimit_test.go
git commit -m "feat(server): global request-body cap (JANUS_HTTP_MAX_BODY_BYTES), restore exempt"
```

---

## Final verification (after all tasks)

- [ ] **Full test suite with race detector**

Run: `go test ./... -race`
Expected: all packages PASS, 0 races. Store/e2e tests apply migrations 000001–000020 on testcontainer boot.

- [ ] **Security gates**

Run: `GOTOOLCHAIN=go1.26.5 govulncheck ./...`
Expected: 0 vulnerabilities.
Run: `gosec -exclude-dir=internal/crypto/shamir ./...`
Expected: 0 issues.

- [ ] **Web unchanged**

Run: `cd web && npm run build && npm test`
Expected: PASS (no web files changed by this plan; opt-in `next_cursor` is ignored by existing callers).

- [ ] **Final holistic review**

Dispatch a final code reviewer over the whole branch diff against the spec (`docs/superpowers/specs/2026-07-15-api-hardening-design.md`), focusing on: (1) pagination keyset correctness + the token filtered-page cursor behavior, (2) the idempotency status-only guarantee (no body ever stored) and the leak test, (3) middleware ordering and the restore exemption, (4) value-free rule intact everywhere.

- [ ] **Finish the branch** via superpowers:finishing-a-development-branch.
