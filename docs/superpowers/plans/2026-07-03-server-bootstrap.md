# Server Bootstrap Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make Janus a runnable server: `janus server` boots against Postgres, auto-migrates, serves a `/v1/sys/*` HTTP API for init/unseal (Shamir + AWS KMS), returns 503 on non-sys routes while sealed, and ships `janus init/unseal/seal-status/seal` CLI wrappers plus a 1-of-1 dev-unseal workflow.

**Architecture:** New `internal/api` package owns the chi router, sys handlers, `RequireUnsealed` middleware, the project-wide JSON error envelope, and a `Boot` function that composes store + crypto + secrets into a `Server`. `cmd/janus` is restructured onto cobra; `init/unseal/seal-status/seal` are thin HTTP clients. The `Keyring` is the single source of truth for sealed-ness. Two small `internal/crypto` additions: `ShamirUnsealer.SubmittedShares()` and 1-of-1 seal support.

**Tech Stack:** Go 1.26.4, chi v5, cobra, `golang.org/x/term`, `log/slog`, testcontainers (existing), fake KMS client for tests.

**Spec:** `docs/superpowers/specs/2026-07-03-server-bootstrap-design.md`

---

## Reference: existing APIs this plan builds on

- `crypto.NewKeyring() *Keyring`; `(*Keyring) Unseal(master []byte) error` (copies; returns `ErrAlreadyUnsealed` if unsealed, `ErrInvalidKeySize`), `Seal()`, `Sealed() bool`.
- `crypto.Unsealer` interface: `Init(ctx) (*InitResult, error)`, `Unseal(ctx) ([]byte, error)`. `InitResult{Shares [][]byte}`.
- `crypto.NewShamirUnsealer(store SealConfigStore, shares, threshold int) *ShamirUnsealer` (0,0 → 3-of-5 default); `SubmitShare(ctx, share []byte) (Progress, error)` where `Progress{Submitted, Required int}`; `Unseal(ctx)`; `Reset()`.
- `crypto.NewKMSUnsealer(store SealConfigStore, client KMSClient) *KMSUnsealer`; `crypto.KMSClient` interface `{Encrypt(ctx, pt) ([]byte, error); Decrypt(ctx, ct) ([]byte, error)}`; `crypto.NewAWSKMSClient(api AWSKMSAPI, keyID string) *AWSKMSClient`.
- `crypto.SealConfigStore` interface `{Get(ctx) (*SealConfig, error); Put(ctx, *SealConfig) error}`; `SealConfig{Type string; Threshold, Shares int; KeyCheckValue, WrappedMasterKey []byte}`; constants `SealTypeShamir`/`SealTypeAWSKMS`; sentinel `ErrNoSealConfig`.
- Crypto sentinels: `ErrSealed`, `ErrAlreadyUnsealed`, `ErrAlreadyInitialized`, `ErrInvalidShare`, `ErrDuplicateShare`, `ErrNotEnoughShares`, `ErrKeyCheckFailed`, `ErrInvalidSealConfig`, `ErrNoSealConfig`. `crypto.KeySize == 32`. `crypto.GenerateKey() ([]byte, error)`.
- `store.Open(ctx, dsn) (*Store, error)`, `(*Store) Migrate(ctx) error`, `Close()`; `store.NewSealConfigStore(s *Store) *PostgresSealConfigStore` (implements `crypto.SealConfigStore`).
- `secrets.NewService(st *store.Store, kr *crypto.Keyring) *Service`.
- Existing `cmd/janus/main.go` is a hand-rolled arg switch (`version`, `migrate`) with `runMigrate()` reading `JANUS_DATABASE_URL` — its logic is preserved, re-homed onto cobra.
- Vendored shamir: `shamir.Split` requires `threshold >= 2`; `shamir.Combine` requires ≥ 2 parts. Hence Task 2's 1-of-1 special case.
- Module path: `github.com/steveokay/janus-secrets`.

---

## Task 1: Dependencies + JSON error envelope

**Files:**
- Modify: `go.mod` (via `go get`)
- Create: `internal/api/errors.go`
- Test: `internal/api/errors_test.go`

- [ ] **Step 1: Add dependencies**

```bash
go get github.com/go-chi/chi/v5@latest github.com/spf13/cobra@latest golang.org/x/term@latest github.com/aws/aws-sdk-go-v2/config@latest
go mod tidy
```

Run: `go build ./...` — Expected: PASS (deps resolve; nothing imports them yet).

- [ ] **Step 2: Write the failing test**

Create `internal/api/errors_test.go`:

```go
package api

import (
	"encoding/json"
	"net/http/httptest"
	"testing"
)

func TestWriteError(t *testing.T) {
	rec := httptest.NewRecorder()
	writeError(rec, 409, CodeAlreadyInitialized, "seal is already initialized")

	if rec.Code != 409 {
		t.Fatalf("status = %d, want 409", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Fatalf("content-type = %q", ct)
	}
	var body struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Error.Code != "already_initialized" || body.Error.Message == "" {
		t.Fatalf("body = %+v", body)
	}
}

func TestWriteJSON(t *testing.T) {
	rec := httptest.NewRecorder()
	writeJSON(rec, 200, map[string]bool{"ok": true})
	if rec.Code != 200 || rec.Header().Get("Content-Type") != "application/json" {
		t.Fatalf("status=%d ct=%q", rec.Code, rec.Header().Get("Content-Type"))
	}
}
```

- [ ] **Step 3: Run it to verify it fails**

Run: `go test ./internal/api/ -run TestWrite`
Expected: FAIL — `undefined: writeError` (package doesn't exist yet).

- [ ] **Step 4: Implement**

Create `internal/api/errors.go`:

```go
// Package api is Janus's HTTP surface: the chi router, the /v1/sys/* seal
// lifecycle endpoints, the RequireUnsealed middleware, and the project-wide
// JSON error envelope. Handlers are thin translation layers; all seal logic
// lives in internal/crypto.
package api

import (
	"encoding/json"
	"net/http"
)

// Error codes used in the {"error":{"code","message"}} envelope. These are the
// project-wide vocabulary; later milestones add to it.
const (
	CodeSealed             = "sealed"
	CodeNotInitialized     = "not_initialized"
	CodeAlreadyInitialized = "already_initialized"
	CodeInvalidShare       = "invalid_share"
	CodeDuplicateShare     = "duplicate_share"
	CodeNotEnoughShares    = "not_enough_shares"
	CodeKeyCheckFailed     = "key_check_failed"
	CodeValidation         = "validation"
	CodeInternal           = "internal"
)

type errorBody struct {
	Error errorDetail `json:"error"`
}

type errorDetail struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// writeJSON writes v as a JSON response with the given status.
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// writeError writes the project error envelope. message must never contain
// internals, key material, or secret values.
func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, errorBody{Error: errorDetail{Code: code, Message: message}})
}
```

- [ ] **Step 5: Run tests to verify they pass**

Run: `go test ./internal/api/ -count=1` — Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add go.mod go.sum internal/api/errors.go internal/api/errors_test.go
git commit -m "feat(api): add chi/cobra/term deps and project-wide JSON error envelope"
```

---

## Task 2: crypto additions — `Progress()` accessor + 1-of-1 seal support

The sys API needs a read-only submitted-share count, and the dev workflow needs a 1-of-1 seal, which the vendored shamir library rejects (`threshold >= 2`, `Combine` needs ≥2 parts). Special-case it the way Vault does: with one share, the share IS the master key; the KCV check still rejects a wrong share. `internal/crypto` has a 100% branch-coverage bar — cover every new branch.

**Files:**
- Modify: `internal/crypto/shamir.go` (Init single-share branch, Unseal threshold-1 branch, new `Progress()`)
- Test: `internal/crypto/shamir_unsealer_test.go` (append tests)

- [ ] **Step 1: Write the failing tests**

Append to `internal/crypto/shamir_unsealer_test.go`:

```go
func TestShamirProgressAccessor(t *testing.T) {
	ctx := context.Background()
	store := fileStore(t)
	u := NewShamirUnsealer(store, 0, 0)
	res, err := u.Init(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got := u.SubmittedShares(); got != 0 {
		t.Fatalf("Progress before submit = %d, want 0", got)
	}
	if _, err := u.SubmitShare(ctx, res.Shares[0]); err != nil {
		t.Fatal(err)
	}
	if got := u.SubmittedShares(); got != 1 {
		t.Fatalf("Progress after one submit = %d, want 1", got)
	}
}

func TestShamirOneOfOne(t *testing.T) {
	ctx := context.Background()
	store := fileStore(t)
	u := NewShamirUnsealer(store, 1, 1)

	res, err := u.Init(ctx)
	if err != nil {
		t.Fatalf("1-of-1 Init: %v", err)
	}
	if len(res.Shares) != 1 || len(res.Shares[0]) != KeySize {
		t.Fatalf("1-of-1 shares: n=%d len=%d, want 1 share of KeySize", len(res.Shares), len(res.Shares[0]))
	}

	// The single share unseals (and KCV verifies it).
	share := append([]byte(nil), res.Shares[0]...)
	if _, err := u.SubmitShare(ctx, share); err != nil {
		t.Fatal(err)
	}
	if got := u.SubmittedShares(); got != 1 {
		t.Fatalf("Progress after submit = %d, want 1", got)
	}
	master, err := u.Unseal(ctx)
	if err != nil {
		t.Fatalf("1-of-1 Unseal: %v", err)
	}
	if len(master) != KeySize {
		t.Fatalf("master len = %d", len(master))
	}
	zero(master)

	// A wrong single share fails the KCV, not silently succeeds.
	u2 := NewShamirUnsealer(store, 1, 1)
	wrong := testKey(0xEE)
	if _, err := u2.SubmitShare(ctx, wrong); err != nil {
		t.Fatal(err)
	}
	if _, err := u2.Unseal(ctx); !errors.Is(err, ErrKeyCheckFailed) {
		t.Fatalf("wrong 1-of-1 share: got %v, want ErrKeyCheckFailed", err)
	}
}
```

Note: `fileStore(t)` (temp-dir `FileSealConfigStore`) and `testKey(b byte)` are existing helpers in `internal/crypto/shamir_unsealer_test.go` / `testhelpers_test.go` — reuse them, do not create duplicates. The second sub-test in `TestShamirOneOfOne` reuses the same `store` deliberately: the seal is already initialized, so the fresh unsealer instance exercises the unseal path only.

- [ ] **Step 2: Run to verify failure**

Run: `go test ./internal/crypto/ -run 'TestShamirProgressAccessor|TestShamirOneOfOne'`
Expected: FAIL — `u.Progress undefined`, and 1-of-1 `Init` errors with "threshold must be at least 2".

- [ ] **Step 3: Implement**

In `internal/crypto/shamir.go`:

(a) In `Init`, replace the `parts, err := shamir.Split(...)` call with:

```go
	var parts [][]byte
	if s.shares == 1 && s.threshold == 1 {
		// Single-share seal (dev/simple deployments): the vendored shamir
		// library requires threshold >= 2, so — like Vault — the one share is
		// the master key itself. The KCV still rejects a wrong share at unseal.
		parts = [][]byte{append([]byte(nil), master...)}
	} else {
		parts, err = shamir.Split(master, s.shares, s.threshold)
		if err != nil {
			return nil, err
		}
	}
```

(b) In `Unseal`, replace the `master, err := shamir.Combine(parts)` call with:

```go
	var master []byte
	if cfg.Threshold == 1 {
		// Single-share seal: the share is the master-key candidate directly
		// (Combine requires >= 2 parts). KCV below verifies it.
		master = append([]byte(nil), parts[0]...)
	} else {
		var cErr error
		master, cErr = shamir.Combine(parts)
		if cErr != nil {
			return nil, ErrInvalidShare
		}
	}
```

(c) Add the accessor:

```go
// Progress reports how many shares have been submitted so far. Read-only
// companion to SubmitShare's return value, for status endpoints.
func (s *ShamirUnsealer) SubmittedShares() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.submitted)
}
```

- [ ] **Step 4: Run tests + coverage**

Run: `go test ./internal/crypto/ -count=1 && go test -coverprofile=crypto.out ./internal/crypto && go tool cover -func=crypto.out | tail -1`
Expected: PASS, total coverage 100.0% (add a case if a new branch is unhit).

- [ ] **Step 5: Commit**

```bash
git add internal/crypto/shamir.go internal/crypto/shamir_unsealer_test.go
git commit -m "feat(crypto): ShamirUnsealer Progress() accessor and 1-of-1 seal support"
```

---

## Task 3: Middleware — `RequireUnsealed` + request logger

**Files:**
- Create: `internal/api/middleware.go`
- Test: `internal/api/middleware_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/api/middleware_test.go`:

```go
package api

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/steveokay/janus-secrets/internal/crypto"
)

