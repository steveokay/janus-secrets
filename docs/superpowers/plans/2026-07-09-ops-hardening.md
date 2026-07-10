# Ops Hardening Batch Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Close four operational-trust gaps: readiness/liveness probes, an authenticated `janus seal`, a server-enforced session idle timeout, and key-preserving full-instance backup/restore.

**Architecture:** Two PRs. PR 1 (branch `ops-probes-seal-idle`, already created, carries the spec) adds two unauthenticated probe endpoints under `/v1/sys/`, swaps `janus seal` onto the authenticated CLI client, and adds an idle check to `auth.Service.VerifySession`. PR 2 (branch `ops-backup-restore`, cut from PR 1's head) adds a logical JSONL dump/restore in `internal/store` (Postgres does the row serialization via `row_to_json`/`json_populate_record`), exposed as `GET /v1/sys/backup` (admin, audited) and `POST /v1/sys/restore` (pre-init, empty-instance-only), plus `janus backup`/`janus restore` CLI commands.

**Tech Stack:** Go stdlib + chi + pgx (existing), cobra CLI, testcontainers Postgres for e2e. No new dependencies, no new migrations.

**Spec:** `docs/superpowers/specs/2026-07-09-ops-hardening-design.md` — authoritative on behavior. One deliberate refinement vs the spec text: the ready endpoint's uninitialized code reuses the existing `not_initialized` vocabulary (`internal/api/errors.go:16`) instead of minting a near-duplicate `uninitialized`; and `oidc_auth_requests` (transient login state, expires in minutes) is excluded from backups for the same reason sessions are.

**Conventions (from CLAUDE.md / repo):** commits end with `Co-Authored-By: Claude Fable 5 <noreply@anthropic.com>`. Gates before each PR: `go build ./...`, `go vet ./...`, `go test ./...` (needs Docker for testcontainers), `gosec -exclude-dir=internal/crypto/shamir ./...`, `govulncheck ./...`. gosec findings get justified inline `// #nosec <ID> -- reason`, never silent suppression. Never log or return secret values.

---

## PR 1 — branch `ops-probes-seal-idle`

### Task 1: Readiness/liveness probes

**Files:**
- Modify: `internal/store/store.go` (add `Ping`)
- Create: `internal/api/sys_probes.go`
- Modify: `internal/api/errors.go` (add `CodeDBUnavailable`)
- Modify: `internal/api/server.go` (register `/live` + `/ready` in the `/v1/sys` route block, next to `/health`)
- Create: `internal/api/sys_probes_test.go`
- Modify: `docker-compose.yml:30` (healthcheck → `/v1/sys/ready`)

Background: `/v1/sys/*` is exempt from `RequireUnsealed` (`internal/api/middleware.go:22`), so no middleware work is needed. Unit-test servers are built by `newShamirTestServer` (`internal/api/harness_test.go:43`) with a **nil** `*store.Store` — `handleReady` must guard `s.st == nil` (skip the ping; unit servers have no DB).

- [ ] **Step 1: Write the failing unit tests**

Create `internal/api/sys_probes_test.go`:

```go
package api

import "testing"

func TestLiveAlways200(t *testing.T) {
	_, ts, _ := newShamirTestServer(t)
	var body struct{ Status string }
	if code := doJSON(t, "GET", ts.URL+"/v1/sys/live", "", &body); code != 200 || body.Status != "live" {
		t.Fatalf("live = %d %+v", code, body)
	}
}

func TestReadyUninitialized503(t *testing.T) {
	_, ts, _ := newShamirTestServer(t)
	var env errEnvelope
	if code := doJSON(t, "GET", ts.URL+"/v1/sys/ready", "", &env); code != 503 || env.Error.Code != CodeNotInitialized {
		t.Fatalf("ready = %d %+v (want 503 not_initialized)", code, env)
	}
}

func TestReadySealed503(t *testing.T) {
	srv, ts, _ := newShamirTestServer(t)
	// Initialize but do not unseal: init via the endpoint, then reseal.
	var ir struct{ Shares []string }
	if code := doJSON(t, "POST", ts.URL+"/v1/sys/init", `{"shares":1,"threshold":1}`, &ir); code != 200 {
		t.Fatalf("init: %d", code)
	}
	srv.keyring.Seal()
	var env errEnvelope
	if code := doJSON(t, "GET", ts.URL+"/v1/sys/ready", "", &env); code != 503 || env.Error.Code != CodeSealed {
		t.Fatalf("ready = %d %+v (want 503 sealed)", code, env)
	}
}
```

Note: check how `handleInit` behaves in the unit harness first — `TestReadySealed503` mirrors whatever `internal/api/sys_lifecycle_test.go` does to reach an initialized-but-sealed state in the in-memory harness; if init auto-unseals there, `srv.keyring.Seal()` (as shown) re-seals.

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/api/ -run 'TestLive|TestReady' -v`
Expected: FAIL — 404s (routes don't exist), `CodeDBUnavailable`/handlers undefined compile errors first.

- [ ] **Step 3: Implement**

Add to `internal/store/store.go` (next to the other Store methods):

```go
// Ping verifies database connectivity (used by the readiness probe).
func (s *Store) Ping(ctx context.Context) error {
	return s.pool.Ping(ctx)
}
```

Add to the const block in `internal/api/errors.go`:

```go
CodeDBUnavailable = "db_unavailable"
```

Create `internal/api/sys_probes.go`:

```go
package api

import (
	"context"
	"net/http"
	"time"
)

// handleLive reports process liveness only. It touches nothing (no DB, no
// keyring) so orchestrators can distinguish "process wedged" from "not ready".
func (s *Server) handleLive(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "live"})
}