func unsealedKeyring(t *testing.T) *crypto.Keyring {
	t.Helper()
	kr := crypto.NewKeyring()
	master, err := crypto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	if err := kr.Unseal(master); err != nil {
		t.Fatal(err)
	}
	return kr
}

func TestRequireUnsealed(t *testing.T) {
	kr := crypto.NewKeyring() // sealed
	probe := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	h := RequireUnsealed(kr)(probe)

	// Non-sys route while sealed → 503 with the sealed envelope.
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/v1/projects", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("sealed non-sys: status %d, want 503", rec.Code)
	}
	var body struct {
		Error struct{ Code string } `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil || body.Error.Code != "sealed" {
		t.Fatalf("sealed body: %s (err %v)", rec.Body.String(), err)
	}

	// Sys routes are exempt even while sealed.
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/v1/sys/seal-status", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("sealed sys: status %d, want 200", rec.Code)
	}

	// Unsealed keyring → non-sys route passes.
	h2 := RequireUnsealed(unsealedKeyring(t))(probe)
	rec = httptest.NewRecorder()
	h2.ServeHTTP(rec, httptest.NewRequest("GET", "/v1/projects", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("unsealed non-sys: status %d, want 200", rec.Code)
	}
}

func TestRequestLoggerNeverLogsBodies(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))
	h := requestLogger(logger)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTeapot)
	}))
	const canary = "deadbeefcafe0123-share-canary"
	req := httptest.NewRequest("POST", "/v1/sys/unseal", strings.NewReader(`{"share":"`+canary+`"}`))
	h.ServeHTTP(httptest.NewRecorder(), req)

	out := buf.String()
	if !strings.Contains(out, "/v1/sys/unseal") || !strings.Contains(out, "418") {
		t.Fatalf("log missing method/path/status: %q", out)
	}
	if strings.Contains(out, canary) {
		t.Fatalf("request body leaked into log: %q", out)
	}
}
```

- [ ] **Step 2: Run to verify failure**

Run: `go test ./internal/api/ -run 'TestRequireUnsealed|TestRequestLogger'`
Expected: FAIL — `undefined: RequireUnsealed`.

- [ ] **Step 3: Implement**

Create `internal/api/middleware.go`:

```go
package api

import (
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/steveokay/janus-secrets/internal/crypto"
)

// RequireUnsealed returns 503 {"error":{"code":"sealed"}} for every route
// except /v1/sys/* while the keyring is sealed. Sys routes stay reachable so
// the operator can initialize and unseal.
func RequireUnsealed(kr *crypto.Keyring) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if strings.HasPrefix(r.URL.Path, "/v1/sys/") {
				next.ServeHTTP(w, r)
				return
			}
			if kr.Sealed() {
				writeError(w, http.StatusServiceUnavailable, CodeSealed,
					"server is sealed; unseal via /v1/sys/unseal")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// requestLogger logs method, path, status, and duration ONLY. Request and
// response bodies are never logged: unseal shares and (later) secret values
// transit them.
func requestLogger(logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			sw := &statusWriter{ResponseWriter: w, status: http.StatusOK}
			next.ServeHTTP(sw, r)
			logger.Info("http",
				"method", r.Method,
				"path", r.URL.Path,
				"status", sw.status,
				"dur_ms", time.Since(start).Milliseconds())
		})
	}
}

// statusWriter captures the response status for logging.
type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(code int) {
	w.status = code
	w.ResponseWriter.WriteHeader(code)
}
```

- [ ] **Step 4: Run tests**

Run: `go test ./internal/api/ -count=1` — Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/api/middleware.go internal/api/middleware_test.go
git commit -m "feat(api): RequireUnsealed middleware and body-free request logger"
```

---

## Task 4: Server type, router, health + seal-status, graceful shutdown

Handler tests use an in-memory `crypto.SealConfigStore` — fast, no Docker. (`Boot` tests in Task 6 use real Postgres.) `secrets.Service` may be nil in these tests: no non-sys route dereferences it yet.

**Files:**
- Create: `internal/api/server.go`
- Create: `internal/api/sys.go` (health + seal-status only; more handlers in Task 5)
- Test: `internal/api/harness_test.go`, `internal/api/sys_status_test.go`, `internal/api/server_test.go`

- [ ] **Step 1: Write the test harness**

Create `internal/api/harness_test.go`:

```go
package api

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/steveokay/janus-secrets/internal/crypto"
)

// memSealStore is an in-memory crypto.SealConfigStore for handler tests.
type memSealStore struct {
	mu  sync.Mutex
	cfg *crypto.SealConfig
}

func (m *memSealStore) Get(context.Context) (*crypto.SealConfig, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.cfg == nil {
		return nil, crypto.ErrNoSealConfig
	}
	c := *m.cfg
	return &c, nil
}

func (m *memSealStore) Put(_ context.Context, cfg *crypto.SealConfig) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	c := *cfg
	m.cfg = &c
	return nil
}

// newShamirTestServer returns a Server wired for a Shamir seal over an
// in-memory store, plus its httptest server.
func newShamirTestServer(t *testing.T) (*Server, *httptest.Server, *memSealStore) {
	t.Helper()
	seals := &memSealStore{}
	kr := crypto.NewKeyring()
	u := crypto.NewShamirUnsealer(seals, 0, 0)
	srv := New(Config{SealType: crypto.SealTypeShamir}, kr, u, seals, nil,
		slog.New(slog.NewTextHandler(io.Discard, nil)))
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return srv, ts, seals
}

// doJSON issues a request and decodes the JSON response into out (if non-nil).
func doJSON(t *testing.T, method, url, body string, out any) int {
	t.Helper()
	var r io.Reader
	if body != "" {
		r = strings.NewReader(body)
	}
	req, err := http.NewRequest(method, url, r)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if out != nil {
		if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
			t.Fatalf("decode %s %s: %v", method, url, err)
		}
	}
	return resp.StatusCode
}

// errCode extracts error.code from a raw envelope-decoded map.
type errEnvelope struct {
	Error struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}
```

- [ ] **Step 2: Write the failing status tests**

Create `internal/api/sys_status_test.go`:

```go
package api

import "testing"

func TestHealthAlways200(t *testing.T) {
	_, ts, _ := newShamirTestServer(t)
	var body struct {
		Status      string `json:"status"`
		Initialized bool   `json:"initialized"`
		Sealed      bool   `json:"sealed"`
	}
	if code := doJSON(t, "GET", ts.URL+"/v1/sys/health", "", &body); code != 200 {
		t.Fatalf("health status = %d", code)
	}
	if body.Status != "ok" || body.Initialized || !body.Sealed {
		t.Fatalf("health body = %+v (want ok, uninitialized, sealed)", body)
	}
}

func TestSealStatusUninitialized(t *testing.T) {
	_, ts, _ := newShamirTestServer(t)
	var body struct {
		Initialized bool   `json:"initialized"`
		Sealed      bool   `json:"sealed"`
		Type        string `json:"type"`
	}
	if code := doJSON(t, "GET", ts.URL+"/v1/sys/seal-status", "", &body); code != 200 {
		t.Fatalf("status = %d", code)
	}
	if body.Initialized || !body.Sealed || body.Type != "shamir" {
		t.Fatalf("body = %+v", body)
	}
}
```

- [ ] **Step 3: Run to verify failure**

Run: `go test ./internal/api/ -run 'TestHealth|TestSealStatus'`
Expected: FAIL — `undefined: New` / `undefined: Config`.

- [ ] **Step 4: Implement the server**

Create `internal/api/server.go`:

```go
package api

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/steveokay/janus-secrets/internal/crypto"
	"github.com/steveokay/janus-secrets/internal/secrets"
)