// handleReady reports whether the instance can serve secret operations:
// database reachable AND seal initialized AND unsealed. Each failure mode has
// its own error code so probes and operators can tell them apart. Probes are
// deliberately not audited (they fire every few seconds and touch no secrets).
func (s *Server) handleReady(w http.ResponseWriter, r *http.Request) {
	// Unit-test servers are wired without a store; production always has one.
	if s.st != nil {
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		if err := s.st.Ping(ctx); err != nil {
			writeError(w, http.StatusServiceUnavailable, CodeDBUnavailable, "database unreachable")
			return
		}
	}
	initialized, _, err := s.sealConfig(r)
	if err != nil {
		writeError(w, http.StatusInternalServerError, CodeInternal, "internal error")
		return
	}
	if !initialized {
		writeError(w, http.StatusServiceUnavailable, CodeNotInitialized, "seal is not initialized")
		return
	}
	if s.keyring.Sealed() {
		writeError(w, http.StatusServiceUnavailable, CodeSealed, "server is sealed; unseal via /v1/sys/unseal")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ready"})
}
```

In `internal/api/server.go`, inside the `/v1/sys` route block right after `r.Get("/health", s.handleHealth)`:

```go
r.Get("/live", s.handleLive)
r.Get("/ready", s.handleReady)
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/api/ -run 'TestLive|TestReady|TestHealth' -v`
Expected: PASS (including the untouched `TestHealthAlways200`).

- [ ] **Step 5: Add an e2e ready test (real DB, incl. db-down)**

Append to `internal/api/sys_probes_test.go`:

```go
func TestReadyE2E(t *testing.T) {
	ts, _, _, _, _ := authStackFull(t) // initialized + unsealed + live DB
	var body struct{ Status string }
	if code := doJSON(t, "GET", ts.URL+"/v1/sys/ready", "", &body); code != 200 || body.Status != "ready" {
		t.Fatalf("ready = %d %+v", code, body)
	}
}
```

(A deterministic db-down probe test would require closing the shared pool mid-test, which poisons the rest of the stack; the `s.st.Ping` error branch is two lines and is covered by the nil-guard unit path plus code review. Do not close the store inside a shared harness.)

Run: `go test ./internal/api/ -run TestReadyE2E -v` (needs Docker)
Expected: PASS

- [ ] **Step 6: Point docker-compose at /ready**

In `docker-compose.yml:30` change the app healthcheck URL:

```yaml
      test: ["CMD", "wget", "-qO-", "http://127.0.0.1:8200/v1/sys/ready"]
```

(`wget` exits non-zero on HTTP 503, which is exactly the semantics we want: sealed/uninitialized = unhealthy.)

- [ ] **Step 7: Commit**

```bash
git add internal/store/store.go internal/api/sys_probes.go internal/api/sys_probes_test.go internal/api/errors.go internal/api/server.go docker-compose.yml
git commit -m "feat(api): /v1/sys/live and /v1/sys/ready probes (sealed/uninitialized/db-down aware)"
```

---

### Task 2: `janus seal` sends credentials

**Files:**
- Modify: `cmd/janus/sys_commands.go:199-214` (`newSealCmd`)
- Modify: `cmd/janus/sys_commands_test.go`

Background: production wires `POST /v1/sys/seal` behind `RequireAuth` + `sys:seal` (`internal/api/server.go:76`), but `newSealCmd` uses the credential-less `sysCall` (`cmd/janus/client.go:35`) → guaranteed 401. The authenticated client `newAPIClient(flagAddr, flagToken)` (`cmd/janus/apiclient.go:20`) already resolves `--token` > `JANUS_TOKEN` > stored session (`cmd/janus/config_store.go:116`) and rewrites 401/403 into actionable hints (`apiclient.go:91`). `janus logout` (`cmd/janus/login.go:76`) is the pattern to mirror; note its `--address` flag default is `""` so `resolveAddress` can fall back to the stored login address — the fixed `defaultAddress()` default on the current seal cmd defeats that and must go.

- [ ] **Step 1: Write the failing tests**

Add to `cmd/janus/sys_commands_test.go` (mirror the existing mux-server pattern used at line 67):

```go
func TestSealSendsBearerToken(t *testing.T) {
	t.Setenv("JANUS_CONFIG_DIR", t.TempDir()) // isolate stored auth
	t.Setenv("JANUS_TOKEN", "")
	var gotAuth string
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/sys/seal", func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"sealed":true}`)
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	cmd := newSealCmd()
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	cmd.SetArgs([]string{"--address", ts.URL, "--token", "janus_svc_abc"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if gotAuth != "Bearer janus_svc_abc" {
		t.Fatalf("Authorization = %q, want Bearer janus_svc_abc", gotAuth)
	}
}

func TestSealSendsStoredSession(t *testing.T) {
	t.Setenv("JANUS_CONFIG_DIR", t.TempDir())
	t.Setenv("JANUS_TOKEN", "")
	var gotCookie string
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/sys/seal", func(w http.ResponseWriter, r *http.Request) {
		if c, err := r.Cookie("janus_session"); err == nil {
			gotCookie = c.Value
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"sealed":true}`)
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()
	if err := saveAuth(&authState{Address: ts.URL, Session: "sess123"}); err != nil {
		t.Fatal(err)
	}

	cmd := newSealCmd()
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	cmd.SetArgs([]string{"--address", ts.URL})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if gotCookie != "sess123" {
		t.Fatalf("session cookie = %q, want sess123", gotCookie)
	}
}

func TestSealAuthErrorIsActionable(t *testing.T) {
	t.Setenv("JANUS_CONFIG_DIR", t.TempDir())
	t.Setenv("JANUS_TOKEN", "")
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/sys/seal", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		fmt.Fprint(w, `{"error":{"code":"unauthenticated","message":"authentication required"}}`)
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	cmd := newSealCmd()
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	cmd.SetArgs([]string{"--address", ts.URL})
	err := cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "janus login") {
		t.Fatalf("want a 'janus login' hint, got %v", err)
	}
}
```

(Add any missing imports: `fmt`, `io`, `net/http`, `net/http/httptest`, `strings`.)

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./cmd/janus/ -run TestSeal -v`
Expected: `TestSealSendsBearerToken` FAILS (`unknown flag: --token`), the others fail on missing auth header / wrong error text.

- [ ] **Step 3: Rewrite `newSealCmd`**

Replace the function at `cmd/janus/sys_commands.go:199-214`:

```go
func newSealCmd() *cobra.Command {
	var address, token string
	cmd := &cobra.Command{
		Use:   "seal",
		Short: "Seal the server (wipes the in-memory master key)",
		RunE: func(cmd *cobra.Command, _ []string) error {
			// Sealing requires sys:seal (admin) — unlike init/unseal/seal-status,
			// which must work pre-auth. Uses the same credential resolution as
			// the secrets commands: --token > JANUS_TOKEN > stored session.
			c, err := newAPIClient(address, token)
			if err != nil {
				return err
			}
			if err := c.call("POST", "/v1/sys/seal", nil, nil); err != nil {
				return err
			}
			cmd.Println("sealed")
			return nil
		},
	}
	cmd.Flags().StringVar(&address, "address", "", "server address (default: stored login address)")
	cmd.Flags().StringVar(&token, "token", "", "service token (overrides stored session)")
	return cmd
}
```

If an existing test in `sys_commands_test.go` (e.g. around line 194 where `/v1/sys/seal` appears) asserted the old unauthenticated behavior, update it to set `JANUS_CONFIG_DIR`/`--token` accordingly rather than deleting it.

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./cmd/janus/ -v`
Expected: PASS (whole package — catches regressions in other sys commands).

- [ ] **Step 5: Commit**

```bash
git add cmd/janus/sys_commands.go cmd/janus/sys_commands_test.go
git commit -m "fix(cli): janus seal authenticates (--token / stored session) — was guaranteed 401 in production"
```

---

### Task 3: Session inactivity timeout

**Files:**
- Modify: `internal/auth/errors.go` (add `ErrSessionExpired`)
- Modify: `internal/auth/service.go` (field + setter)
- Modify: `internal/auth/sessions.go:79-108` (`VerifySession` idle check)
- Modify: `internal/auth/sessions_test.go` (new tests)
- Modify: `internal/api/errors.go` (add `CodeSessionExpired`)
- Modify: `internal/api/middleware_auth.go:58-72` (map the new error)
- Create/extend: `internal/api/middleware_auth_test.go` or the existing middleware test file (401-code test)
- Modify: `internal/api/boot.go` (`BootConfig.SessionIdleTimeout` + wire-through)
- Modify: `cmd/janus/server.go:28-51` (parse `JANUS_SESSION_IDLE_TIMEOUT`)

Design notes: the **default (30m) is applied in `cmd/janus/server.go`**, not in `Boot` — so `BootConfig{}`'s zero value means *disabled*, and every existing e2e test that calls `Boot` directly keeps working unchanged. `sessions.last_seen_at` is `NOT NULL DEFAULT now()` (migration 000002) and already slides on every request via `TouchLastSeen` (`internal/store/sessions.go:43`). Idle enforcement applies to session cookies only; service tokens keep their own `expires_at` semantics.

- [ ] **Step 1: Write the failing auth-layer tests**

Add to `internal/auth/sessions_test.go` (harness: `newTestService` + `resetPool` from `internal/auth/harness_test.go:123,21`):

```go
func TestVerifySessionIdleTimeout(t *testing.T) {
	svc, email, password := newTestService(t)
	svc.SetSessionIdleTimeout(30 * time.Minute)
	ctx := context.Background()
	cookie, err := svc.Login(ctx, email, []byte(password))
	if err != nil {
		t.Fatal(err)
	}
	// Fresh session verifies.
	if _, err := svc.VerifySession(ctx, cookie); err != nil {
		t.Fatalf("fresh session: %v", err)
	}
	// Backdate last_seen_at beyond the idle window.
	if _, err := resetPool.Exec(ctx,
		`UPDATE sessions SET last_seen_at = now() - interval '31 minutes'`); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.VerifySession(ctx, cookie); !errors.Is(err, ErrSessionExpired) {
		t.Fatalf("want ErrSessionExpired, got %v", err)
	}
	// The idle-expired session row was deleted.
	var n int
	if err := resetPool.QueryRow(ctx, `SELECT count(*) FROM sessions`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("idle-expired session not deleted (%d rows)", n)
	}
}

func TestVerifySessionIdleDisabled(t *testing.T) {
	svc, email, password := newTestService(t)
	// Zero (the default) disables idle enforcement entirely.
	ctx := context.Background()
	cookie, err := svc.Login(ctx, email, []byte(password))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := resetPool.Exec(ctx,
		`UPDATE sessions SET last_seen_at = now() - interval '23 hours'`); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.VerifySession(ctx, cookie); err != nil {
		t.Fatalf("idle-disabled session should verify: %v", err)
	}
}

func TestVerifySessionAbsoluteTTLStillEnforced(t *testing.T) {
	svc, email, password := newTestService(t)
	svc.SetSessionIdleTimeout(30 * time.Minute)
	ctx := context.Background()
	cookie, err := svc.Login(ctx, email, []byte(password))
	if err != nil {
		t.Fatal(err)
	}
	// Recently active but past the 24h absolute expiry → plain unauthenticated.
	if _, err := resetPool.Exec(ctx,
		`UPDATE sessions SET expires_at = now() - interval '1 minute', last_seen_at = now()`); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.VerifySession(ctx, cookie); !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("want ErrUnauthenticated, got %v", err)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/auth/ -run TestVerifySessionIdle -v` (needs Docker)
Expected: compile FAIL — `SetSessionIdleTimeout` and `ErrSessionExpired` undefined.

- [ ] **Step 3: Implement the auth layer**

`internal/auth/errors.go` — add to the var block:

```go
// ErrSessionExpired is returned when a session exceeded the configured
// inactivity window. Distinct from ErrUnauthenticated so the API can tell
// the (previously authenticated) caller why they were logged out.
ErrSessionExpired = errors.New("auth: session expired due to inactivity")
```

`internal/auth/service.go` — add a field to `Service` (after `keyring *crypto.Keyring`):

```go
	// idleTimeout is the session inactivity window; 0 disables enforcement.
	idleTimeout time.Duration
```

and a setter (near `NewService`; add `"time"` to imports):

```go
// SetSessionIdleTimeout configures the session inactivity window (0 disables).
// Called once during boot, before the server serves requests.
func (s *Service) SetSessionIdleTimeout(d time.Duration) { s.idleTimeout = d }
```

`internal/auth/sessions.go` — in `VerifySession`, insert between the absolute-expiry check (line 95-98) and `TouchLastSeen` (line 99):

```go
	if s.idleTimeout > 0 {
		last := sess.LastSeenAt
		if last.IsZero() { // defensive: column is NOT NULL, but stay safe
			last = sess.CreatedAt
		}
		if time.Since(last) > s.idleTimeout {
			_ = s.sessions.Delete(ctx, sess.ID) // opportunistic cleanup
			return Principal{}, ErrSessionExpired
		}
	}
```

- [ ] **Step 4: Run auth tests**

Run: `go test ./internal/auth/ -v`
Expected: PASS (all three new tests + no regressions).

- [ ] **Step 5: Write the failing API-layer test (401 code)**

Add to the file containing the existing `RequireAuth`/middleware tests (`internal/api/middleware_test.go` has one at line ~50; put this beside it or in a new `internal/api/middleware_auth_test.go`). The stub implements the `authVerifier` interface consumed at `internal/api/middleware_auth.go:50-53` (`VerifyServiceToken` + `VerifySession`) — check the interface definition at the top of `middleware_auth.go` and match it exactly:

```go
type idleExpiredVerifier struct{}

func (idleExpiredVerifier) VerifySession(context.Context, string) (auth.Principal, error) {
	return auth.Principal{}, auth.ErrSessionExpired
}

func (idleExpiredVerifier) VerifyServiceToken(context.Context, string) (auth.Principal, *auth.TokenScope, error) {
	return auth.Principal{}, nil, auth.ErrUnauthenticated
}

func TestRequireAuthIdleExpired401(t *testing.T) {
	h := RequireAuth(idleExpiredVerifier{})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("handler must not run")
	}))
	req := httptest.NewRequest("GET", "/v1/projects", nil)
	req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: "stale"})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 401 {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
	var env errEnvelope
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatal(err)
	}
	if env.Error.Code != CodeSessionExpired {
		t.Fatalf("code = %q, want session_expired", env.Error.Code)
	}
}
```

Run: `go test ./internal/api/ -run TestRequireAuthIdleExpired -v`
Expected: compile FAIL (`CodeSessionExpired` undefined).

- [ ] **Step 6: Implement the API layer**

`internal/api/errors.go` — add to the const block:

```go
CodeSessionExpired = "session_expired"
```

`internal/api/middleware_auth.go` — in the `switch` at line 58, add a case **before** the generic `ErrUnauthenticated` case:

```go
			case errors.Is(err, auth.ErrSessionExpired):
				writeError(w, http.StatusUnauthorized, CodeSessionExpired,
					"session expired due to inactivity")
```

Run: `go test ./internal/api/ -run TestRequireAuth -v`
Expected: PASS.

(No frontend change: `web/src` maps every 401 to clear-cache-and-login via the global auth-event handler.)

- [ ] **Step 7: Wire configuration through boot**

`internal/api/boot.go` — add to `BootConfig` (after `SealType`):

```go
	// SessionIdleTimeout is the UI-session inactivity window. Zero disables
	// idle enforcement (the 30m production default is applied by cmd/janus,
	// so tests that build BootConfig directly get no idle timeout).
	SessionIdleTimeout time.Duration
```

(add `"time"` to imports) and after `authSvc := auth.NewService(st, kr)` (line 103):

```go
	authSvc.SetSessionIdleTimeout(bc.SessionIdleTimeout)
```

`cmd/janus/server.go` — in `runServer`, before `bc := api.BootConfig{...}`:

```go
	idle := 30 * time.Minute // production default; 0 disables
	if v := os.Getenv("JANUS_SESSION_IDLE_TIMEOUT"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil || d < 0 {
			return fmt.Errorf("invalid JANUS_SESSION_IDLE_TIMEOUT %q: use a Go duration like 30m, or 0 to disable", v)
		}
		idle = d
	}
```

add `SessionIdleTimeout: idle,` to the `api.BootConfig{...}` literal, and add `"fmt"` + `"time"` to imports.

- [ ] **Step 8: Full-package verification and commit**

Run: `go build ./... && go vet ./... && go test ./internal/auth/ ./internal/api/ ./cmd/janus/`
Expected: all PASS.

```bash
git add internal/auth/ internal/api/ cmd/janus/server.go
git commit -m "feat(auth): server-enforced session idle timeout (JANUS_SESSION_IDLE_TIMEOUT, default 30m, 0 disables)"
```

---

### Task 4: PR 1 gates and pull request

- [ ] **Step 1: Run the full gate suite**

```bash
go build ./... && go vet ./...
go test ./...
gosec -exclude-dir=internal/crypto/shamir ./...
govulncheck ./...
```
Expected: all clean (Docker must be running for testcontainers). Fix anything that surfaces; gosec findings get justified inline `// #nosec <ID> -- reason` only when genuinely false-positive.

- [ ] **Step 2: Update `status.md`** — add an ops-hardening section recording items 1-3 as done (follow the file's existing per-milestone format).

- [ ] **Step 3: Push and open PR 1**

```bash
git push -u origin ops-probes-seal-idle
gh pr create --title "Ops hardening 1/2: ready/live probes, authenticated janus seal, session idle timeout" --body "<summary per spec docs/superpowers/specs/2026-07-09-ops-hardening-design.md §1-3; note docker-compose healthcheck now targets /v1/sys/ready>

🤖 Generated with [Claude Code](https://claude.com/claude-code)"
```
(The user merges PRs themselves — do not merge.)

---

## PR 2 — branch `ops-backup-restore` (cut from `ops-probes-seal-idle` HEAD; rebase onto main after PR 1 merges if needed)

### Task 5: Store — schema version, emptiness check, dump

**Files:**
- Create: `internal/store/backup.go`

The dump uses Postgres itself for serialization: `SELECT row_to_json(t)::text FROM <table> t` emits one JSON object per row (bytea as `"\\x…"` hex, timestamptz as ISO-8601, `text[]`/`jsonb` as JSON — all of which `json_populate_record` parses back losslessly on restore). Tests for this task land in Task 7/9 at the API level (the api package's testcontainers harness has verified helper symbols; this keeps every test against the real wire path). Verify with `go build` here.

- [ ] **Step 1: Create `internal/store/backup.go`**

```go
package store

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/jackc/pgx/v5"
)

// backupTable names one table in the logical dump. The slice order is
// FK-safe for insertion (parents before children); restore enforces it.
type backupTable struct {
	name    string
	orderBy string
}

// backupTables is the full-instance dump set. Excluded on purpose:
// sessions and oidc_auth_requests (ephemeral login state — everyone
// re-authenticates after a restore) and schema_migrations (owned by
// golang-migrate; the header pins the version instead).
var backupTables = []backupTable{
	{"seal_config", "id"},
	{"auth_config", "id"},
	{"users", "created_at, id"},
	{"oidc_providers", "created_at, id"},
	{"oidc_identities", "created_at, id"},
	{"oidc_federation_config", "created_at, id"},
	{"oidc_federation_bindings", "created_at, id"},
	{"projects", "created_at, id"},
	{"environments", "created_at, id"},
	{"role_bindings", "created_at, id"},
	{"configs", "created_at, id"},
	{"config_versions", "config_id, version"},
	{"secret_values", "created_at, id"},
	{"config_version_entries", "config_version_id, key"},
	{"service_tokens", "created_at, id"},
	{"transit_keys", "created_at, id"},
	{"transit_key_versions", "transit_key_id, version"},
	{"audit_events", "seq"},
}

// SchemaVersion returns the applied golang-migrate version. A dirty
// migration state is an error (never back up or restore over one).
func (s *Store) SchemaVersion(ctx context.Context) (int64, error) {
	var v int64
	var dirty bool
	if err := s.pool.QueryRow(ctx,
		`SELECT version, dirty FROM schema_migrations`).Scan(&v, &dirty); err != nil {
		return 0, mapError(err)
	}
	if dirty {
		return 0, errors.New("store: schema_migrations is dirty")
	}
	return v, nil
}

// IsEmptyForRestore reports whether the instance is empty enough to restore
// into: no seal config, no users, no projects (the state of a freshly
// migrated database, before /v1/sys/init).
func (s *Store) IsEmptyForRestore(ctx context.Context) (bool, error) {
	var seals, users, projects int
	err := s.pool.QueryRow(ctx, `SELECT
		(SELECT count(*) FROM seal_config),
		(SELECT count(*) FROM users),
		(SELECT count(*) FROM projects)`).Scan(&seals, &users, &projects)
	if err != nil {
		return false, mapError(err)
	}
	return seals == 0 && users == 0 && projects == 0, nil
}

// DumpBackup streams every backup table to w as JSONL records:
// {"table":"<name>","row":{...}}. Rows are emitted exactly as stored —
// wrapped keys, ciphertexts, and hashes stay wrapped (key-preserving dump;
// the output contains no plaintext secrets by construction). pgx streams
// result rows, so large tables (audit_events) never buffer in memory.
func (s *Store) DumpBackup(ctx context.Context, w io.Writer) error {
	for _, t := range backupTables {
		// #nosec G201 -- identifiers come from the fixed compile-time backupTables list, not user input.
		q := fmt.Sprintf(`SELECT row_to_json(t)::text FROM %s t ORDER BY %s`, t.name, t.orderBy)
		if err := dumpTable(ctx, s, t.name, q, w); err != nil {
			return err
		}
	}
	return nil
}

func dumpTable(ctx context.Context, s *Store, name, query string, w io.Writer) error {
	rows, err := s.pool.Query(ctx, query)
	if err != nil {
		return mapError(err)
	}
	defer rows.Close()
	for rows.Next() {
		var rowJSON string
		if err := rows.Scan(&rowJSON); err != nil {
			return mapError(err)
		}
		if _, err := fmt.Fprintf(w, "{\"table\":%q,\"row\":%s}\n", name, rowJSON); err != nil {
			return err
		}
	}
	return mapError(rows.Err())
}
```

Note: `mapError(nil)` must return nil — check `internal/store/errors.go:27`; if it does not handle nil, guard `rows.Err()` with an explicit `if err := rows.Err(); err != nil { return mapError(err) }`.

- [ ] **Step 2: Build**

Run: `go build ./internal/store/`
Expected: compiles clean.

- [ ] **Step 3: Commit**

```bash
git add internal/store/backup.go
git commit -m "feat(store): logical JSONL dump — SchemaVersion, IsEmptyForRestore, DumpBackup"
```

---

### Task 6: Store — restore

**Files:**
- Modify: `internal/store/backup.go`

- [ ] **Step 1: Append `RestoreBackup` to `internal/store/backup.go`**

```go
// RestoreBackup inserts records supplied by next() inside one transaction.
// next returns (table, rowJSON, nil) per record and io.EOF when done. Records
// must arrive in backupTables order (the dump's order) — that guarantees FK
// parents land before children. Any error rolls the whole restore back,
// leaving the instance empty and restorable.
func (s *Store) RestoreBackup(ctx context.Context, next func() (string, []byte, error)) error {
	order := make(map[string]int, len(backupTables))
	for i, t := range backupTables {
		order[t.name] = i
	}
	lastIdx := -1
	return s.withTx(ctx, func(tx pgx.Tx) error {
		for {
			table, row, err := next()
			if errors.Is(err, io.EOF) {
				return nil
			}
			if err != nil {
				return err
			}
			idx, ok := order[table]
			if !ok {
				return fmt.Errorf("store: unknown backup table %q", table)
			}
			if idx < lastIdx {
				return fmt.Errorf("store: backup records out of order at %q", table)
			}
			lastIdx = idx
			// json_populate_record maps JSON fields onto the table's row type;
			// SELECT * preserves column order for the bare INSERT.
			// #nosec G201 -- identifier from the fixed compile-time backupTables list, not user input.
			q := fmt.Sprintf(
				`INSERT INTO %s SELECT * FROM json_populate_record(NULL::%s, $1::json)`,
				table, table)
			if _, err := tx.Exec(ctx, q, string(row)); err != nil {
				return mapError(err)
			}
		}
	})
}
```

(`withTx` is `internal/store/store.go:42`; it owns commit/rollback.)

- [ ] **Step 2: Build**

Run: `go build ./internal/store/`
Expected: compiles clean.

- [ ] **Step 3: Commit**

```bash
git add internal/store/backup.go
git commit -m "feat(store): transactional ordered RestoreBackup from JSONL records"
```

---

### Task 7: API — authz action, handlers, routes, version plumbing, first e2e

**Files:**
- Modify: `internal/authz/actions.go` (add `SysBackup`, grant to admin)
- Create: `internal/api/backup_handlers.go`
- Modify: `internal/api/server.go` (routes; both auth and nil-auth branches)
- Modify: `internal/api/boot.go` + `cmd/janus/server.go` (version plumbing)
- Create: `internal/api/backup_e2e_test.go`

- [ ] **Step 1: authz action**

`internal/authz/actions.go` — add to the const block after `SysSeal`:

```go
SysBackup Action = "sys:backup" // instance-scoped
```

and add `SysBackup` to the `adminActions` set (line 63-65), next to `SysSeal`.

If `internal/authz` has a role-matrix table test enumerating admin actions, extend it; the package is held to 100% coverage — run `go test ./internal/authz/ -cover` and confirm it stays at 100%.

- [ ] **Step 2: Version plumbing**

`internal/api/server.go` — add `Version string` to the `Config` struct (next to `SealType`).
`internal/api/boot.go` — add `Version string` to `BootConfig`; pass it through in the `New(Config{ListenAddr: bc.ListenAddr, SealType: sealType, Version: bc.Version}, …)` call (line 118).
`cmd/janus/server.go` — add `Version: version,` to the `api.BootConfig{...}` literal (the `version` var is `cmd/janus/main.go:11`).

- [ ] **Step 3: Write the failing e2e tests**

Create `internal/api/backup_e2e_test.go`:

```go
package api

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/steveokay/janus-secrets/internal/secrets"
)

// backupRaw GETs /v1/sys/backup with a session cookie and returns status+body.
func backupRaw(t *testing.T, base, cookie string) (int, string) {
	t.Helper()
	req, err := http.NewRequest("GET", base+"/v1/sys/backup", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: cookie})
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var sb strings.Builder
	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 0, 1<<20), 64<<20)
	for sc.Scan() {
		sb.WriteString(sc.Text())
		sb.WriteByte('\n')
	}
	if err := sc.Err(); err != nil {
		t.Fatal(err)
	}
	return resp.StatusCode, sb.String()
}

func TestBackupE2E(t *testing.T) {
	ts, srv, email, password, cid := authStackFull(t)
	cookie := login(t, ts.URL, email, password)
	if _, err := srv.service.SetSecrets(context.Background(), cid, []secrets.SecretChange{
		{Key: "CANARY", Value: []byte("plaintext-canary-8d1f")},
	}, "seed", "test"); err != nil {
		t.Fatal(err)
	}

	code, body := backupRaw(t, ts.URL, cookie)
	if code != 200 {
		t.Fatalf("backup: %d", code)
	}
	lines := strings.Split(strings.TrimRight(body, "\n"), "\n")

	// Header first: format 1 + a positive migration version.
	var hdr struct {
		JanusBackup      int   `json:"janus_backup"`
		MigrationVersion int64 `json:"migration_version"`
	}
	if err := json.Unmarshal([]byte(lines[0]), &hdr); err != nil {
		t.Fatalf("header: %v (%s)", err, lines[0])
	}
	if hdr.JanusBackup != 1 || hdr.MigrationVersion < 1 {
		t.Fatalf("header = %+v", hdr)
	}
	// Contains the seeded structure and, critically, NO plaintext.
	if !strings.Contains(body, `"table":"projects"`) || !strings.Contains(body, `"table":"secret_values"`) {
		t.Fatalf("dump missing tables")
	}
	if strings.Contains(body, "plaintext-canary-8d1f") {
		t.Fatal("backup leaked a plaintext secret value")
	}
	// Audited.
	_, exp := rawGet(t, ts.URL+"/v1/audit/export?format=jsonl&action=sys.backup", cookie)
	if !strings.Contains(exp, "sys.backup") {
		t.Fatal("sys.backup audit event missing")
	}
}

func TestBackupForbiddenForNonAdminToken(t *testing.T) {
	ts, _, email, password, cid := authStackFull(t)
	cookie := login(t, ts.URL, email, password)
	// Mint a read-only config-scoped token (uses the wire shape from tokens e2e).
	var mint struct{ Token string `json:"token"` }
	if code := doAuthed(t, "POST", ts.URL+"/v1/tokens",
		cookie, "", `{"name":"ci","scope_kind":"config","scope_id":"`+cid+`","access":"read"}`, &mint); code != 200 && code != 201 {
		t.Fatalf("mint: %d", code)
	}
	if code := doAuthed(t, "GET", ts.URL+"/v1/sys/backup", "", mint.Token, "", nil); code != 403 {
		t.Fatalf("backup with scoped token = %d, want 403", code)
	}
}

func TestRestoreRefusedOnNonEmptyInstance(t *testing.T) {
	ts, _, _, _, _ := authStackFull(t) // initialized == not empty
	var env errEnvelope
	if code := doJSON(t, "POST", ts.URL+"/v1/sys/restore",
		`{"janus_backup":1,"migration_version":1}`, &env); code != 409 || env.Error.Code != "not_empty" {
		t.Fatalf("restore on live instance = %d %+v (want 409 not_empty)", code, env)
	}
}
```

Before running: confirm the token-mint request/response field names against `internal/api/tokens` handler or an existing tokens e2e test (`internal/api/authz_e2e_test.go` mints tokens) and adjust `TestBackupForbiddenForNonAdminToken` to the real wire shape. Also confirm `rawGet`'s signature in the audit e2e test file and match it.

Run: `go test ./internal/api/ -run 'TestBackup|TestRestore' -v`
Expected: FAIL — 404 (routes missing).

- [ ] **Step 4: Implement handlers**

Create `internal/api/backup_handlers.go`:

```go
package api

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/steveokay/janus-secrets/internal/audit"
)

// backupHeader is line 1 of a dump; restore validates it before any insert.
type backupHeader struct {
	JanusBackup      int    `json:"janus_backup"`
	MigrationVersion int64  `json:"migration_version"`
	JanusVersion     string `json:"janus_version"`
	CreatedAt        string `json:"created_at"`
}

// backupRecord is every subsequent line.
type backupRecord struct {
	Table string          `json:"table"`
	Row   json.RawMessage `json:"row"`
}

// handleBackup streams a key-preserving full-instance dump. Auth-gated in
// production via RequireAuth + sys:backup (see New); rows are emitted exactly
// as stored, so the stream contains no plaintext secrets by construction.
func (s *Server) handleBackup(w http.ResponseWriter, r *http.Request) {
	ver, err := s.st.SchemaVersion(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, CodeInternal, "internal error")
		return
	}
	// Audit BEFORE streaming: once the body starts we cannot switch to an
	// error response (same rule as the audit export handler). Audit-write
	// failure fails the request.
	if err := s.record(r, "sys.backup", "", "success", "", ""); err != nil {
		writeError(w, http.StatusInternalServerError, CodeInternal, "internal error")
		return
	}
	now := time.Now().UTC()
	w.Header().Set("Content-Type", "application/x-ndjson")
	w.Header().Set("Content-Disposition",
		fmt.Sprintf("attachment; filename=%q", "janus-backup-"+now.Format("20060102T150405Z")+".jsonl"))
	hdr, err := json.Marshal(backupHeader{
		JanusBackup:      1,
		MigrationVersion: ver,
		JanusVersion:     s.cfg.Version,
		CreatedAt:        now.Format(time.RFC3339),
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, CodeInternal, "internal error")
		return
	}
	if _, err := w.Write(append(hdr, '\n')); err != nil {
		return // client went away; nothing to do
	}
	if err := s.st.DumpBackup(r.Context(), w); err != nil {
		// Headers are committed; a truncated stream fails restore's
		// transaction safely on the other end. Log and stop.
		s.logger.Warn("backup stream failed", "err", err)
	}
}

// handleRestore rebuilds an EMPTY instance from a dump. Like /v1/sys/init it
// is a pre-auth bootstrap operation: no credentials exist to check on an
// empty instance, and the emptiness gate (plus initMu) is the guard.
func (s *Server) handleRestore(w http.ResponseWriter, r *http.Request) {
	// Serialize against init (and concurrent restores) — same mutex, same
	// reasoning as handleInit.
	s.initMu.Lock()
	defer s.initMu.Unlock()

	empty, err := s.st.IsEmptyForRestore(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, CodeInternal, "internal error")
		return
	}
	if !empty {
		writeError(w, http.StatusConflict, CodeNotEmpty,
			"restore requires an empty instance (fresh database, before init)")
		return
	}

	dec := json.NewDecoder(bufio.NewReaderSize(r.Body, 1<<20))
	var hdr backupHeader
	if err := dec.Decode(&hdr); err != nil {
		writeError(w, http.StatusBadRequest, CodeValidation, "missing or invalid backup header")
		return
	}
	if hdr.JanusBackup != 1 {
		writeError(w, http.StatusUnprocessableEntity, CodeValidation,
			fmt.Sprintf("unsupported backup format version %d (this server reads version 1)", hdr.JanusBackup))
		return
	}
	ver, err := s.st.SchemaVersion(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, CodeInternal, "internal error")
		return
	}
	if hdr.MigrationVersion != ver {
		writeError(w, http.StatusUnprocessableEntity, CodeValidation,
			fmt.Sprintf("backup schema version %d does not match server schema version %d; run the janus version that wrote this backup",
				hdr.MigrationVersion, ver))
		return
	}

	err = s.st.RestoreBackup(r.Context(), func() (string, []byte, error) {
		var rec backupRecord
		if err := dec.Decode(&rec); err != nil {
			return "", nil, err // io.EOF terminates cleanly
		}
		return rec.Table, rec.Row, nil
	})
	if err != nil {
		s.logger.Warn("restore failed; transaction rolled back", "err", err)
		writeError(w, http.StatusUnprocessableEntity, CodeValidation,
			"restore failed; the instance is unchanged (see server log)")
		return
	}

	// Append sys.restore to the restored hash chain: the audit store reads
	// the chain head from the table, so continuity is automatic and
	// GET /v1/audit/verify passes across the restore boundary.
	if err := s.recordActor(r, audit.Actor{Kind: "anonymous"},
		"sys.restore", "", "success", "", ""); err != nil {
		writeError(w, http.StatusInternalServerError, CodeInternal, "internal error")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"restored": true, "sealed": true})
}
```

Add to `internal/api/errors.go` const block:

```go
CodeNotEmpty = "not_empty"
```

- [ ] **Step 5: Register routes**

`internal/api/server.go`, in the `/v1/sys` block: add next to the probe routes (works pre-auth):

```go
r.Post("/restore", s.handleRestore)
```

and in the `if s.auth != nil && s.authz != nil` branch, next to the seal route:

```go
r.With(RequireAuth(s.auth), s.requireInstance(authz.SysBackup, "sys.backup", "")).Get("/backup", s.handleBackup)
```

with the mirror in the `else` branch (unit-test seam, same as seal):

```go
r.Get("/backup", s.handleBackup)
```

- [ ] **Step 6: Run the e2e tests**

Run: `go test ./internal/api/ -run 'TestBackup|TestRestore' -v` (needs Docker)
Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add internal/authz/actions.go internal/api/backup_handlers.go internal/api/backup_e2e_test.go internal/api/errors.go internal/api/server.go internal/api/boot.go cmd/janus/server.go
git commit -m "feat(api): GET /v1/sys/backup (admin, audited, streamed) + POST /v1/sys/restore (empty-instance bootstrap)"
```

---

### Task 8: CLI — `janus backup` / `janus restore`

**Files:**
- Modify: `cmd/janus/apiclient.go` (add `stream`)
- Create: `cmd/janus/backup_restore.go`
- Modify: `cmd/janus/main.go:25-38` (register both commands)
- Create: `cmd/janus/backup_restore_test.go`

- [ ] **Step 1: Write the failing tests**

Create `cmd/janus/backup_restore_test.go` (same mux pattern as the seal tests):

```go
package main

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBackupStreamsToStdoutWithAuth(t *testing.T) {
	t.Setenv("JANUS_CONFIG_DIR", t.TempDir())
	t.Setenv("JANUS_TOKEN", "")
	const dump = "{\"janus_backup\":1}\n{\"table\":\"projects\",\"row\":{}}\n"
	var gotAuth string
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/sys/backup", func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/x-ndjson")
		fmt.Fprint(w, dump)
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	var out bytes.Buffer
	cmd := newBackupCmd()
	cmd.SetOut(&out)
	cmd.SetErr(io.Discard)
	cmd.SetArgs([]string{"--address", ts.URL, "--token", "janus_svc_abc"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if gotAuth != "Bearer janus_svc_abc" {
		t.Fatalf("auth = %q", gotAuth)
	}
	if out.String() != dump {
		t.Fatalf("stdout = %q", out.String())
	}
}

func TestBackupWritesFile0600(t *testing.T) {
	t.Setenv("JANUS_CONFIG_DIR", t.TempDir())
	t.Setenv("JANUS_TOKEN", "")
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/sys/backup", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "{\"janus_backup\":1}\n")
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	out := filepath.Join(t.TempDir(), "b.jsonl")
	cmd := newBackupCmd()
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	cmd.SetArgs([]string{"--address", ts.URL, "--token", "tk", "--out", out})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(out)
	if err != nil || !strings.Contains(string(b), "janus_backup") {
		t.Fatalf("file: %v %q", err, b)
	}
	// Permission check is meaningful on POSIX only; on Windows Go approximates.
	if fi, _ := os.Stat(out); fi != nil && fi.Mode().Perm()&0o077 != 0 && os.PathSeparator == '/' {
		t.Fatalf("perms too open: %v", fi.Mode())
	}
}

func TestRestoreSendsBodyAndPrintsUnsealHint(t *testing.T) {
	t.Setenv("JANUS_CONFIG_DIR", t.TempDir())
	var gotBody string
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/sys/restore", func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"restored":true,"sealed":true}`)
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	var out bytes.Buffer
	cmd := newRestoreCmd()
	cmd.SetOut(&out)
	cmd.SetErr(io.Discard)
	cmd.SetIn(strings.NewReader("{\"janus_backup\":1}\n"))
	cmd.SetArgs([]string{"--address", ts.URL})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(gotBody, "janus_backup") {
		t.Fatalf("body = %q", gotBody)
	}
	if !strings.Contains(out.String(), "unseal") {
		t.Fatalf("output missing unseal hint: %q", out.String())
	}
}
```

Run: `go test ./cmd/janus/ -run 'TestBackup|TestRestore' -v`
Expected: compile FAIL — commands undefined.

- [ ] **Step 2: Implement**

Add to `cmd/janus/apiclient.go`:

```go
// stream issues an authenticated request and returns the raw body on 2xx.
// It uses a client WITHOUT a total timeout: http.Client.Timeout covers the
// whole body read, which a large backup can legitimately exceed.
func (c *apiClient) stream(method, path string) (io.ReadCloser, error) {
	req, err := http.NewRequest(method, c.address+path, nil)
	if err != nil {
		return nil, err
	}
	switch {
	case c.cred.Bearer != "":
		req.Header.Set("Authorization", "Bearer "+c.cred.Bearer)
	case c.cred.Cookie != "":
		// #nosec G124 -- outgoing client request cookie; server-side cookie flags do not apply.
		req.AddCookie(&http.Cookie{Name: "janus_session", Value: c.cred.Cookie})
	}
	resp, err := (&http.Client{}).Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 300 {
		defer resp.Body.Close()
		return nil, rewriteAPIError(decodeAPIError(resp))
	}
	return resp.Body, nil
}
```

Create `cmd/janus/backup_restore.go`:

```go
package main

import (
	"fmt"
	"io"
	"net/http"
	"os"

	"github.com/spf13/cobra"
)

func newBackupCmd() *cobra.Command {
	var address, token, out string
	cmd := &cobra.Command{
		Use:   "backup",
		Short: "Stream a full-instance backup (JSONL) to stdout or --out",
		Long: "Streams GET /v1/sys/backup: a key-preserving logical dump. The file\n" +
			"contains only wrapped keys and ciphertext — it is useless without the\n" +
			"original unseal shares/KMS key, and safe to store like any backup.",
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, err := newAPIClient(address, token)
			if err != nil {
				return err
			}
			body, err := c.stream("GET", "/v1/sys/backup")
			if err != nil {
				return err
			}
			defer body.Close()
			var w io.Writer = cmd.OutOrStdout()
			if out != "" {
				f, err := os.OpenFile(out, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600) // #nosec G304 -- operator-chosen output path
				if err != nil {
					return err
				}
				defer f.Close()
				w = f
			}
			n, err := io.Copy(w, body)
			if err != nil {
				return fmt.Errorf("backup stream interrupted after %d bytes: %w", n, err)
			}
			fmt.Fprintf(cmd.ErrOrStderr(), "backup complete (%d bytes)\n", n)
			return nil
		},
	}
	cmd.Flags().StringVar(&address, "address", "", "server address (default: stored login address)")
	cmd.Flags().StringVar(&token, "token", "", "service token (overrides stored session)")
	cmd.Flags().StringVar(&out, "out", "", "write to file instead of stdout (created 0600)")
	return cmd
}

func newRestoreCmd() *cobra.Command {
	var address string
	cmd := &cobra.Command{
		Use:   "restore [file]",
		Args:  cobra.MaximumNArgs(1),
		Short: "Restore a backup into an EMPTY instance (reads stdin without a file arg)",
		Long: "POSTs the dump to /v1/sys/restore. Only valid against a freshly\n" +
			"migrated, uninitialized instance. Afterwards the instance is sealed:\n" +
			"unseal with the ORIGINAL shares or KMS key of the backed-up instance.",
		RunE: func(cmd *cobra.Command, args []string) error {
			var in io.Reader = cmd.InOrStdin()
			if len(args) == 1 {
				f, err := os.Open(args[0]) // #nosec G304 -- operator-supplied backup path
				if err != nil {
					return err
				}
				defer f.Close()
				in = f
			}
			req, err := http.NewRequest("POST", resolveAddress(address)+"/v1/sys/restore", in)
			if err != nil {
				return err
			}
			req.Header.Set("Content-Type", "application/x-ndjson")
			// No total timeout: large restores stream for a while.
			resp, err := (&http.Client{}).Do(req)
			if err != nil {
				return err
			}
			defer resp.Body.Close()
			if resp.StatusCode >= 300 {
				return rewriteAPIError(decodeAPIError(resp))
			}
			cmd.Println("restored — the instance is sealed; unseal with the ORIGINAL shares (janus unseal)")
			return nil
		},
	}
	cmd.Flags().StringVar(&address, "address", "", "server address")
	return cmd
}
```

Register in `cmd/janus/main.go` `root.AddCommand(...)`, after `newSealCmd()`:

```go
newBackupCmd(),
newRestoreCmd(),
```

- [ ] **Step 3: Run tests**

Run: `go test ./cmd/janus/ -v`
Expected: PASS (new tests + no regressions; the CLI leak test `cli_leak_test.go` must stay green).

- [ ] **Step 4: Commit**

```bash
git add cmd/janus/apiclient.go cmd/janus/backup_restore.go cmd/janus/backup_restore_test.go cmd/janus/main.go
git commit -m "feat(cli): janus backup / janus restore (streamed, key-preserving)"
```

---

### Task 9: Round-trip DR-drill e2e + failure modes

**Files:**
- Modify: `internal/api/backup_e2e_test.go`

- [ ] **Step 1: Write the round-trip test**

Append to `internal/api/backup_e2e_test.go`. This is the money test: two real Postgres containers, backup A → restore into B → unseal B with A's share → same login works → same plaintext comes back → audit chain verifies across the boundary.

```go
// drillStack boots a stack like authStackFull but returns the unseal share too.
func drillStack(t *testing.T) (tsURL string, srv *Server, share, email, password, cid string) {
	t.Helper()
	dsn := bootPostgres(t)
	ctx := context.Background()
	s, st, err := Boot(ctx, BootConfig{DatabaseURL: dsn, SealType: "shamir"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(st.Close)
	hts := httptest.NewServer(s.Handler())
	t.Cleanup(hts.Close)
	var ir struct {
		Shares []string `json:"shares"`
		Admin  *struct{ Email, Password string } `json:"admin"`
	}
	if code := doJSON(t, "POST", hts.URL+"/v1/sys/init",
		`{"shares":1,"threshold":1,"admin_email":"dr@corp.io"}`, &ir); code != 200 {
		t.Fatalf("init: %d", code)
	}
	if code := doJSON(t, "POST", hts.URL+"/v1/sys/unseal",
		fmt.Sprintf(`{"share":%q}`, ir.Shares[0]), nil); code != 200 {
		t.Fatal("unseal failed")
	}
	p, err := s.service.CreateProject(ctx, "drproj", "DR Project")
	if err != nil {
		t.Fatal(err)
	}
	e, err := s.service.CreateEnvironment(ctx, p.ID, "prod", "Prod")
	if err != nil {
		t.Fatal(err)
	}
	c, err := s.service.CreateConfig(ctx, e.ID, "root", nil)
	if err != nil {
		t.Fatal(err)
	}
	return hts.URL, s, ir.Shares[0], ir.Admin.Email, ir.Admin.Password, c.ID
}

func TestBackupRestoreRoundTrip(t *testing.T) {
	// --- Instance A: populate, back up. ---
	aURL, aSrv, share, email, password, cid := drillStack(t)
	cookie := login(t, aURL, email, password)
	if _, err := aSrv.service.SetSecrets(context.Background(), cid, []secrets.SecretChange{
		{Key: "DB_URL", Value: []byte("postgres://dr-secret")},
		{Key: "API_KEY", Value: []byte("sk-dr-42")},
	}, "seed", "test"); err != nil {
		t.Fatal(err)
	}
	// Seed a transit key and encrypt a value on A (shapes mirror
	// transit_dataplane_e2e_test.go — verify there if this 4xxes).
	const transitPT = "ZHItdHJhbnNpdC1wbGFpbnRleHQ=" // base64("dr-transit-plaintext")
	if code := doAuthed(t, "POST", aURL+"/v1/transit/keys",
		cookie, "", `{"name":"drkey","type":"aes256-gcm"}`, nil); code != 200 && code != 201 {
		t.Fatalf("transit key create: %d", code)
	}
	var enc struct {
		Ciphertext string `json:"ciphertext"`
	}
	if code := doAuthed(t, "POST", aURL+"/v1/transit/encrypt/drkey",
		cookie, "", `{"plaintext":"`+transitPT+`"}`, &enc); code != 200 || enc.Ciphertext == "" {
		t.Fatalf("transit encrypt: %d %+v", code, enc)
	}
	transitCT := enc.Ciphertext

	code, dump := backupRaw(t, aURL, cookie)
	if code != 200 {
		t.Fatalf("backup: %d", code)
	}

	// --- Instance B: fresh DB, restore, unseal with A's share. ---
	bDSN := bootPostgres(t)
	ctx := context.Background()
	bSrv, bSt, err := Boot(ctx, BootConfig{DatabaseURL: bDSN, SealType: "shamir"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(bSt.Close)
	bTS := httptest.NewServer(bSrv.Handler())
	t.Cleanup(bTS.Close)

	req, err := http.NewRequest("POST", bTS.URL+"/v1/sys/restore", strings.NewReader(dump))
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("restore: %d", resp.StatusCode)
	}
	if code := doJSON(t, "POST", bTS.URL+"/v1/sys/unseal",
		fmt.Sprintf(`{"share":%q}`, share), nil); code != 200 {
		t.Fatal("unseal of restored instance with ORIGINAL share failed")
	}

	// Same credentials work; same plaintext comes back.
	bCookie := login(t, bTS.URL, email, password)
	var all struct {
		Secrets map[string]string `json:"secrets"`
	}
	if code := doAuthed(t, "GET", bTS.URL+"/v1/configs/"+cid+"/secrets?reveal=true",
		bCookie, "", "", &all); code != 200 {
		t.Fatalf("reveal on restored instance: %d", code)
	}
	if all.Secrets["DB_URL"] != "postgres://dr-secret" || all.Secrets["API_KEY"] != "sk-dr-42" {
		t.Fatalf("restored secrets differ: %+v", all.Secrets)
	}

	// Audit chain verifies ACROSS the restore boundary (sys.restore appended).
	var vr struct {
		Valid bool `json:"valid"`
	}
	if code := doAuthed(t, "GET", bTS.URL+"/v1/audit/verify", bCookie, "", "", &vr); code != 200 || !vr.Valid {
		t.Fatalf("audit verify after restore: code=%d valid=%v", code, vr.Valid)
	}

	// Transit survives too: ciphertext produced on A decrypts on B (the
	// restored wrapped_material unwraps under the same master key).
	var dec struct {
		Plaintext string `json:"plaintext"`
	}
	if code := doAuthed(t, "POST", bTS.URL+"/v1/transit/decrypt/drkey",
		bCookie, "", `{"ciphertext":"`+transitCT+`"}`, &dec); code != 200 || dec.Plaintext != transitPT {
		t.Fatalf("transit decrypt after restore: code=%d plaintext=%q", code, dec.Plaintext)
	}
}

func TestRestoreTruncatedStreamRollsBack(t *testing.T) {
	aURL, aSrv, _, email, password, cid := drillStack(t)
	cookie := login(t, aURL, email, password)
	if _, err := aSrv.service.SetSecrets(context.Background(), cid, []secrets.SecretChange{
		{Key: "K", Value: []byte("v")},
	}, "seed", "test"); err != nil {
		t.Fatal(err)
	}
	code, dump := backupRaw(t, aURL, cookie)
	if code != 200 {
		t.Fatalf("backup: %d", code)
	}
	truncated := dump[:len(dump)*2/3] // chop mid-stream

	bDSN := bootPostgres(t)
	bSrv, bSt, err := Boot(context.Background(), BootConfig{DatabaseURL: bDSN, SealType: "shamir"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(bSt.Close)
	bTS := httptest.NewServer(bSrv.Handler())
	t.Cleanup(bTS.Close)

	req, _ := http.NewRequest("POST", bTS.URL+"/v1/sys/restore", strings.NewReader(truncated))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 422 {
		t.Fatalf("truncated restore = %d, want 422", resp.StatusCode)
	}
	// Instance stayed empty → a full, valid restore still succeeds afterwards.
	req2, _ := http.NewRequest("POST", bTS.URL+"/v1/sys/restore", strings.NewReader(dump))
	resp2, err := http.DefaultClient.Do(req2)
	if err != nil {
		t.Fatal(err)
	}
	resp2.Body.Close()
	if resp2.StatusCode != 200 {
		t.Fatalf("restore after rollback = %d, want 200", resp2.StatusCode)
	}
}

func TestRestoreSchemaVersionMismatch422(t *testing.T) {
	bDSN := bootPostgres(t)
	bSrv, bSt, err := Boot(context.Background(), BootConfig{DatabaseURL: bDSN, SealType: "shamir"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(bSt.Close)
	bTS := httptest.NewServer(bSrv.Handler())
	t.Cleanup(bTS.Close)
	var env errEnvelope
	if code := doJSON(t, "POST", bTS.URL+"/v1/sys/restore",
		`{"janus_backup":1,"migration_version":99999}`, &env); code != 422 || env.Error.Code != CodeValidation {
		t.Fatalf("mismatch = %d %+v (want 422 validation)", code, env)
	}
}
```

Notes: `"shamir"` string literals — use `crypto.SealTypeShamir` and import `internal/crypto` to match `authStackFull`; this file also needs `fmt` and `net/http/httptest` added to the imports from Task 7. Check the audit-verify response field names against `internal/api/audit_handlers.go` (`valid` assumed — adjust to the real wire shape). The truncation point (`2/3`) must land after the header line; the dumps here are hundreds of lines, so it always does.

- [ ] **Step 2: Run**

Run: `go test ./internal/api/ -run 'TestBackupRestore|TestRestoreTruncated|TestRestoreSchema' -v` (needs Docker; spins 4+ containers, allow a few minutes)
Expected: PASS.

- [ ] **Step 3: Commit**

```bash
git add internal/api/backup_e2e_test.go
git commit -m "test(api): DR-drill round trip, truncated-stream rollback, schema-mismatch e2e"
```

---

### Task 10: Docs, gates, PR 2

**Files:**
- Create: `docs/ops/backup-restore.md`
- Modify: `status.md`

- [ ] **Step 1: Write the DR runbook**

Create `docs/ops/backup-restore.md`:

```markdown
# Backup & restore (disaster recovery)

Janus backups are **key-preserving logical dumps**: every row exactly as
stored — wrapped KEKs, wrapped DEKs, ciphertexts, password hashes, token
HMACs. A backup file contains **no plaintext secrets** and is useless without
the original unseal material. Corollary: **your unseal shares (or KMS key)
are part of your DR plan** — a backup cannot be opened without them.

## Taking a backup

    janus backup --out janus-backup.jsonl          # stored session
    janus backup --token $JANUS_TOKEN > b.jsonl    # service token / CI

Requires an admin (`sys:backup`). Each backup writes a `sys.backup` audit
event. Cron it and ship the file offsite; it is safe at rest like any
ciphertext, but treat it as sensitive metadata (names, paths, actors).

## Restoring

1. Fresh Postgres + the **same janus version** that wrote the backup
   (restore checks the schema version and refuses a mismatch).
2. Start the server (it auto-migrates). Do **not** run `janus init`.
3. `janus restore janus-backup.jsonl`
4. `janus unseal` with the ORIGINAL shares (or start with the same KMS key).
5. Verify: `janus seal-status`, `GET /v1/audit/verify` (chain includes a
   `sys.restore` event), spot-read a secret.

Restore only works on an empty instance (no seal config, users, or
projects) — it will never overwrite live data. A failed restore rolls back
completely; the instance stays empty and restorable.

Sessions are not backed up: everyone logs in again after a restore.
```

- [ ] **Step 2: Update `status.md`** — record item 4 (backup/restore) as done, per the file's format.

- [ ] **Step 3: Full gates**

```bash
go build ./... && go vet ./...
go test ./...
gosec -exclude-dir=internal/crypto/shamir ./...
govulncheck ./...
```
Expected: clean. gosec will flag the `fmt.Sprintf` SQL in `internal/store/backup.go` (G201) — the inline `// #nosec G201` justifications are already in place; confirm they suppress with reasons intact, and that no OTHER new findings appear.

- [ ] **Step 4: Push and open PR 2**

```bash
git push -u origin ops-backup-restore
gh pr create --title "Ops hardening 2/2: key-preserving instance backup & restore" --body "<summary per spec §4: JSONL dump, empty-instance restore, chain-continuous sys.restore, DR runbook. Round-trip DR drill in CI.>

🤖 Generated with [Claude Code](https://claude.com/claude-code)"
```
(If PR 1 has merged by now, rebase onto `origin/main` first so PR 2's diff is just item 4.)
```