// Config is the api server's static configuration.
type Config struct {
	// ListenAddr defaults to ":8200".
	ListenAddr string
	// SealType is the effective seal type ("shamir" or "awskms"): the stored
	// type when initialized, otherwise the operator-configured one.
	SealType string
}

// Server is Janus's HTTP server. The keyring is the single source of truth
// for sealed-ness; svc is held for future secret routes and may be nil until
// those exist.
type Server struct {
	cfg      Config
	keyring  *crypto.Keyring
	unsealer crypto.Unsealer
	seals    crypto.SealConfigStore
	service  *secrets.Service
	logger   *slog.Logger
	router   chi.Router
}

// New wires the router. logger nil defaults to slog.Default().
func New(cfg Config, kr *crypto.Keyring, u crypto.Unsealer,
	seals crypto.SealConfigStore, svc *secrets.Service, logger *slog.Logger) *Server {
	if cfg.ListenAddr == "" {
		cfg.ListenAddr = ":8200"
	}
	if logger == nil {
		logger = slog.Default()
	}
	s := &Server{cfg: cfg, keyring: kr, unsealer: u, seals: seals, service: svc, logger: logger}

	r := chi.NewRouter()
	r.Use(requestLogger(logger))
	r.Use(RequireUnsealed(kr))
	r.Route("/v1/sys", func(r chi.Router) {
		r.Get("/health", s.handleHealth)
		r.Get("/seal-status", s.handleSealStatus)
		r.Post("/init", s.handleInit)
		r.Post("/unseal", s.handleUnseal)
		r.Post("/unseal/reset", s.handleUnsealReset)
		r.Post("/seal", s.handleSeal)
	})
	s.router = r
	return s
}

// Handler exposes the router (used by tests and ListenAndServe).
func (s *Server) Handler() http.Handler { return s.router }

// ListenAndServe serves until ctx is canceled, then drains for up to 10s.
func (s *Server) ListenAndServe(ctx context.Context) error {
	srv := &http.Server{
		Addr:              s.cfg.ListenAddr,
		Handler:           s.router,
		ReadHeaderTimeout: 10 * time.Second,
	}
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

Create `internal/api/sys.go` (status handlers only; Task 5 adds the rest — include stubs so the router compiles):

```go
package api

import (
	"errors"
	"net/http"

	"github.com/steveokay/janus-secrets/internal/crypto"
)

// zero overwrites b with zeros (best-effort; see internal/crypto).
func zero(b []byte) {
	for i := range b {
		b[i] = 0
	}
}

// sealConfig returns (initialized, cfg). A missing seal config is the
// uninitialized state, not an error.
func (s *Server) sealConfig(r *http.Request) (bool, *crypto.SealConfig, error) {
	cfg, err := s.seals.Get(r.Context())
	if errors.Is(err, crypto.ErrNoSealConfig) {
		return false, nil, nil
	}
	if err != nil {
		return false, nil, err
	}
	return true, cfg, nil
}

type progressBody struct {
	Submitted int `json:"submitted"`
	Required  int `json:"required"`
}

// shamirProgress returns submit progress when the unsealer is Shamir.
func (s *Server) shamirProgress(required int) *progressBody {
	sh, ok := s.unsealer.(*crypto.ShamirUnsealer)
	if !ok {
		return nil
	}
	return &progressBody{Submitted: sh.SubmittedShares(), Required: required}
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	initialized, _, err := s.sealConfig(r)
	if err != nil {
		writeError(w, http.StatusInternalServerError, CodeInternal, "internal error")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"status":      "ok",
		"initialized": initialized,
		"sealed":      s.keyring.Sealed(),
	})
}

type sealStatusResponse struct {
	Initialized bool          `json:"initialized"`
	Sealed      bool          `json:"sealed"`
	Type        string        `json:"type"`
	Threshold   int           `json:"threshold,omitempty"`
	Shares      int           `json:"shares,omitempty"`
	Progress    *progressBody `json:"progress,omitempty"`
}

func (s *Server) handleSealStatus(w http.ResponseWriter, r *http.Request) {
	initialized, cfg, err := s.sealConfig(r)
	if err != nil {
		writeError(w, http.StatusInternalServerError, CodeInternal, "internal error")
		return
	}
	resp := sealStatusResponse{
		Initialized: initialized,
		Sealed:      s.keyring.Sealed(),
		Type:        s.cfg.SealType,
	}
	if initialized {
		resp.Type = cfg.Type
		if cfg.Type == crypto.SealTypeShamir {
			resp.Threshold = cfg.Threshold
			resp.Shares = cfg.Shares
			if resp.Sealed {
				resp.Progress = s.shamirProgress(cfg.Threshold)
			}
		}
	}
	writeJSON(w, http.StatusOK, resp)
}

// Implemented in Task 5.
func (s *Server) handleInit(w http.ResponseWriter, r *http.Request) {
	writeError(w, http.StatusInternalServerError, CodeInternal, "not implemented")
}
func (s *Server) handleUnseal(w http.ResponseWriter, r *http.Request) {
	writeError(w, http.StatusInternalServerError, CodeInternal, "not implemented")
}
func (s *Server) handleUnsealReset(w http.ResponseWriter, r *http.Request) {
	writeError(w, http.StatusInternalServerError, CodeInternal, "not implemented")
}
func (s *Server) handleSeal(w http.ResponseWriter, r *http.Request) {
	writeError(w, http.StatusInternalServerError, CodeInternal, "not implemented")
}
```

- [ ] **Step 5: Write and run the shutdown test**

Create `internal/api/server_test.go`:

```go
package api

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"testing"
	"time"

	"github.com/steveokay/janus-secrets/internal/crypto"
)

func TestGracefulShutdown(t *testing.T) {
	seals := &memSealStore{}
	srv := New(Config{ListenAddr: "127.0.0.1:0", SealType: crypto.SealTypeShamir},
		crypto.NewKeyring(), crypto.NewShamirUnsealer(seals, 0, 0), seals, nil,
		slog.New(slog.NewTextHandler(io.Discard, nil)))

	// ListenAddr :0 picks a free port; we only assert the lifecycle: serve,
	// cancel, clean return.
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- srv.ListenAndServe(ctx) }()
	time.Sleep(100 * time.Millisecond) // let it start listening
	cancel()
	select {
	case err := <-done:
		if err != nil && err != http.ErrServerClosed {
			t.Fatalf("shutdown returned %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("server did not shut down within 5s")
	}
}
```

Run: `go test ./internal/api/ -count=1`
Expected: PASS (status tests + shutdown + Tasks 1/3 tests).

- [ ] **Step 6: Commit**

```bash
git add internal/api/server.go internal/api/sys.go internal/api/harness_test.go internal/api/sys_status_test.go internal/api/server_test.go
git commit -m "feat(api): Server with chi router, health/seal-status, graceful shutdown"
```

---

## Task 5: Init / unseal / reset / seal handlers (Shamir + KMS branches)

**Files:**
- Modify: `internal/api/sys.go` (replace the four stubs)
- Test: `internal/api/sys_lifecycle_test.go`

- [ ] **Step 1: Write the failing lifecycle tests**

Create `internal/api/sys_lifecycle_test.go`:

```go
package api

import (
	"context"
	"encoding/hex"
	"fmt"
	"io"
	"log/slog"
	"net/http/httptest"
	"testing"

	"github.com/steveokay/janus-secrets/internal/crypto"
)

type initResp struct {
	Type   string   `json:"type"`
	Shares []string `json:"shares"`
}

type unsealResp struct {
	Sealed   bool          `json:"sealed"`
	Progress *progressBody `json:"progress"`
}

func TestShamirFullLifecycle(t *testing.T) {
	srv, ts, _ := newShamirTestServer(t)

	// Init 3-of-5.
	var ir initResp
	if code := doJSON(t, "POST", ts.URL+"/v1/sys/init", `{"shares":5,"threshold":3}`, &ir); code != 200 {
		t.Fatalf("init status = %d", code)
	}
	if ir.Type != "shamir" || len(ir.Shares) != 5 {
		t.Fatalf("init resp = %+v", ir)
	}
	for _, sh := range ir.Shares {
		if _, err := hex.DecodeString(sh); err != nil {
			t.Fatalf("share %q not hex: %v", sh, err)
		}
	}

	// Still sealed after init.
	if !srv.keyring.Sealed() {
		t.Fatal("keyring must stay sealed after shamir init")
	}

	// Double init → 409.
	var env errEnvelope
	if code := doJSON(t, "POST", ts.URL+"/v1/sys/init", `{}`, &env); code != 409 || env.Error.Code != CodeAlreadyInitialized {
		t.Fatalf("double init: code=%d env=%+v", code, env)
	}

	// Two shares → still sealed, progress reported.
	var ur unsealResp
	for i := 0; i < 2; i++ {
		body := fmt.Sprintf(`{"share":%q}`, ir.Shares[i])
		if code := doJSON(t, "POST", ts.URL+"/v1/sys/unseal", body, &ur); code != 200 {
			t.Fatalf("unseal %d status = %d", i, code)
		}
	}
	if !ur.Sealed || ur.Progress == nil || ur.Progress.Submitted != 2 || ur.Progress.Required != 3 {
		t.Fatalf("after 2 shares: %+v", ur)
	}

	// Duplicate share → 400 duplicate_share.
	body := fmt.Sprintf(`{"share":%q}`, ir.Shares[0])
	if code := doJSON(t, "POST", ts.URL+"/v1/sys/unseal", body, &env); code != 400 || env.Error.Code != CodeDuplicateShare {
		t.Fatalf("dup share: code=%d env=%+v", code, env)
	}

	// Third share → unsealed.
	body = fmt.Sprintf(`{"share":%q}`, ir.Shares[2])
	if code := doJSON(t, "POST", ts.URL+"/v1/sys/unseal", body, &ur); code != 200 {
		t.Fatalf("third share status = %d", code)
	}
	if ur.Sealed || srv.keyring.Sealed() {
		t.Fatalf("should be unsealed: %+v", ur)
	}

	// Seal again via sys/seal.
	var sr struct {
		Sealed bool `json:"sealed"`
	}
	if code := doJSON(t, "POST", ts.URL+"/v1/sys/seal", "", &sr); code != 200 || !sr.Sealed {
		t.Fatalf("seal: code=%d resp=%+v", code, sr)
	}
	if !srv.keyring.Sealed() {
		t.Fatal("keyring should be sealed after sys/seal")
	}
}

func TestShamirPoisonedSetRecovery(t *testing.T) {
	_, ts, _ := newShamirTestServer(t)
	var ir initResp
	doJSON(t, "POST", ts.URL+"/v1/sys/init", `{"shares":5,"threshold":3}`, &ir)

	// Two good shares + one syntactically-valid wrong share.
	for i := 0; i < 2; i++ {
		doJSON(t, "POST", ts.URL+"/v1/sys/unseal", fmt.Sprintf(`{"share":%q}`, ir.Shares[i]), nil)
	}
	raw, _ := hex.DecodeString(ir.Shares[2])
	raw[0] ^= 0xFF // corrupt
	var env errEnvelope
	code := doJSON(t, "POST", ts.URL+"/v1/sys/unseal",
		fmt.Sprintf(`{"share":%q}`, hex.EncodeToString(raw)), &env)
	if code != 400 || env.Error.Code != CodeKeyCheckFailed {
		t.Fatalf("poisoned set: code=%d env=%+v", code, env)
	}

	// Reset, then clean resubmission succeeds.
	var rr unsealResp
	if code := doJSON(t, "POST", ts.URL+"/v1/sys/unseal/reset", "", &rr); code != 200 || rr.Progress.Submitted != 0 {
		t.Fatalf("reset: code=%d resp=%+v", code, rr)
	}
	var ur unsealResp
	for i := 0; i < 3; i++ {
		doJSON(t, "POST", ts.URL+"/v1/sys/unseal", fmt.Sprintf(`{"share":%q}`, ir.Shares[i]), &ur)
	}
	if ur.Sealed {
		t.Fatalf("after clean resubmission: %+v", ur)
	}
}

func TestUnsealValidation(t *testing.T) {
	_, ts, _ := newShamirTestServer(t)
	var env errEnvelope

	// Unseal before init → 400 not_initialized.
	if code := doJSON(t, "POST", ts.URL+"/v1/sys/unseal", `{"share":"abcd"}`, &env); code != 400 || env.Error.Code != CodeNotInitialized {
		t.Fatalf("pre-init unseal: code=%d env=%+v", code, env)
	}

	doJSON(t, "POST", ts.URL+"/v1/sys/init", `{}`, nil) // 3-of-5 defaults

	// Missing share under shamir → 400 validation.
	if code := doJSON(t, "POST", ts.URL+"/v1/sys/unseal", `{}`, &env); code != 400 || env.Error.Code != CodeValidation {
		t.Fatalf("missing share: code=%d env=%+v", code, env)
	}
	// Non-hex share → 400 invalid_share.
	if code := doJSON(t, "POST", ts.URL+"/v1/sys/unseal", `{"share":"zznothex"}`, &env); code != 400 || env.Error.Code != CodeInvalidShare {
		t.Fatalf("bad hex: code=%d env=%+v", code, env)
	}
}

// fakeKMS mirrors the crypto package's test fake: reversible prefix transform.
type fakeKMS struct{ fail bool }

func (f *fakeKMS) Encrypt(_ context.Context, pt []byte) ([]byte, error) {
	if f.fail {
		return nil, fmt.Errorf("simulated kms outage")
	}
	return append([]byte("wrapped:"), pt...), nil
}

func (f *fakeKMS) Decrypt(_ context.Context, ct []byte) ([]byte, error) {
	if f.fail {
		return nil, fmt.Errorf("simulated kms outage")
	}
	return ct[len("wrapped:"):], nil
}

func newKMSTestServer(t *testing.T, client crypto.KMSClient) (*Server, *httptest.Server) {
	t.Helper()
	seals := &memSealStore{}
	kr := crypto.NewKeyring()
	u := crypto.NewKMSUnsealer(seals, client)
	srv := New(Config{SealType: crypto.SealTypeAWSKMS}, kr, u, seals, nil,
		slog.New(slog.NewTextHandler(io.Discard, nil)))
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return srv, ts
}

func TestKMSInitAutoUnseals(t *testing.T) {
	srv, ts := newKMSTestServer(t, &fakeKMS{})
	var ir initResp
	if code := doJSON(t, "POST", ts.URL+"/v1/sys/init", "", &ir); code != 200 || ir.Type != "awskms" || len(ir.Shares) != 0 {
		t.Fatalf("kms init: code=%d resp=%+v", code, ir)
	}
	if srv.keyring.Sealed() {
		t.Fatal("kms init must auto-unseal")
	}

	// shares/threshold under kms → 400 validation (on a fresh server).
	srv2, ts2 := newKMSTestServer(t, &fakeKMS{})
	_ = srv2
	var env errEnvelope
	if code := doJSON(t, "POST", ts2.URL+"/v1/sys/init", `{"shares":5,"threshold":3}`, &env); code != 400 || env.Error.Code != CodeValidation {
		t.Fatalf("kms init with shares: code=%d env=%+v", code, env)
	}
}

func TestKMSUnsealRetry(t *testing.T) {
	client := &fakeKMS{}
	srv, ts := newKMSTestServer(t, client)
	doJSON(t, "POST", ts.URL+"/v1/sys/init", "", nil)
	srv.keyring.Seal() // simulate a restart's sealed state

	client.fail = true
	var env errEnvelope
	if code := doJSON(t, "POST", ts.URL+"/v1/sys/unseal", "", &env); code != 500 || env.Error.Code != CodeInternal {
		t.Fatalf("kms retry during outage: code=%d env=%+v", code, env)
	}

	client.fail = false
	var ur unsealResp
	if code := doJSON(t, "POST", ts.URL+"/v1/sys/unseal", "", &ur); code != 200 || ur.Sealed {
		t.Fatalf("kms retry after recovery: code=%d resp=%+v", code, ur)
	}
	if srv.keyring.Sealed() {
		t.Fatal("keyring should be unsealed after retry")
	}
}
```

- [ ] **Step 2: Run to verify failure**

Run: `go test ./internal/api/ -run 'TestShamirFull|TestShamirPoisoned|TestUnsealValidation|TestKMS'`
Expected: FAIL — stubs return 500 "not implemented".

- [ ] **Step 3: Implement the handlers**

In `internal/api/sys.go`, replace the four stubs with:

```go
type initRequest struct {
	Shares    int `json:"shares"`
	Threshold int `json:"threshold"`
}

type initResponse struct {
	Type   string   `json:"type"`
	Shares []string `json:"shares,omitempty"`
}

func (s *Server) handleInit(w http.ResponseWriter, r *http.Request) {
	var req initRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil && !errors.Is(err, io.EOF) {
		writeError(w, http.StatusBadRequest, CodeValidation, "invalid JSON body")
		return
	}

	switch s.cfg.SealType {
	case crypto.SealTypeShamir:
		// A short-lived unsealer carries the requested share/threshold params;
		// the long-lived one (0,0) handles submission and unseal afterwards.
		u := crypto.NewShamirUnsealer(s.seals, req.Shares, req.Threshold)
		res, err := u.Init(r.Context())
		if err != nil {
			s.writeInitError(w, err)
			return
		}
		shares := make([]string, len(res.Shares))
		for i, sh := range res.Shares {
			shares[i] = hex.EncodeToString(sh)
			zero(sh) // one-time exposure: the response is the only copy
		}
		writeJSON(w, http.StatusOK, initResponse{Type: crypto.SealTypeShamir, Shares: shares})

	case crypto.SealTypeAWSKMS:
		if req.Shares != 0 || req.Threshold != 0 {
			writeError(w, http.StatusBadRequest, CodeValidation,
				"shares/threshold do not apply to a kms seal")
			return
		}
		if _, err := s.unsealer.Init(r.Context()); err != nil {
			s.writeInitError(w, err)
			return
		}
		// Auto-unseal: the operator holds nothing under KMS.
		if err := s.unsealNow(r.Context()); err != nil {
			writeError(w, http.StatusInternalServerError, CodeInternal, "internal error")
			return
		}
		writeJSON(w, http.StatusOK, initResponse{Type: crypto.SealTypeAWSKMS})

	default:
		writeError(w, http.StatusInternalServerError, CodeInternal, "internal error")
	}
}

func (s *Server) writeInitError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, crypto.ErrAlreadyInitialized):
		writeError(w, http.StatusConflict, CodeAlreadyInitialized, "seal is already initialized")
	default:
		// shamir.Split parameter errors (threshold > shares etc.) land here.
		writeError(w, http.StatusBadRequest, CodeValidation, "invalid seal parameters")
	}
}

// unsealNow runs the unsealer and feeds the keyring, zeroizing the master.
func (s *Server) unsealNow(ctx context.Context) error {
	master, err := s.unsealer.Unseal(ctx)
	if err != nil {
		return err
	}
	defer zero(master)
	if err := s.keyring.Unseal(master); err != nil && !errors.Is(err, crypto.ErrAlreadyUnsealed) {
		return err
	}
	return nil
}

type unsealRequest struct {
	Share string `json:"share"`
}

func (s *Server) handleUnseal(w http.ResponseWriter, r *http.Request) {
	var req unsealRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil && !errors.Is(err, io.EOF) {
		writeError(w, http.StatusBadRequest, CodeValidation, "invalid JSON body")
		return
	}
	initialized, cfg, err := s.sealConfig(r)
	if err != nil {
		writeError(w, http.StatusInternalServerError, CodeInternal, "internal error")
		return
	}
	if !initialized {
		writeError(w, http.StatusBadRequest, CodeNotInitialized, "seal is not initialized")
		return
	}

	switch cfg.Type {
	case crypto.SealTypeAWSKMS:
		if req.Share != "" {
			writeError(w, http.StatusBadRequest, CodeValidation, "kms seal takes no share")
			return
		}
		if !s.keyring.Sealed() {
			writeJSON(w, http.StatusOK, unsealStateBody(s, cfg))
			return
		}
		if err := s.unsealNow(r.Context()); err != nil {
			// KMS outage / IAM failure: generic error, no internals.
			writeError(w, http.StatusInternalServerError, CodeInternal, "unseal failed")
			return
		}
		writeJSON(w, http.StatusOK, unsealStateBody(s, cfg))

	case crypto.SealTypeShamir:
		sh, ok := s.unsealer.(*crypto.ShamirUnsealer)
		if !ok {
			writeError(w, http.StatusInternalServerError, CodeInternal, "internal error")
			return
		}
		if req.Share == "" {
			writeError(w, http.StatusBadRequest, CodeValidation, "share is required")
			return
		}
		raw, err := hex.DecodeString(req.Share)
		if err != nil {
			writeError(w, http.StatusBadRequest, CodeInvalidShare, "share is not valid hex")
			return
		}
		defer zero(raw)
		if !s.keyring.Sealed() {
			writeJSON(w, http.StatusOK, unsealStateBody(s, cfg))
			return
		}
		progress, err := sh.SubmitShare(r.Context(), raw)
		if err != nil {
			switch {
			case errors.Is(err, crypto.ErrDuplicateShare):
				writeError(w, http.StatusBadRequest, CodeDuplicateShare, "share already submitted")
			case errors.Is(err, crypto.ErrInvalidShare):
				writeError(w, http.StatusBadRequest, CodeInvalidShare, "invalid share")
			default:
				writeError(w, http.StatusInternalServerError, CodeInternal, "internal error")
			}
			return
		}
		if progress.Submitted >= progress.Required {
			if err := s.unsealNow(r.Context()); err != nil {
				// Reconstruction or KCV failure: the submitted set is poisoned;
				// the operator resets and resubmits.
				writeError(w, http.StatusBadRequest, CodeKeyCheckFailed,
					"key reconstruction failed; discard submitted shares via /v1/sys/unseal/reset and resubmit")
				return
			}
		}
		writeJSON(w, http.StatusOK, unsealStateBody(s, cfg))

	default:
		writeError(w, http.StatusInternalServerError, CodeInternal, "internal error")
	}
}

// unsealStateBody is the shared unseal/reset response shape.
func unsealStateBody(s *Server, cfg *crypto.SealConfig) map[string]any {
	body := map[string]any{"sealed": s.keyring.Sealed()}
	if cfg.Type == crypto.SealTypeShamir && s.keyring.Sealed() {
		body["progress"] = s.shamirProgress(cfg.Threshold)
	}
	return body
}

func (s *Server) handleUnsealReset(w http.ResponseWriter, r *http.Request) {
	initialized, cfg, err := s.sealConfig(r)
	if err != nil {
		writeError(w, http.StatusInternalServerError, CodeInternal, "internal error")
		return
	}
	if !initialized {
		writeError(w, http.StatusBadRequest, CodeNotInitialized, "seal is not initialized")
		return
	}
	sh, ok := s.unsealer.(*crypto.ShamirUnsealer)
	if !ok {
		writeError(w, http.StatusBadRequest, CodeValidation, "reset applies to shamir seals only")
		return
	}
	sh.Reset()
	writeJSON(w, http.StatusOK, unsealStateBody(s, cfg))
}

func (s *Server) handleSeal(w http.ResponseWriter, r *http.Request) {
	// NOTE: unauthenticated until the auth milestone (availability-only,
	// fail-closed). See the design spec's security posture.
	s.keyring.Seal()
	writeJSON(w, http.StatusOK, map[string]any{"sealed": true})
}
```

Add the imports `encoding/hex`, `encoding/json`, `io`, and `context` to `sys.go`'s import block.

- [ ] **Step 4: Run tests**

Run: `go test ./internal/api/ -count=1` — Expected: PASS (all lifecycle + earlier tests).

- [ ] **Step 5: Commit**

```bash
git add internal/api/sys.go internal/api/sys_lifecycle_test.go
git commit -m "feat(api): init/unseal/reset/seal handlers for shamir and kms seals"
```

---

## Task 6: `Boot` — compose store + crypto + secrets into a Server

**Files:**
- Create: `internal/api/boot.go`
- Test: `internal/api/boot_test.go` (testcontainers — real Postgres)

- [ ] **Step 1: Write the failing tests**

Create `internal/api/boot_test.go`:

```go
package api

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/steveokay/janus-secrets/internal/crypto"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
)

// bootPostgres starts a throwaway Postgres and returns its DSN.
func bootPostgres(t *testing.T) string {
	t.Helper()
	ctx := context.Background()
	ctr, err := tcpostgres.Run(ctx, "postgres:16-alpine",
		tcpostgres.WithDatabase("janus"),
		tcpostgres.WithUsername("janus"),
		tcpostgres.WithPassword("janus-test"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).WithStartupTimeout(60*time.Second)),
	)
	if err != nil {
		t.Skip("postgres/docker not available:", err)
	}
	t.Cleanup(func() { _ = ctr.Terminate(context.Background()) })
	dsn, err := ctr.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatal(err)
	}
	return dsn
}

func TestBootFreshDatabaseMigratesAndStaysSealed(t *testing.T) {
	dsn := bootPostgres(t)
	srv, st, err := Boot(context.Background(), BootConfig{
		DatabaseURL: dsn,
		SealType:    crypto.SealTypeShamir,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if !srv.keyring.Sealed() {
		t.Fatal("fresh boot must be sealed")
	}
	// Migrations applied: the seal config store works (returns uninitialized).
	if _, err := srv.seals.Get(context.Background()); err != crypto.ErrNoSealConfig {
		t.Fatalf("seal store on fresh db: %v, want ErrNoSealConfig", err)
	}
}

func TestBootSealTypeRequiredWhenUninitialized(t *testing.T) {
	dsn := bootPostgres(t)
	_, _, err := Boot(context.Background(), BootConfig{DatabaseURL: dsn})
	if err == nil || !strings.Contains(err.Error(), "JANUS_SEAL_TYPE") {
		t.Fatalf("uninitialized boot without seal type: %v", err)
	}
}

func TestBootTypeMismatchIsFatal(t *testing.T) {
	dsn := bootPostgres(t)
	ctx := context.Background()

	// First boot + init as shamir.
	srv, st, err := Boot(ctx, BootConfig{DatabaseURL: dsn, SealType: crypto.SealTypeShamir})
	if err != nil {
		t.Fatal(err)
	}
	u := crypto.NewShamirUnsealer(srv.seals, 1, 1)
	if _, err := u.Init(ctx); err != nil {
		t.Fatal(err)
	}
	st.Close()

	// Second boot claiming awskms → fatal.
	if _, _, err := Boot(ctx, BootConfig{DatabaseURL: dsn, SealType: crypto.SealTypeAWSKMS}); err == nil || !strings.Contains(err.Error(), "mismatch") {
		t.Fatalf("type mismatch boot: %v", err)
	}
}

func TestBootKMSAutoUnseal(t *testing.T) {
	dsn := bootPostgres(t)
	ctx := context.Background()
	client := &fakeKMS{}
	factory := func(context.Context) (crypto.KMSClient, error) { return client, nil }

	// Boot 1: uninitialized; init via the unsealer, which auto-wraps.
	srv1, st1, err := Boot(ctx, BootConfig{
		DatabaseURL: dsn, SealType: crypto.SealTypeAWSKMS, NewKMSClient: factory,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := srv1.unsealer.Init(ctx); err != nil {
		t.Fatal(err)
	}
	st1.Close()

	// Boot 2: initialized KMS seal → auto-unseals at boot.
	srv2, st2, err := Boot(ctx, BootConfig{
		DatabaseURL: dsn, SealType: "", NewKMSClient: factory, // type from storage
	})
	if err != nil {
		t.Fatal(err)
	}
	defer st2.Close()
	if srv2.keyring.Sealed() {
		t.Fatal("initialized kms boot must auto-unseal")
	}

	// Boot 3: KMS down at boot → stays sealed but serves; retry endpoint works.
	client.fail = true
	srv3, st3, err := Boot(ctx, BootConfig{
		DatabaseURL: dsn, NewKMSClient: factory,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer st3.Close()
	if !srv3.keyring.Sealed() {
		t.Fatal("boot during kms outage must stay sealed, not fail")
	}
	client.fail = false
	if err := srv3.unsealNow(ctx); err != nil {
		t.Fatalf("retry after recovery: %v", err)
	}
	if srv3.keyring.Sealed() {
		t.Fatal("retry should unseal")
	}
}
```

- [ ] **Step 2: Run to verify failure**

Run: `go test ./internal/api/ -run TestBoot`
Expected: FAIL — `undefined: Boot` / `undefined: BootConfig`.

- [ ] **Step 3: Implement Boot**

Create `internal/api/boot.go`:

```go
package api

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/steveokay/janus-secrets/internal/crypto"
	"github.com/steveokay/janus-secrets/internal/secrets"
	"github.com/steveokay/janus-secrets/internal/store"
)

// BootConfig is everything `janus server` derives from the environment.
type BootConfig struct {
	DatabaseURL string
	ListenAddr  string
	// SealType is the operator-configured seal type. Optional once
	// initialized (the stored type is authoritative); a conflicting value is
	// a fatal misconfiguration.
	SealType string
	// NewKMSClient lazily builds the KMS client, called only when the
	// effective seal type is awskms. cmd/janus supplies the real AWS
	// implementation; tests supply fakes.
	NewKMSClient func(context.Context) (crypto.KMSClient, error)
	Logger       *slog.Logger
}

// Boot opens the store, auto-migrates, resolves the seal configuration,
// builds the unsealer and (for an initialized KMS seal) auto-unseals, and
// returns the wired Server. The caller owns closing the returned Store.
func Boot(ctx context.Context, bc BootConfig) (*Server, *store.Store, error) {
	logger := bc.Logger
	if logger == nil {
		logger = slog.Default()
	}

	st, err := store.Open(ctx, bc.DatabaseURL)
	if err != nil {
		return nil, nil, err
	}
	if err := st.Migrate(ctx); err != nil {
		st.Close()
		return nil, nil, fmt.Errorf("migrate: %w", err)
	}

	seals := store.NewSealConfigStore(st)
	stored, err := seals.Get(ctx)
	initialized := true
	if errors.Is(err, crypto.ErrNoSealConfig) {
		initialized = false
	} else if err != nil {
		st.Close()
		return nil, nil, err
	}

	// Resolve the effective seal type: stored wins; env must agree if set.
	var sealType string
	if initialized {
		sealType = stored.Type
		if bc.SealType != "" && bc.SealType != sealType {
			st.Close()
			return nil, nil, fmt.Errorf(
				"seal type mismatch: JANUS_SEAL_TYPE=%q but stored seal is %q", bc.SealType, sealType)
		}
	} else {
		sealType = bc.SealType
		if sealType == "" {
			st.Close()
			return nil, nil, errors.New("JANUS_SEAL_TYPE is required before the seal is initialized")
		}
	}
	if sealType != crypto.SealTypeShamir && sealType != crypto.SealTypeAWSKMS {
		st.Close()
		return nil, nil, fmt.Errorf("unknown seal type %q", sealType)
	}

	kr := crypto.NewKeyring()
	var unsealer crypto.Unsealer
	switch sealType {
	case crypto.SealTypeShamir:
		unsealer = crypto.NewShamirUnsealer(seals, 0, 0)
	case crypto.SealTypeAWSKMS:
		if bc.NewKMSClient == nil {
			st.Close()
			return nil, nil, errors.New("kms seal requires a KMS client")
		}
		client, err := bc.NewKMSClient(ctx)
		if err != nil {
			st.Close()
			return nil, nil, err
		}
		unsealer = crypto.NewKMSUnsealer(seals, client)
	}

	svc := secrets.NewService(st, kr)
	srv := New(Config{ListenAddr: bc.ListenAddr, SealType: sealType}, kr, unsealer, seals, svc, logger)

	// KMS auto-unseal: best-effort at boot; failure keeps serving sealed and
	// POST /v1/sys/unseal retries.
	if initialized && sealType == crypto.SealTypeAWSKMS {
		if err := srv.unsealNow(ctx); err != nil {
			logger.Warn("kms auto-unseal failed; server remains sealed (retry via POST /v1/sys/unseal)",
				"err", err)
		}
	}
	return srv, st, nil
}
```

- [ ] **Step 4: Run tests**

Run: `go test ./internal/api/ -count=1` (Docker required for TestBoot*)
Expected: PASS, boot tests RUN (not skipped).

- [ ] **Step 5: Commit**

```bash
git add internal/api/boot.go internal/api/boot_test.go
git commit -m "feat(api): Boot composes store, seal discovery, unsealer, and kms auto-unseal"
```

---

## Task 7: API-layer leak test

**Files:**
- Create: `internal/api/leak_test.go`

- [ ] **Step 1: Write the test**

Create `internal/api/leak_test.go`:

```go
package api

import (
	"bytes"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/steveokay/janus-secrets/internal/crypto"
)

// TestNoShareMaterialInLogsOrErrors drives the full init/unseal lifecycle with
// log capture and asserts that no share hex ever appears in the logs, and that
// error-path responses never echo submitted share material.
func TestNoShareMaterialInLogsOrErrors(t *testing.T) {
	var logBuf bytes.Buffer
	seals := &memSealStore{}
	kr := crypto.NewKeyring()
	u := crypto.NewShamirUnsealer(seals, 0, 0)
	srv := New(Config{SealType: crypto.SealTypeShamir}, kr, u, seals, nil,
		slog.New(slog.NewTextHandler(&logBuf, nil)))
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	var ir initResp
	doJSON(t, "POST", ts.URL+"/v1/sys/init", `{"shares":5,"threshold":3}`, &ir)
	if len(ir.Shares) != 5 {
		t.Fatalf("init shares = %d", len(ir.Shares))
	}

	// Collect error-path response bodies: duplicate share, poisoned set.
	var errBodies []string
	post := func(body string) string {
		resp, err := http.Post(ts.URL+"/v1/sys/unseal", "application/json", strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return string(b)
	}
	post(fmt.Sprintf(`{"share":%q}`, ir.Shares[0]))
	errBodies = append(errBodies, post(fmt.Sprintf(`{"share":%q}`, ir.Shares[0]))) // duplicate
	post(fmt.Sprintf(`{"share":%q}`, ir.Shares[1]))
	corrupted := "ff" + ir.Shares[2][2:]
	errBodies = append(errBodies, post(fmt.Sprintf(`{"share":%q}`, corrupted))) // poisons the set

	logs := logBuf.String()
	for i, sh := range ir.Shares {
		if strings.Contains(logs, sh) {
			t.Fatalf("share %d leaked into logs", i)
		}
		for _, eb := range errBodies {
			if strings.Contains(eb, sh) {
				t.Fatalf("share %d echoed in error response: %s", i, eb)
			}
		}
	}
	if strings.Contains(logs, corrupted) {
		t.Fatal("submitted share material leaked into logs")
	}
	// Sanity: the logger did log the requests (method/path/status).
	if !strings.Contains(logs, "/v1/sys/unseal") {
		t.Fatalf("expected request logs, got: %q", logs)
	}
}
```

- [ ] **Step 2: Run it**

Run: `go test ./internal/api/ -run TestNoShareMaterial -count=1`
Expected: PASS (it should pass immediately — it guards regressions; if it FAILS, a handler or the logger is leaking and must be fixed, not the test).

- [ ] **Step 3: Commit**

```bash
git add internal/api/leak_test.go
git commit -m "test(api): assert no share material in logs or error responses"
```

---

## Task 8: cobra CLI — server, init, unseal, seal-status, seal, migrate

**Files:**
- Rewrite: `cmd/janus/main.go`
- Create: `cmd/janus/server.go`, `cmd/janus/migrate.go`, `cmd/janus/client.go`, `cmd/janus/sys_commands.go`
- Test: `cmd/janus/sys_commands_test.go`

- [ ] **Step 1: Write the failing CLI tests**

Create `cmd/janus/sys_commands_test.go` (a scripted stub server exercises the client plumbing — no crypto or Postgres):

```go
package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// stubSys scripts the sys API for CLI tests.
func stubSys(t *testing.T, sealType string) (*httptest.Server, *[]string) {
	t.Helper()
	var paths []string
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/sys/init", func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		var req struct{ Shares, Threshold int }
		_ = json.NewDecoder(r.Body).Decode(&req)
		if sealType == "shamir" {
			shares := []string{"aa01", "bb02", "cc03"}
			if req.Shares == 1 {
				shares = []string{"dd04"}
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"type": "shamir", "shares": shares})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"type": "awskms"})
	})
	mux.HandleFunc("GET /v1/sys/seal-status", func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"initialized": true, "sealed": true, "type": sealType,
			"threshold": 3, "shares": 5,
			"progress": map[string]int{"submitted": 1, "required": 3},
		})
	})
	mux.HandleFunc("POST /v1/sys/unseal", func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		var req struct{ Share string }
		_ = json.NewDecoder(r.Body).Decode(&req)
		if sealType == "shamir" && req.Share == "" {
			w.WriteHeader(400)
			_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]string{"code": "validation", "message": "share is required"}})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"sealed": false})
	})
	mux.HandleFunc("POST /v1/sys/seal", func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		_ = json.NewEncoder(w).Encode(map[string]any{"sealed": true})
	})
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	return ts, &paths
}

// runCLI executes the root command with args, returning stdout.
func runCLI(t *testing.T, stdin string, args ...string) (string, error) {
	t.Helper()
	root := newRootCmd()
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	if stdin != "" {
		root.SetIn(strings.NewReader(stdin))
	}
	root.SetArgs(args)
	err := root.Execute()
	return out.String(), err
}

func TestInitCommandPrintsSharesWithWarning(t *testing.T) {
	ts, _ := stubSys(t, "shamir")
	out, err := runCLI(t, "", "init", "--address", ts.URL, "--shares", "5", "--threshold", "3")
	if err != nil {
		t.Fatal(err)
	}
	for _, sh := range []string{"aa01", "bb02", "cc03"} {
		if !strings.Contains(out, sh) {
			t.Fatalf("output missing share %s: %q", sh, out)
		}
	}
	if !strings.Contains(strings.ToLower(out), "will not be shown again") {
		t.Fatalf("output missing warning: %q", out)
	}
}

func TestInitCommandJSON(t *testing.T) {
	ts, _ := stubSys(t, "shamir")
	out, err := runCLI(t, "", "init", "--address", ts.URL, "--json")
	if err != nil {
		t.Fatal(err)
	}
	var resp struct {
		Type   string   `json:"type"`
		Shares []string `json:"shares"`
	}
	if err := json.Unmarshal([]byte(out), &resp); err != nil {
		t.Fatalf("--json output not JSON: %q (%v)", out, err)
	}
	if resp.Type != "shamir" || len(resp.Shares) != 3 {
		t.Fatalf("resp = %+v", resp)
	}
}

func TestUnsealCommandWithFlag(t *testing.T) {
	ts, paths := stubSys(t, "shamir")
	out, err := runCLI(t, "", "unseal", "--address", ts.URL, "--share", "aa01")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "unsealed") {
		t.Fatalf("output = %q", out)
	}
	found := false
	for _, p := range *paths {
		if p == "/v1/sys/unseal" {
			found = true
		}
	}
	if !found {
		t.Fatalf("unseal endpoint not called: %v", *paths)
	}
}

func TestUnsealCommandReadsStdinWhenPiped(t *testing.T) {
	ts, _ := stubSys(t, "shamir")
	out, err := runCLI(t, "aa01\n", "unseal", "--address", ts.URL)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "unsealed") {
		t.Fatalf("output = %q", out)
	}
}

func TestUnsealCommandKMSNeedsNoShare(t *testing.T) {
	ts, _ := stubSys(t, "awskms")
	out, err := runCLI(t, "", "unseal", "--address", ts.URL)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "unsealed") {
		t.Fatalf("output = %q", out)
	}
}

func TestSealStatusCommand(t *testing.T) {
	ts, _ := stubSys(t, "shamir")
	out, err := runCLI(t, "", "seal-status", "--address", ts.URL)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"initialized", "sealed", "shamir", "1/3"} {
		if !strings.Contains(out, want) {
			t.Fatalf("status output missing %q: %q", want, out)
		}
	}
}

func TestSealCommand(t *testing.T) {
	ts, paths := stubSys(t, "shamir")
	if _, err := runCLI(t, "", "seal", "--address", ts.URL); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, p := range *paths {
		if p == "/v1/sys/seal" {
			found = true
		}
	}
	if !found {
		t.Fatalf("seal endpoint not called: %v", *paths)
	}
}
```

- [ ] **Step 2: Run to verify failure**

Run: `go test ./cmd/janus/ -run Test`
Expected: FAIL — `undefined: newRootCmd`.

- [ ] **Step 3: Rewrite main.go onto cobra**

Replace `cmd/janus/main.go` with:

```go
// Command janus is the Janus server and its operator CLI.
package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

var version = "dev"

func newRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:           "janus",
		Short:         "Janus — self-hosted secrets manager",
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.AddCommand(
		newServerCmd(),
		newMigrateCmd(),
		newInitCmd(),
		newUnsealCmd(),
		newSealStatusCmd(),
		newSealCmd(),
		newVersionCmd(),
	)
	return root
}

func newVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the janus version",
		Run: func(cmd *cobra.Command, _ []string) {
			fmt.Fprintln(cmd.OutOrStdout(), "janus", version)
		},
	}
}

func main() {
	if err := newRootCmd().Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "janus:", err)
		os.Exit(1)
	}
}
```

- [ ] **Step 4: Create migrate.go**

Create `cmd/janus/migrate.go` (re-homes the existing logic verbatim):

```go
package main

import (
	"context"
	"errors"
	"os"

	"github.com/spf13/cobra"
	"github.com/steveokay/janus-secrets/internal/store"
)

func newMigrateCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "migrate",
		Short: "Apply database migrations (JANUS_DATABASE_URL)",
		RunE: func(cmd *cobra.Command, _ []string) error {
			dsn := os.Getenv("JANUS_DATABASE_URL")
			if dsn == "" {
				return errors.New("JANUS_DATABASE_URL is not set")
			}
			ctx := context.Background()
			s, err := store.Open(ctx, dsn)
			if err != nil {
				return err
			}
			defer s.Close()
			if err := s.Migrate(ctx); err != nil {
				return err
			}
			cmd.Println("migrations applied")
			return nil
		},
	}
}
```

- [ ] **Step 5: Create server.go**

Create `cmd/janus/server.go`:

```go
package main

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/kms"
	"github.com/spf13/cobra"
	"github.com/steveokay/janus-secrets/internal/api"
	"github.com/steveokay/janus-secrets/internal/crypto"
)

func newServerCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "server",
		Short: "Run the Janus server",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runServer(cmd.Context())
		},
	}
}

func runServer(ctx context.Context) error {
	dsn := os.Getenv("JANUS_DATABASE_URL")
	if dsn == "" {
		return errors.New("JANUS_DATABASE_URL is not set")
	}
	logger := slog.Default()

	bc := api.BootConfig{
		DatabaseURL: dsn,
		ListenAddr:  os.Getenv("JANUS_LISTEN_ADDR"), // "" → :8200 default
		SealType:    os.Getenv("JANUS_SEAL_TYPE"),
		Logger:      logger,
		NewKMSClient: func(ctx context.Context) (crypto.KMSClient, error) {
			arn := os.Getenv("JANUS_AWS_KMS_KEY_ARN")
			if arn == "" {
				return nil, errors.New("JANUS_AWS_KMS_KEY_ARN is not set")
			}
			cfg, err := awsconfig.LoadDefaultConfig(ctx)
			if err != nil {
				return nil, err
			}
			return crypto.NewAWSKMSClient(kms.NewFromConfig(cfg), arn), nil
		},
	}

	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()

	srv, st, err := api.Boot(ctx, bc)
	if err != nil {
		return err
	}
	defer st.Close()

	logger.Info("janus server listening",
		"addr", firstNonEmpty(os.Getenv("JANUS_LISTEN_ADDR"), ":8200"),
		"seal_type", firstNonEmpty(os.Getenv("JANUS_SEAL_TYPE"), "(from storage)"))
	return srv.ListenAndServe(ctx)
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}
```

- [ ] **Step 6: Create client.go**

Create `cmd/janus/client.go`:

```go
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"
)

// defaultAddress resolves the sys-API address: --address flag > JANUS_ADDR >
// http://127.0.0.1:8200.
func defaultAddress() string {
	if v := os.Getenv("JANUS_ADDR"); v != "" {
		return v
	}
	return "http://127.0.0.1:8200"
}

// apiError is a decoded {"error":{...}} envelope.
type apiError struct {
	Status  int
	Code    string
	Message string
}

func (e *apiError) Error() string {
	return fmt.Sprintf("%s (%s, HTTP %d)", e.Message, e.Code, e.Status)
}

// sysCall issues a JSON request to the sys API and decodes the response into
// out (if non-nil). Non-2xx responses are returned as *apiError.
func sysCall(address, method, path string, in, out any) error {
	var body io.Reader
	if in != nil {
		b, err := json.Marshal(in)
		if err != nil {
			return err
		}
		body = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, address+path, body)
	if err != nil {
		return err
	}
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	hc := &http.Client{Timeout: 30 * time.Second}
	resp, err := hc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 {
		var env struct {
			Error struct {
				Code    string `json:"code"`
				Message string `json:"message"`
			} `json:"error"`
		}
		_ = json.NewDecoder(resp.Body).Decode(&env)
		return &apiError{Status: resp.StatusCode, Code: env.Error.Code, Message: env.Error.Message}
	}
	if out != nil {
		return json.NewDecoder(resp.Body).Decode(out)
	}
	return nil
}
```

- [ ] **Step 7: Create sys_commands.go**

Create `cmd/janus/sys_commands.go`:

```go
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"golang.org/x/term"
)

type sealStatus struct {
	Initialized bool   `json:"initialized"`
	Sealed      bool   `json:"sealed"`
	Type        string `json:"type"`
	Threshold   int    `json:"threshold"`
	Shares      int    `json:"shares"`
	Progress    *struct {
		Submitted int `json:"submitted"`
		Required  int `json:"required"`
	} `json:"progress"`
}

func newInitCmd() *cobra.Command {
	var address string
	var shares, threshold int
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "init",
		Short: "Initialize the seal (returns Shamir shares exactly once)",
		RunE: func(cmd *cobra.Command, _ []string) error {
			req := map[string]int{}
			if shares != 0 {
				req["shares"] = shares
			}
			if threshold != 0 {
				req["threshold"] = threshold
			}
			var resp struct {
				Type   string   `json:"type"`
				Shares []string `json:"shares"`
			}
			if err := sysCall(address, "POST", "/v1/sys/init", req, &resp); err != nil {
				return err
			}
			if asJSON {
				enc := json.NewEncoder(cmd.OutOrStdout())
				return enc.Encode(resp)
			}
			cmd.Printf("Seal initialized (type: %s).\n", resp.Type)
			if len(resp.Shares) > 0 {
				cmd.Println("\nUnseal shares — store each in a separate secure location.")
				cmd.Println("They WILL NOT BE SHOWN AGAIN.")
				for i, sh := range resp.Shares {
					cmd.Printf("  Share %d: %s\n", i+1, sh)
				}
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&address, "address", defaultAddress(), "server address")
	cmd.Flags().IntVar(&shares, "shares", 0, "number of Shamir shares (default 5)")
	cmd.Flags().IntVar(&threshold, "threshold", 0, "unseal threshold (default 3)")
	cmd.Flags().BoolVar(&asJSON, "json", false, "emit the raw JSON response")
	return cmd
}

func newUnsealCmd() *cobra.Command {
	var address, share string
	cmd := &cobra.Command{
		Use:   "unseal",
		Short: "Submit an unseal share (or trigger a KMS unseal retry)",
		RunE: func(cmd *cobra.Command, _ []string) error {
			var st sealStatus
			if err := sysCall(address, "GET", "/v1/sys/seal-status", nil, &st); err != nil {
				return err
			}

			var req any
			if st.Type == "awskms" {
				req = nil // empty-body retry
			} else {
				if share == "" {
					s, err := readShare(cmd)
					if err != nil {
						return err
					}
					share = s
				}
				if share == "" {
					return fmt.Errorf("share is required for a shamir seal")
				}
				req = map[string]string{"share": share}
			}

			var resp struct {
				Sealed   bool `json:"sealed"`
				Progress *struct {
					Submitted int `json:"submitted"`
					Required  int `json:"required"`
				} `json:"progress"`
			}
			if err := sysCall(address, "POST", "/v1/sys/unseal", req, &resp); err != nil {
				return err
			}
			if resp.Sealed {
				if resp.Progress != nil {
					cmd.Printf("sealed — %d/%d shares\n", resp.Progress.Submitted, resp.Progress.Required)
				} else {
					cmd.Println("sealed")
				}
				return nil
			}
			cmd.Println("unsealed")
			return nil
		},
	}
	cmd.Flags().StringVar(&address, "address", defaultAddress(), "server address")
	cmd.Flags().StringVar(&share, "share", "", "unseal share (hex); omit to read from stdin")
	return cmd
}

// readShare reads a share from the command's stdin: echo-off prompt on a TTY,
// plain line read when piped.
func readShare(cmd *cobra.Command) (string, error) {
	if f, ok := cmd.InOrStdin().(*os.File); ok && term.IsTerminal(int(f.Fd())) {
		fmt.Fprint(cmd.ErrOrStderr(), "Share: ")
		b, err := term.ReadPassword(int(f.Fd()))
		fmt.Fprintln(cmd.ErrOrStderr())
		if err != nil {
			return "", err
		}
		return strings.TrimSpace(string(b)), nil
	}
	r := bufio.NewReader(cmd.InOrStdin())
	line, err := r.ReadString('\n')
	if err != nil && line == "" {
		return "", err
	}
	return strings.TrimSpace(line), nil
}

func newSealStatusCmd() *cobra.Command {
	var address string
	cmd := &cobra.Command{
		Use:   "seal-status",
		Short: "Show seal status",
		RunE: func(cmd *cobra.Command, _ []string) error {
			var st sealStatus
			if err := sysCall(address, "GET", "/v1/sys/seal-status", nil, &st); err != nil {
				return err
			}
			cmd.Printf("initialized: %v\nsealed:      %v\ntype:        %s\n", st.Initialized, st.Sealed, st.Type)
			if st.Type == "shamir" && st.Initialized {
				cmd.Printf("threshold:   %d of %d\n", st.Threshold, st.Shares)
			}
			if st.Progress != nil {
				cmd.Printf("progress:    %d/%d shares\n", st.Progress.Submitted, st.Progress.Required)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&address, "address", defaultAddress(), "server address")
	return cmd
}

func newSealCmd() *cobra.Command {
	var address string
	cmd := &cobra.Command{
		Use:   "seal",
		Short: "Seal the server (wipes the in-memory master key)",
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := sysCall(address, "POST", "/v1/sys/seal", nil, nil); err != nil {
				return err
			}
			cmd.Println("sealed")
			return nil
		},
	}
	cmd.Flags().StringVar(&address, "address", defaultAddress(), "server address")
	return cmd
}
```

- [ ] **Step 8: Run tests**

Run: `go build ./... && go test ./cmd/janus/ -count=1`
Expected: PASS.

- [ ] **Step 9: Commit**

```bash
git add cmd/janus/
git commit -m "feat(cli): cobra janus CLI — server, init, unseal, seal-status, seal, migrate"
```

---

## Task 9: Dockerfile, compose app service, dev-unseal script, Make targets

**Files:**
- Create: `Dockerfile`
- Modify: `docker-compose.yml`
- Create: `scripts/dev-unseal.sh`
- Modify: `Makefile` (add `dev-up`)
- Modify: `.gitignore` (add `.dev/`)

- [ ] **Step 1: Create the Dockerfile**

Create `Dockerfile` (Go-only multi-stage; the web build stage arrives in Phase 2):

```dockerfile
FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -ldflags="-s -w" -o /janus ./cmd/janus

FROM alpine:3.21
RUN adduser -D -u 10001 janus
USER janus
COPY --from=build /janus /usr/local/bin/janus
EXPOSE 8200
ENTRYPOINT ["janus"]
CMD ["server"]
```

- [ ] **Step 2: Add the app service to docker-compose.yml**

Replace `docker-compose.yml` with:

```yaml
services:
  postgres:
    image: postgres:16-alpine
    environment:
      POSTGRES_USER: janus
      POSTGRES_PASSWORD: janus-dev
      POSTGRES_DB: janus
    ports:
      - "127.0.0.1:5432:5432"
    volumes:
      - pgdata:/var/lib/postgresql/data
    healthcheck:
      test: ["CMD-SHELL", "pg_isready -U janus -d janus"]
      interval: 3s
      timeout: 3s
      retries: 20

  janus:
    build: .
    command: server
    environment:
      JANUS_DATABASE_URL: postgres://janus:janus-dev@postgres:5432/janus?sslmode=disable
      JANUS_SEAL_TYPE: shamir
    ports:
      - "127.0.0.1:8200:8200"
    depends_on:
      postgres:
        condition: service_healthy
    healthcheck:
      test: ["CMD", "wget", "-qO-", "http://127.0.0.1:8200/v1/sys/health"]
      interval: 5s
      timeout: 3s
      retries: 12

volumes:
  pgdata:
```

- [ ] **Step 3: Create the dev-unseal script**

Create `scripts/dev-unseal.sh`:

```bash
#!/usr/bin/env bash
# Dev-only: 1-of-1 seal with the single share cached on disk. The share IS the
# master key — never use this flow outside local development.
set -euo pipefail

ADDR="${JANUS_ADDR:-http://127.0.0.1:8200}"
SHARE_FILE=".dev/janus-share"
JANUS="${JANUS_BIN:-bin/janus}"

# Wait for the server to answer health (60s budget).
for i in $(seq 1 60); do
  if "$JANUS" seal-status --address "$ADDR" >/dev/null 2>&1; then break; fi
  [ "$i" = 60 ] && { echo "server not reachable at $ADDR" >&2; exit 1; }
  sleep 1
done

status="$("$JANUS" seal-status --address "$ADDR")"

if ! echo "$status" | grep -q "initialized: true"; then
  echo "initializing dev seal (1-of-1)..."
  mkdir -p .dev
  umask 177
  "$JANUS" init --shares 1 --threshold 1 --address "$ADDR" \
    | grep -oE '\b[0-9a-f]{32,}\b' | head -1 > "$SHARE_FILE"
  echo "dev share saved to $SHARE_FILE (dev only — this is the master key)"
fi

# Unseal is idempotent: if already unsealed the server just reports the state.
"$JANUS" unseal --address "$ADDR" --share "$(cat "$SHARE_FILE")"
```

Run: `git update-index --chmod=+x scripts/dev-unseal.sh` after adding (or `chmod +x` on Unix).

- [ ] **Step 4: Makefile + .gitignore**

In `Makefile`, replace the `dev` target and add `dev-up`:

```makefile
dev:
	@echo "make dev: hot-reload arrives with the web UI milestone; use 'make dev-up'"; exit 1

dev-up: build
	docker compose up -d --build
	./scripts/dev-unseal.sh
```

In `.gitignore`, add a line: `.dev/`

- [ ] **Step 5: Verify the stack (requires Docker)**

```bash
make build
docker compose up -d --build
./scripts/dev-unseal.sh
bin/janus seal-status
docker compose down
```

Expected: script initializes 1-of-1, saves `.dev/janus-share`, unseals; `seal-status` shows `initialized: true`, `sealed: false`. (If the compose build is slow in the sandbox, verifying `docker compose config` parses + the script against a locally-run `go run ./cmd/janus server` is an acceptable substitute — say which was done.)

- [ ] **Step 6: Commit**

```bash
git add Dockerfile docker-compose.yml scripts/dev-unseal.sh Makefile .gitignore
git commit -m "feat(dev): Dockerfile, compose app service, 1-of-1 dev-unseal workflow"
```

---

## Task 10: Full-suite + security gates + tracker

**Files:**
- Modify: `status.md`

- [ ] **Step 1: Full verification**

```bash
go build ./... && go vet ./...
go test ./... -count=1
go run github.com/securego/gosec/v2/cmd/gosec@v2.27.1 -exclude-dir=internal/crypto/shamir ./...
go run golang.org/x/vuln/cmd/govulncheck@latest ./...
go test -coverprofile=crypto.out ./internal/crypto && go tool cover -func=crypto.out | tail -1
```

Expected: build/vet clean; all tests pass (api/store/secrets against Docker, not skipped); gosec 0 issues; govulncheck clean; crypto coverage 100.0%.

- [ ] **Step 2: Update status.md**

Add a Milestone 4 section mirroring Milestone 3's format (scope delivered, task checklist, verification), check off "Server bootstrap: unseal-at-startup + `janus init`/`unseal` CLI" from the later-milestones list, and update the "Usable in-process, not yet over the wire" note: the server now runs, initializes, and unseals over HTTP (`make dev-up`); still no auth or secret-facing routes.

- [ ] **Step 3: Commit**

```bash
git add status.md
git commit -m "docs: mark Milestone 4 (server bootstrap) complete in status tracker"
```

---

## Self-review notes

- **Spec coverage:** deps + envelope (T1); crypto Progress + 1-of-1 (T2, spec "Existing packages"); middleware + logger (T3); Server/router/health/seal-status/shutdown (T4); init/unseal/reset/seal for both seal types incl. KMS auto-unseal-on-init and retry (T5); Boot with auto-migrate, type resolution/mismatch, KMS boot auto-unseal (T6); leak test (T7); cobra CLI with all six commands, echo-off stdin, seal-type-aware unseal (T8); Dockerfile/compose/dev script/Make/gitignore (T9); gates + tracker (T10). Every spec section maps to a task.
- **Type consistency:** `Config{ListenAddr, SealType}`, `New(cfg, kr, u, seals, svc, logger)`, `BootConfig{DatabaseURL, ListenAddr, SealType, NewKMSClient, Logger}`, `Boot(ctx, bc) (*Server, *store.Store, error)`, `progressBody{Submitted, Required}`, error-code constants — used identically across tasks. Test helpers `memSealStore`/`doJSON`/`errEnvelope`/`initResp`/`unsealResp`/`fakeKMS` are defined once (T4/T5 harness) and reused (T6/T7).
- **Sequencing:** every task compiles and its tests pass at its own commit (T4 ships router stubs so the router compiles before T5 fills them in; T5's KMS test helpers live in the same commit as the KMS handler branches they test).
- **Known judgment calls recorded for reviewers:** `sys/seal` unauthenticated (spec-sanctioned, availability-only); KMS boot-failure log includes the error string (contains no key material — AWS errors are IAM/network detail); handler tests use an in-memory seal store for speed, with Postgres-backed coverage concentrated in `TestBoot*`.
