# OIDC CI Federation (C2) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Let a CI job exchange its GitHub Actions OIDC token for a short-lived, scoped `janus_svc_` service token, gated by admin-registered trust bindings.

**Architecture:** Reuse C1's go-oidc verifier pattern (built from a configured issuer, JWKS discovery) to verify an incoming CI JWT; match its claims against pre-registered bindings; mint a normal short-lived service token via a new `MintFederatedToken` path. New config/bindings tables (migration `000009`), a federation service in `internal/auth`, and exchange + admin routes in `internal/api`.

**Tech Stack:** Go, pgx/PostgreSQL, chi, `github.com/coreos/go-oidc/v3` (already a dep from C1), go-jose (test-only, already a dep).

---

## Context the implementer must know

- **Worktree/branch:** work in `C:/Users/Athelos/Desktop/claude/janus-secrets/.claude/worktrees/oidc-ci-federation` on branch `worktree-oidc-ci-federation` (based on current `main`, which already contains C1 OIDC login and D usage-metrics). Bash = Git Bash / POSIX sh. Module `github.com/steveokay/janus-secrets`.
- **Tests need Docker** (testcontainers/Postgres). Run `go test` with `-count=1`.
- **Stale editor diagnostics:** trust `go build`/`go test` over gopls "undefined"/"unused" for new-in-branch symbols. `gofmt -l` is noisy from repo-wide CRLF — not a gate.
- **C1 reuse surface (read these):**
  - `internal/auth/oidc.go` — `oidcVerifierFor` (build/cache a go-oidc verifier), the `oidcMu`/`oidcCache` caching pattern, `zeroize`, `randToken`.
  - `internal/auth/tokens.go` — `svcTokenPrefix`, `mac(key, raw)`, `s.hmacKey(ctx)`, `metaOf(tok)`, `TokenMeta`, `MintServiceToken`.
  - `internal/store/oidc.go` — repo pattern (`r.s.pool`, `mapError`, single-row Get with `LIMIT 1`).
  - `internal/store/service_tokens.go` — `svcTokenCols`, `scanServiceToken`, `Create`.
  - `internal/api/oidc_handlers.go` + `server.go` — handler/route patterns: `s.record`, `writeError`, `writeJSON`, `s.writeServiceError`, `requireInstance(authz.OIDCManage, ...)`, `loginLimiter.middleware`, `RequireUnsealed`.
  - `internal/api/oidc_mockidp_api_test.go`, `internal/api/oidc_login_test.go` — mock IdP + `authStackFull`, `doAuthed`, `login`, `noRedirectClient`, `errEnvelope`.
- **Gates (run at the end, Task 13):** `go build ./...`, `go vet ./...`, `go test ./... -count=1`, `go test ./internal/crypto/ -cover` (100%), `gosec -exclude-dir=internal/crypto/shamir ./...` (0), `govulncheck ./...` (0 affecting).

## Shared type/constant definitions (used across tasks — keep names identical)

```go
// internal/auth/oidc_federation.go
const (
	defaultFederationIssuer = "https://token.actions.githubusercontent.com"
	federationMaxTTL        = time.Hour
	federationDefaultTTL    = 15 * time.Minute
)

type FederationConfigInput struct {
	Issuer   string // empty → defaultFederationIssuer
	Audience string // required, non-empty
	Enabled  bool
}
type FederationConfigView struct {
	Issuer   string `json:"issuer"`
	Audience string `json:"audience"`
	Enabled  bool   `json:"enabled"`
}
type FederationBindingInput struct {
	Name        string
	MatchClaims map[string]string // must include a non-empty "repository"
	ScopeKind   string            // "config" | "environment"
	ScopeID     string
	Access      string // "read" | "readwrite"
	TTLSeconds  int    // 0 → federationDefaultTTL; must be ≤ federationMaxTTL
	Enabled     bool
}
type FederationBindingView struct {
	ID          string            `json:"id"`
	Name        string            `json:"name"`
	MatchClaims map[string]string `json:"match_claims"`
	ScopeKind   string            `json:"scope_kind"`
	ScopeID     string            `json:"scope_id"`
	Access      string            `json:"access"`
	TTLSeconds  int               `json:"ttl_seconds"`
	Enabled     bool              `json:"enabled"`
}
// FederationResult is the successful exchange outcome (for the handler to audit + return).
type FederationResult struct {
	Token      string
	Meta       TokenMeta
	Binding    string // matched binding name
	Repository string // "repository" claim (for audit)
	Subject    string // "sub" claim (for audit)
}
```

```go
// internal/auth/errors.go — add (all map to one indistinguishable 401 at the API;
// the distinct sentinels only enrich the server-side audit detail).
var (
	ErrFederationNotConfigured = errors.New("auth: federation not configured")
	ErrFederationVerify        = errors.New("auth: federation token verification failed")
	ErrFederationNoMatch       = errors.New("auth: no federation binding matched")
	ErrFederationAmbiguous     = errors.New("auth: multiple federation bindings matched")
)
```

```go
// internal/store/models.go — add
type OIDCFederationConfig struct {
	ID        string
	Issuer    string
	Audience  string
	Enabled   bool
	CreatedAt time.Time
	UpdatedAt time.Time
}
type OIDCFederationBinding struct {
	ID          string
	Name        string
	MatchClaims map[string]string
	ScopeKind   string
	ScopeID     string
	Access      string
	TTLSeconds  int
	Enabled     bool
	CreatedAt   time.Time
	UpdatedAt   time.Time
}
```

---

### Task 1: Migration `000009_oidc_federation` + models

**Files:**
- Create: `migrations/000009_oidc_federation.up.sql`, `migrations/000009_oidc_federation.down.sql`
- Modify: `internal/store/models.go` (add the two structs above)

- [ ] **Step 1: Write the up migration**

`migrations/000009_oidc_federation.up.sql`:
```sql
-- CI federation: a single trust-provider row + claim-matched bindings that mint
-- short-lived scoped service tokens for CI jobs (GitHub Actions OIDC).
CREATE TABLE oidc_federation_config (
    id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    issuer     text NOT NULL,
    audience   text NOT NULL,
    enabled    boolean NOT NULL DEFAULT false,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE oidc_federation_bindings (
    id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    name         text NOT NULL UNIQUE,
    match_claims jsonb NOT NULL,
    scope_kind   text NOT NULL CHECK (scope_kind IN ('config', 'environment')),
    scope_id     uuid NOT NULL,
    access       text NOT NULL CHECK (access IN ('read', 'readwrite')),
    ttl_seconds  integer NOT NULL CHECK (ttl_seconds > 0),
    enabled      boolean NOT NULL DEFAULT true,
    created_at   timestamptz NOT NULL DEFAULT now(),
    updated_at   timestamptz NOT NULL DEFAULT now()
);

-- Federated tokens have no human minter: allow NULL created_by and record the
-- binding that minted the token (forensics + integrity). Existing user tokens
-- keep a non-null created_by.
ALTER TABLE service_tokens ALTER COLUMN created_by DROP NOT NULL;
ALTER TABLE service_tokens ADD COLUMN federation_binding uuid
    REFERENCES oidc_federation_bindings(id) ON DELETE SET NULL;
ALTER TABLE service_tokens ADD CONSTRAINT service_tokens_minter_presence
    CHECK (created_by IS NOT NULL OR federation_binding IS NOT NULL);
```

- [ ] **Step 2: Write the down migration**

`migrations/000009_oidc_federation.down.sql`:
```sql
ALTER TABLE service_tokens DROP CONSTRAINT IF EXISTS service_tokens_minter_presence;
ALTER TABLE service_tokens DROP COLUMN IF EXISTS federation_binding;
-- Restore NOT NULL only if no NULL rows remain (federated tokens must be gone).
ALTER TABLE service_tokens ALTER COLUMN created_by SET NOT NULL;
DROP TABLE IF EXISTS oidc_federation_bindings;
DROP TABLE IF EXISTS oidc_federation_config;
```

- [ ] **Step 3: Add the models** — append the `OIDCFederationConfig` and `OIDCFederationBinding` structs (from the shared definitions above) to `internal/store/models.go`. Ensure `time` is imported (it already is).

- [ ] **Step 4: Verify build + migration applies**

Run: `go build ./... && go test ./internal/store/ -run TestOpen -count=1`
Expected: build clean; the store package's existing connect/migrate test passes (testcontainers runs all migrations including `000009`). If no `TestOpen` exists, instead run `go test ./internal/store/ -run OIDC -count=1` — the C1 OIDC store tests migrate the full schema and must still pass.

- [ ] **Step 5: Commit**

```bash
git add migrations/000009_oidc_federation.up.sql migrations/000009_oidc_federation.down.sql internal/store/models.go
git commit -m "feat(store): migration 000009 — oidc federation config + bindings"
```

---

### Task 2: Federation store repos (config + bindings)

**Files:**
- Create: `internal/store/oidc_federation.go`
- Test: `internal/store/oidc_federation_test.go`

- [ ] **Step 1: Write the failing test** — `internal/store/oidc_federation_test.go`:

```go
package store

import (
	"context"
	"testing"
)

func TestFederationConfigRoundTrip(t *testing.T) {
	st := requireStore(t)
	ctx := context.Background()
	r := NewOIDCFederationConfigRepo(st)

	if _, err := r.Get(ctx); err != ErrNotFound {
		t.Fatalf("empty Get: want ErrNotFound, got %v", err)
	}
	if err := r.Put(ctx, OIDCFederationConfig{
		Issuer: "https://iss.example", Audience: "janus", Enabled: true,
	}); err != nil {
		t.Fatal(err)
	}
	// Put is a single-row upsert: a second Put replaces, not appends.
	if err := r.Put(ctx, OIDCFederationConfig{
		Issuer: "https://iss.example", Audience: "janus2", Enabled: false,
	}); err != nil {
		t.Fatal(err)
	}
	got, err := r.Get(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got.Audience != "janus2" || got.Enabled {
		t.Fatalf("upsert not applied: %+v", got)
	}
	if err := r.Delete(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Get(ctx); err != ErrNotFound {
		t.Fatalf("after delete: want ErrNotFound, got %v", err)
	}
}

func TestFederationBindingRepo(t *testing.T) {
	st := requireStore(t)
	ctx := context.Background()
	// A binding's scope_id FK-free (plain uuid col), but needs a real config/env
	// only at the service layer; the store accepts any uuid. Use a generated id.
	scopeID := st.NewID()
	r := NewOIDCFederationBindingRepo(st)

	b := OIDCFederationBinding{
		Name:        "prod-deploy",
		MatchClaims: map[string]string{"repository": "org/app", "environment": "prod"},
		ScopeKind:   "config", ScopeID: scopeID, Access: "read", TTLSeconds: 900, Enabled: true,
	}
	created, err := r.Create(ctx, b)
	if err != nil {
		t.Fatal(err)
	}
	if created.ID == "" || created.MatchClaims["repository"] != "org/app" {
		t.Fatalf("create returned %+v", created)
	}
	list, err := r.List(ctx)
	if err != nil || len(list) != 1 {
		t.Fatalf("list: %v len=%d", err, len(list))
	}
	if list[0].MatchClaims["environment"] != "prod" || list[0].TTLSeconds != 900 {
		t.Fatalf("round-trip mismatch: %+v", list[0])
	}
	if err := r.Delete(ctx, created.ID); err != nil {
		t.Fatal(err)
	}
	if list, _ := r.List(ctx); len(list) != 0 {
		t.Fatalf("after delete len=%d", len(list))
	}
}
```

> Note: `requireStore(t)` and `st.NewID()` already exist (used by C1 store tests + secrets). Confirm the exact helper name for a migrated test store by grepping `internal/store/*_test.go` for `requireStore` / `func newTestStore`; use whatever the C1 `oidc_test.go` uses.

- [ ] **Step 2: Run it to confirm it fails**

Run: `go test ./internal/store/ -run "TestFederationConfigRoundTrip|TestFederationBindingRepo" -count=1`
Expected: FAIL (undefined `NewOIDCFederationConfigRepo` / `NewOIDCFederationBindingRepo`).

- [ ] **Step 3: Implement the repos** — `internal/store/oidc_federation.go`:

```go
package store

import (
	"context"
	"encoding/json"

	"github.com/jackc/pgx/v5"
)

// --- config (single row) ---

const fedConfigCols = `id::text, issuer, audience, enabled, created_at, updated_at`

type OIDCFederationConfigRepo struct{ s *Store }

func NewOIDCFederationConfigRepo(s *Store) *OIDCFederationConfigRepo {
	return &OIDCFederationConfigRepo{s: s}
}

// Put upserts the single federation config row (delete-then-insert keeps it to
// one row without needing a unique sentinel column).
func (r *OIDCFederationConfigRepo) Put(ctx context.Context, c OIDCFederationConfig) error {
	return r.s.withTx(ctx, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `DELETE FROM oidc_federation_config`); err != nil {
			return err
		}
		_, err := tx.Exec(ctx,
			`INSERT INTO oidc_federation_config (issuer, audience, enabled)
			 VALUES ($1, $2, $3)`, c.Issuer, c.Audience, c.Enabled)
		return err
	})
}

func (r *OIDCFederationConfigRepo) Get(ctx context.Context) (*OIDCFederationConfig, error) {
	row := r.s.pool.QueryRow(ctx,
		`SELECT `+fedConfigCols+` FROM oidc_federation_config ORDER BY created_at LIMIT 1`)
	var c OIDCFederationConfig
	if err := row.Scan(&c.ID, &c.Issuer, &c.Audience, &c.Enabled, &c.CreatedAt, &c.UpdatedAt); err != nil {
		return nil, mapError(err)
	}
	return &c, nil
}

func (r *OIDCFederationConfigRepo) Delete(ctx context.Context) error {
	_, err := r.s.pool.Exec(ctx, `DELETE FROM oidc_federation_config`)
	return mapError(err)
}

// --- bindings ---

const fedBindingCols = `id::text, name, match_claims, scope_kind, scope_id::text,
	access, ttl_seconds, enabled, created_at, updated_at`

type OIDCFederationBindingRepo struct{ s *Store }

func NewOIDCFederationBindingRepo(s *Store) *OIDCFederationBindingRepo {
	return &OIDCFederationBindingRepo{s: s}
}

func (r *OIDCFederationBindingRepo) Create(ctx context.Context, b OIDCFederationBinding) (*OIDCFederationBinding, error) {
	claims, err := json.Marshal(b.MatchClaims)
	if err != nil {
		return nil, err
	}
	row := r.s.pool.QueryRow(ctx,
		`INSERT INTO oidc_federation_bindings
		   (name, match_claims, scope_kind, scope_id, access, ttl_seconds, enabled)
		 VALUES ($1, $2, $3, $4::uuid, $5, $6, $7)
		 RETURNING `+fedBindingCols,
		b.Name, claims, b.ScopeKind, b.ScopeID, b.Access, b.TTLSeconds, b.Enabled)
	return scanFedBinding(row)
}

func (r *OIDCFederationBindingRepo) List(ctx context.Context) ([]OIDCFederationBinding, error) {
	rows, err := r.s.pool.Query(ctx,
		`SELECT `+fedBindingCols+` FROM oidc_federation_bindings ORDER BY created_at`)
	if err != nil {
		return nil, mapError(err)
	}
	defer rows.Close()
	var out []OIDCFederationBinding
	for rows.Next() {
		b, err := scanFedBinding(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *b)
	}
	return out, mapError(rows.Err())
}

func (r *OIDCFederationBindingRepo) Delete(ctx context.Context, id string) error {
	return r.s.execAffectingOne(ctx,
		`DELETE FROM oidc_federation_bindings WHERE id = $1::uuid`, id)
}

func scanFedBinding(row interface{ Scan(...any) error }) (*OIDCFederationBinding, error) {
	var b OIDCFederationBinding
	var claims []byte
	if err := row.Scan(&b.ID, &b.Name, &claims, &b.ScopeKind, &b.ScopeID,
		&b.Access, &b.TTLSeconds, &b.Enabled, &b.CreatedAt, &b.UpdatedAt); err != nil {
		return nil, mapError(err)
	}
	if err := json.Unmarshal(claims, &b.MatchClaims); err != nil {
		return nil, err
	}
	return &b, nil
}
```

> Confirm `r.s.withTx`, `r.s.execAffectingOne`, and `mapError` signatures by reading `internal/store/oidc.go` and `internal/store/service_tokens.go` — these are the exact helpers C1 and the token repo use.

- [ ] **Step 4: Run to verify pass**

Run: `go test ./internal/store/ -run "TestFederationConfigRoundTrip|TestFederationBindingRepo" -count=1`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/store/oidc_federation.go internal/store/oidc_federation_test.go
git commit -m "feat(store): OIDC federation config + binding repos"
```

---

### Task 3: `service_tokens` — nullable minter + `CreateFederated`

**Files:**
- Modify: `internal/store/models.go` (add `FederationBinding string` to `ServiceToken`)
- Modify: `internal/store/service_tokens.go` (`svcTokenCols`, `scanServiceToken`, add `CreateFederated`)
- Test: `internal/store/service_tokens_federated_test.go`

- [ ] **Step 1: Write the failing test** — `internal/store/service_tokens_federated_test.go`:

```go
package store

import (
	"context"
	"testing"
	"time"
)

func TestCreateFederatedToken(t *testing.T) {
	st := requireStore(t)
	ctx := context.Background()
	// A binding to attribute the token to (FK target).
	bindings := NewOIDCFederationBindingRepo(st)
	b, err := bindings.Create(ctx, OIDCFederationBinding{
		Name: "b1", MatchClaims: map[string]string{"repository": "org/app"},
		ScopeKind: "config", ScopeID: st.NewID(), Access: "read", TTLSeconds: 900, Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	exp := time.Now().Add(15 * time.Minute)
	tok, err := NewServiceTokenRepo(st).CreateFederated(ctx,
		"ci-token", []byte("hmac-bytes-32-............."), "config", st.NewID(), "read", &exp, b.ID)
	if err != nil {
		t.Fatal(err)
	}
	if tok.CreatedBy != "" {
		t.Fatalf("federated token should have empty CreatedBy, got %q", tok.CreatedBy)
	}
	if tok.FederationBinding != b.ID {
		t.Fatalf("federation_binding = %q, want %q", tok.FederationBinding, b.ID)
	}
	if tok.ExpiresAt == nil {
		t.Fatal("expected expiry set")
	}
}
```

> Confirm the token-repo constructor name (`NewServiceTokenRepo`) by grepping `internal/store/service_tokens.go`.

- [ ] **Step 2: Run to confirm it fails**

Run: `go test ./internal/store/ -run TestCreateFederatedToken -count=1`
Expected: FAIL (undefined `CreateFederated` / `FederationBinding`).

- [ ] **Step 3: Implement** — three edits in `internal/store`:

(a) In `internal/store/models.go`, add a field to `ServiceToken`:
```go
	FederationBinding string // "" for user-minted tokens
```

(b) In `internal/store/service_tokens.go`, extend `svcTokenCols` and make `scanServiceToken` tolerate NULL `created_by` and the new column:
```go
const svcTokenCols = `id::text, name, token_hmac, created_by::text, scope_kind,
	scope_id::text, access, created_at, expires_at, revoked_at, federation_binding::text`

func scanServiceToken(row interface{ Scan(...any) error }) (*ServiceToken, error) {
	var t ServiceToken
	var scopeID, createdBy, fedBinding *string // all nullable
	if err := row.Scan(&t.ID, &t.Name, &t.TokenHMAC, &createdBy, &t.ScopeKind,
		&scopeID, &t.Access, &t.CreatedAt, &t.ExpiresAt, &t.RevokedAt, &fedBinding); err != nil {
		return nil, mapError(err)
	}
	if scopeID != nil {
		t.ScopeID = *scopeID
	}
	if createdBy != nil {
		t.CreatedBy = *createdBy
	}
	if fedBinding != nil {
		t.FederationBinding = *fedBinding
	}
	return &t, nil
}
```

(c) Add `CreateFederated` in `internal/store/service_tokens.go`:
```go
// CreateFederated inserts a service token minted by CI federation: created_by is
// NULL and federation_binding records the matched binding.
func (r *ServiceTokenRepo) CreateFederated(ctx context.Context, name string, tokenHMAC []byte,
	scopeKind, scopeID, access string, expiresAt *time.Time, bindingID string) (*ServiceToken, error) {
	var sid any = scopeID
	if scopeID == "" {
		sid = nil
	}
	row := r.s.pool.QueryRow(ctx,
		`INSERT INTO service_tokens
		   (name, token_hmac, created_by, scope_kind, scope_id, access, expires_at, federation_binding)
		 VALUES ($1, $2, NULL, $3, $4, $5, $6, $7::uuid)
		 RETURNING `+svcTokenCols,
		name, tokenHMAC, scopeKind, sid, access, expiresAt, bindingID)
	return scanServiceToken(row)
}
```

- [ ] **Step 4: Run to verify pass + no regressions in token tests**

Run: `go test ./internal/store/ -run "TestCreateFederatedToken|ServiceToken" -count=1`
Expected: PASS (the existing `Create`/`List`/`GetByHMAC` tests still pass with the new nullable scan).

- [ ] **Step 5: Commit**

```bash
git add internal/store/models.go internal/store/service_tokens.go internal/store/service_tokens_federated_test.go
git commit -m "feat(store): nullable minter + CreateFederated for CI tokens"
```

---

### Task 4: `MintFederatedToken` (auth)

**Files:**
- Modify: `internal/auth/tokens.go` (add `MintFederatedToken`)
- Test: `internal/auth/tokens_federated_test.go`

- [ ] **Step 1: Write the failing test** — `internal/auth/tokens_federated_test.go`:

```go
package auth

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestMintFederatedToken(t *testing.T) {
	svc, _, _ := newTestService(t) // unsealed service (C1 helper)
	ctx := context.Background()
	// Need a real config scope + a binding for the FK. Reuse store repos directly.
	scopeID := seedConfigScope(t, svc) // see note
	b, err := svc.CreateFederationBinding(ctx, FederationBindingInput{
		Name: "b1", MatchClaims: map[string]string{"repository": "org/app"},
		ScopeKind: "config", ScopeID: scopeID, Access: "read", TTLSeconds: 900, Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	raw, meta, err := svc.MintFederatedToken(ctx, b.Name, "config", scopeID, "read", 15*time.Minute, b.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(raw, "janus_svc_") {
		t.Fatalf("token prefix: %s", raw)
	}
	if meta.ExpiresAt == nil {
		t.Fatal("expected expiry")
	}
	// The federated token verifies like any service token.
	p, scope, err := svc.VerifyServiceToken(ctx, raw)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if scope.Kind != "config" || scope.ID != scopeID || p.Kind != KindServiceToken {
		t.Fatalf("verified scope/principal wrong: %+v %+v", scope, p)
	}
}
```

> This test depends on Task 6 (`CreateFederationBinding`) and a scope-seeding helper. **Order note:** if the implementer does tasks strictly in order, write only the `MintFederatedToken` portion here and stub the binding via the store repo directly (`store.NewOIDCFederationBindingRepo`), then let Task 6 add `CreateFederationBinding`. To avoid a forward dependency, replace the `svc.CreateFederationBinding(...)` call with a direct store insert:
> ```go
> br := storeBindingRepoFor(t, svc) // grep how newTestService exposes the *store.Store; or add a tiny test accessor
> b, err := br.Create(ctx, store.OIDCFederationBinding{Name: "b1", MatchClaims: map[string]string{"repository": "org/app"}, ScopeKind: "config", ScopeID: scopeID, Access: "read", TTLSeconds: 900, Enabled: true})
> ```
> Use whichever keeps the test self-contained. `seedConfigScope`/`storeBindingRepoFor` are test helpers you write next to this test using the same `*store.Store` the service holds (grep `newTestService` in `internal/auth/*_test.go` to see how it exposes the store — C1's harness constructs the service from a store it can reach). `KindServiceToken` is the existing principal kind returned by `VerifyServiceToken` — confirm its exact name in `internal/auth/principal.go`/`tokens.go`.

- [ ] **Step 2: Run to confirm it fails**

Run: `go test ./internal/auth/ -run TestMintFederatedToken -count=1`
Expected: FAIL (undefined `MintFederatedToken`).

- [ ] **Step 3: Implement `MintFederatedToken`** in `internal/auth/tokens.go`:

```go
// MintFederatedToken issues a short-lived service token for a matched CI
// federation binding. Unlike MintServiceToken there is no human minter; the
// token is attributed to the binding. Scope validity is the caller's concern
// (the binding was validated at config time).
func (s *Service) MintFederatedToken(ctx context.Context, name, scopeKind, scopeID, access string,
	ttl time.Duration, bindingID string) (string, TokenMeta, error) {
	random, err := randToken(32)
	if err != nil {
		return "", TokenMeta{}, err
	}
	raw := svcTokenPrefix + random
	key, err := s.hmacKey(ctx)
	if err != nil {
		return "", TokenMeta{}, err
	}
	defer zeroize(key)
	expiresAt := time.Now().Add(ttl)
	tok, err := s.tokens.CreateFederated(ctx, name, mac(key, raw), scopeKind, scopeID, access, &expiresAt, bindingID)
	if err != nil {
		return "", TokenMeta{}, err
	}
	return raw, metaOf(tok), nil
}
```

- [ ] **Step 4: Run to verify pass**

Run: `go test ./internal/auth/ -run TestMintFederatedToken -count=1`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/auth/tokens.go internal/auth/tokens_federated_test.go
git commit -m "feat(auth): MintFederatedToken — short-lived CI-scoped service token"
```

---

### Task 5: Service wiring — federation repos + fields on `Service`

**Files:**
- Modify: `internal/auth/service.go` (add repos + verifier-cache fields; wire in `NewService`)

- [ ] **Step 1: Add fields to the `Service` struct** (mirror the C1 `oidcProviders`/`oidcMu`/`oidcCache` fields):

```go
	oidcFedConfig   *store.OIDCFederationConfigRepo
	oidcFedBindings *store.OIDCFederationBindingRepo
	fedMu           sync.Mutex
	fedCache        *fedVerifier
```

- [ ] **Step 2: Wire them in `NewService`** where the C1 OIDC repos are constructed:

```go
	oidcFedConfig:   store.NewOIDCFederationConfigRepo(st),
	oidcFedBindings: store.NewOIDCFederationBindingRepo(st),
```

- [ ] **Step 3: Add a placeholder `fedVerifier` type** (filled in Task 7) so the struct compiles — put it in `internal/auth/oidc_federation.go` (create the file):

```go
package auth

// fedVerifier caches the go-oidc verifier for the configured federation issuer.
type fedVerifier struct {
	issuer   string
	audience string
	verifier *oidc.IDTokenVerifier
}
```
Add imports as needed (`github.com/coreos/go-oidc/v3/oidc`).

- [ ] **Step 4: Verify build**

Run: `go build ./... && go vet ./internal/auth/`
Expected: clean (unused `fedVerifier` fields are fine; they're referenced next tasks).

- [ ] **Step 5: Commit**

```bash
git add internal/auth/service.go internal/auth/oidc_federation.go
git commit -m "feat(auth): wire federation repos + verifier cache onto Service"
```

---

### Task 6: Federation config + binding service CRUD (with validation)

**Files:**
- Modify: `internal/auth/oidc_federation.go` (config/binding CRUD + validation + `invalidateFederationVerifier`)
- Modify: `internal/auth/errors.go` (add the federation sentinels)
- Test: `internal/auth/oidc_federation_config_test.go`

- [ ] **Step 1: Write the failing test** — `internal/auth/oidc_federation_config_test.go`:

```go
package auth

import (
	"context"
	"testing"
)

func TestFederationConfigAndBindingValidation(t *testing.T) {
	svc, _, _ := newTestService(t)
	ctx := context.Background()
	scopeID := seedConfigScope(t, svc)

	// Empty audience rejected.
	if err := svc.SetFederationConfig(ctx, FederationConfigInput{Audience: "", Enabled: true}); err != ErrValidation {
		t.Fatalf("empty audience: want ErrValidation, got %v", err)
	}
	// Empty issuer defaults to GitHub Actions.
	if err := svc.SetFederationConfig(ctx, FederationConfigInput{Audience: "janus", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	cfg, err := svc.GetFederationConfig(ctx)
	if err != nil || cfg.Issuer != defaultFederationIssuer || cfg.Audience != "janus" {
		t.Fatalf("config: %v %+v", err, cfg)
	}

	// Binding without a repository claim is rejected.
	if _, err := svc.CreateFederationBinding(ctx, FederationBindingInput{
		Name: "bad", MatchClaims: map[string]string{"environment": "prod"},
		ScopeKind: "config", ScopeID: scopeID, Access: "read", TTLSeconds: 900, Enabled: true,
	}); err != ErrValidation {
		t.Fatalf("missing repository: want ErrValidation, got %v", err)
	}
	// TTL over the cap is rejected.
	if _, err := svc.CreateFederationBinding(ctx, FederationBindingInput{
		Name: "toolong", MatchClaims: map[string]string{"repository": "org/app"},
		ScopeKind: "config", ScopeID: scopeID, Access: "read", TTLSeconds: 7200, Enabled: true,
	}); err != ErrValidation {
		t.Fatalf("ttl over cap: want ErrValidation, got %v", err)
	}
	// Unknown scope is rejected.
	if _, err := svc.CreateFederationBinding(ctx, FederationBindingInput{
		Name: "badscope", MatchClaims: map[string]string{"repository": "org/app"},
		ScopeKind: "config", ScopeID: svc.newIDForTest(), Access: "read", TTLSeconds: 900, Enabled: true,
	}); err == nil {
		t.Fatal("unknown scope: want error")
	}
	// Valid binding, default TTL when 0.
	b, err := svc.CreateFederationBinding(ctx, FederationBindingInput{
		Name: "ok", MatchClaims: map[string]string{"repository": "org/app"},
		ScopeKind: "config", ScopeID: scopeID, Access: "read", TTLSeconds: 0, Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if b.TTLSeconds != int(federationDefaultTTL.Seconds()) {
		t.Fatalf("default ttl not applied: %d", b.TTLSeconds)
	}
	if list, _ := svc.ListFederationBindings(ctx); len(list) != 1 {
		t.Fatalf("list len=%d", len(list))
	}
	if err := svc.DeleteFederationBinding(ctx, b.ID); err != nil {
		t.Fatal(err)
	}
}
```

> `seedConfigScope(t, svc)` must create a real config and return its id (federation bindings validate the scope exists, like `MintServiceToken` validates via `s.configs.Get`). Write it using the same project→env→config creation the C1/token tests use — grep `internal/auth/*_test.go` for how tokens tests build a config scope (they call the secrets service or store repos). `svc.newIDForTest()` — use whatever the harness exposes for a random-but-absent uuid, or just a constant well-formed uuid string that won't exist, e.g. `"00000000-0000-0000-0000-0000000000ff"`.

- [ ] **Step 2: Run to confirm it fails**

Run: `go test ./internal/auth/ -run TestFederationConfigAndBindingValidation -count=1`
Expected: FAIL (undefined methods).

- [ ] **Step 3: Implement the CRUD + validation** in `internal/auth/oidc_federation.go`:

```go
func (s *Service) SetFederationConfig(ctx context.Context, in FederationConfigInput) error {
	if strings.TrimSpace(in.Audience) == "" {
		return ErrValidation
	}
	issuer := strings.TrimSpace(in.Issuer)
	if issuer == "" {
		issuer = defaultFederationIssuer
	}
	if err := s.oidcFedConfig.Put(ctx, store.OIDCFederationConfig{
		Issuer: issuer, Audience: in.Audience, Enabled: in.Enabled,
	}); err != nil {
		return err
	}
	s.invalidateFederationVerifier()
	return nil
}

func (s *Service) GetFederationConfig(ctx context.Context) (*FederationConfigView, error) {
	c, err := s.oidcFedConfig.Get(ctx)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &FederationConfigView{Issuer: c.Issuer, Audience: c.Audience, Enabled: c.Enabled}, nil
}

func (s *Service) DeleteFederationConfig(ctx context.Context) error {
	if err := s.oidcFedConfig.Delete(ctx); err != nil {
		return err
	}
	s.invalidateFederationVerifier()
	return nil
}

func (s *Service) CreateFederationBinding(ctx context.Context, in FederationBindingInput) (*FederationBindingView, error) {
	if strings.TrimSpace(in.Name) == "" {
		return nil, ErrValidation
	}
	if strings.TrimSpace(in.MatchClaims["repository"]) == "" {
		return nil, ErrValidation // repository condition is mandatory
	}
	if in.Access != "read" && in.Access != "readwrite" {
		return nil, ErrValidation
	}
	ttl := in.TTLSeconds
	if ttl == 0 {
		ttl = int(federationDefaultTTL.Seconds())
	}
	if ttl < 0 || ttl > int(federationMaxTTL.Seconds()) {
		return nil, ErrValidation
	}
	switch in.ScopeKind {
	case "config":
		if _, err := s.configs.Get(ctx, in.ScopeID); err != nil {
			return nil, scopeErr(err)
		}
	case "environment":
		if _, err := s.envs.Get(ctx, in.ScopeID); err != nil {
			return nil, scopeErr(err)
		}
	default:
		return nil, ErrValidation
	}
	b, err := s.oidcFedBindings.Create(ctx, store.OIDCFederationBinding{
		Name: in.Name, MatchClaims: in.MatchClaims, ScopeKind: in.ScopeKind,
		ScopeID: in.ScopeID, Access: in.Access, TTLSeconds: ttl, Enabled: in.Enabled,
	})
	if err != nil {
		return nil, err
	}
	return fedBindingView(b), nil
}

func (s *Service) ListFederationBindings(ctx context.Context) ([]FederationBindingView, error) {
	bs, err := s.oidcFedBindings.List(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]FederationBindingView, 0, len(bs))
	for i := range bs {
		out = append(out, *fedBindingView(&bs[i]))
	}
	return out, nil
}

func (s *Service) DeleteFederationBinding(ctx context.Context, id string) error {
	if err := s.oidcFedBindings.Delete(ctx, id); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return ErrNotFound
		}
		return err
	}
	return nil
}

func (s *Service) invalidateFederationVerifier() {
	s.fedMu.Lock()
	s.fedCache = nil
	s.fedMu.Unlock()
}

func fedBindingView(b *store.OIDCFederationBinding) *FederationBindingView {
	return &FederationBindingView{
		ID: b.ID, Name: b.Name, MatchClaims: b.MatchClaims, ScopeKind: b.ScopeKind,
		ScopeID: b.ScopeID, Access: b.Access, TTLSeconds: b.TTLSeconds, Enabled: b.Enabled,
	}
}
```

Add the four `ErrFederation*` sentinels to `internal/auth/errors.go` (shared block above). Ensure `strings`, `errors`, and the `store` import are present in `oidc_federation.go`. `s.configs`/`s.envs` and `scopeErr` already exist (used by `MintServiceToken`).

- [ ] **Step 4: Run to verify pass**

Run: `go test ./internal/auth/ -run TestFederationConfigAndBindingValidation -count=1`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/auth/oidc_federation.go internal/auth/errors.go internal/auth/oidc_federation_config_test.go
git commit -m "feat(auth): federation config + binding CRUD with validation"
```

---

### Task 7: Claim matcher (pure) + unit test

**Files:**
- Modify: `internal/auth/oidc_federation.go` (`matchFederationBinding`, `claimsSatisfy`, `stringClaims`)
- Test: `internal/auth/oidc_federation_match_test.go`

- [ ] **Step 1: Write the failing test** — `internal/auth/oidc_federation_match_test.go`:

```go
package auth

import (
	"errors"
	"testing"

	"github.com/steveokay/janus-secrets/internal/store"
)

func TestMatchFederationBinding(t *testing.T) {
	mk := func(name string, claims map[string]string, enabled bool) store.OIDCFederationBinding {
		return store.OIDCFederationBinding{ID: name, Name: name, MatchClaims: claims, Enabled: enabled}
	}
	prod := mk("prod", map[string]string{"repository": "org/app", "environment": "prod"}, true)
	anyRef := mk("any", map[string]string{"repository": "org/app"}, true)
	disabled := mk("dis", map[string]string{"repository": "org/app"}, false)

	tok := map[string]string{"repository": "org/app", "environment": "prod", "ref": "refs/heads/main"}

	tests := []struct {
		name     string
		bindings []store.OIDCFederationBinding
		want     string // matched binding name, or "" with wantErr
		wantErr  error
	}{
		{"single match", []store.OIDCFederationBinding{prod}, "prod", nil},
		{"extra token claims ignored", []store.OIDCFederationBinding{anyRef}, "any", nil},
		{"no match", []store.OIDCFederationBinding{mk("x", map[string]string{"repository": "org/other"}, true)}, "", ErrFederationNoMatch},
		{"ambiguous", []store.OIDCFederationBinding{prod, anyRef}, "", ErrFederationAmbiguous},
		{"disabled skipped", []store.OIDCFederationBinding{disabled}, "", ErrFederationNoMatch},
		{"empty claims never matches", []store.OIDCFederationBinding{mk("empty", map[string]string{}, true)}, "", ErrFederationNoMatch},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			b, err := matchFederationBinding(tok, tc.bindings)
			if tc.wantErr != nil {
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("err = %v, want %v", err, tc.wantErr)
				}
				return
			}
			if err != nil || b.Name != tc.want {
				t.Fatalf("got (%v, %v), want %s", b, err, tc.want)
			}
		})
	}
}
```

- [ ] **Step 2: Run to confirm it fails**

Run: `go test ./internal/auth/ -run TestMatchFederationBinding -count=1`
Expected: FAIL (undefined `matchFederationBinding`).

- [ ] **Step 3: Implement** in `internal/auth/oidc_federation.go`:

```go
// matchFederationBinding returns the single enabled binding whose every
// match_claims entry equals the token's claim. Zero matches → ErrFederationNoMatch;
// more than one → ErrFederationAmbiguous (no "most specific wins" guessing).
func matchFederationBinding(claims map[string]string, bindings []store.OIDCFederationBinding) (*store.OIDCFederationBinding, error) {
	var matched *store.OIDCFederationBinding
	for i := range bindings {
		b := &bindings[i]
		if !b.Enabled || !claimsSatisfy(claims, b.MatchClaims) {
			continue
		}
		if matched != nil {
			return nil, ErrFederationAmbiguous
		}
		matched = b
	}
	if matched == nil {
		return nil, ErrFederationNoMatch
	}
	return matched, nil
}

// claimsSatisfy is true when every wanted claim equals the token's claim. An
// empty want never matches (defense in depth against a claim-less binding).
func claimsSatisfy(tokenClaims, want map[string]string) bool {
	if len(want) == 0 {
		return false
	}
	for k, v := range want {
		if tokenClaims[k] != v {
			return false
		}
	}
	return true
}

// stringClaims projects a raw claim set to its string-valued entries (the only
// kind bindings match on). Non-string claims (iat/exp numbers) are dropped.
func stringClaims(raw map[string]any) map[string]string {
	out := make(map[string]string, len(raw))
	for k, v := range raw {
		if s, ok := v.(string); ok {
			out[k] = s
		}
	}
	return out
}
```

- [ ] **Step 4: Run to verify pass**

Run: `go test ./internal/auth/ -run TestMatchFederationBinding -count=1`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/auth/oidc_federation.go internal/auth/oidc_federation_match_test.go
git commit -m "feat(auth): federation claim matcher (exactly-one, deny-ambiguous)"
```

---

### Task 8: `federationVerifierFor` + `FederateCILogin` (end-to-end at service layer)

**Files:**
- Modify: `internal/auth/oidc_federation.go` (`federationVerifierFor`, `FederateCILogin`)
- Test: `internal/auth/oidc_federation_exchange_test.go` (+ extend the mock IdP to sign arbitrary claims)

- [ ] **Step 1: Extend the auth-package mock IdP to sign arbitrary claims.** Read `internal/auth/oidc_mockidp_test.go`; add a method that signs a caller-supplied claim map and returns the raw JWT:

```go
// signClaims signs an arbitrary claim set (for CI-federation tests) and returns
// the compact JWT. Mirrors signIDToken but takes explicit claims.
func (m *mockIdP) signClaims(t *testing.T, claims map[string]any) string {
	t.Helper()
	sig, err := jose.NewSigner(
		jose.SigningKey{Algorithm: jose.RS256, Key: m.key},
		(&jose.SignerOptions{}).WithType("JWT").WithHeader("kid", m.keyID))
	if err != nil {
		t.Fatal(err)
	}
	raw, err := jwt.Signed(sig).Claims(claims).Serialize()
	if err != nil {
		t.Fatal(err)
	}
	return raw
}
```

- [ ] **Step 2: Write the failing test** — `internal/auth/oidc_federation_exchange_test.go`:

```go
package auth

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestFederateCILogin(t *testing.T) {
	svc, _, _ := newTestService(t)
	ctx := context.Background()
	idp := newMockIdP(t, "janus") // clientID unused here; issuer = idp.srv.URL
	scopeID := seedConfigScope(t, svc)

	if err := svc.SetFederationConfig(ctx, FederationConfigInput{
		Issuer: idp.srv.URL, Audience: "janus", Enabled: true,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.CreateFederationBinding(ctx, FederationBindingInput{
		Name: "prod", MatchClaims: map[string]string{"repository": "org/app", "environment": "prod"},
		ScopeKind: "config", ScopeID: scopeID, Access: "read", TTLSeconds: 900, Enabled: true,
	}); err != nil {
		t.Fatal(err)
	}

	good := idp.signClaims(t, map[string]any{
		"iss": idp.srv.URL, "aud": "janus", "sub": "repo:org/app:environment:prod",
		"exp": time.Now().Add(time.Hour).Unix(), "iat": time.Now().Unix(),
		"repository": "org/app", "environment": "prod", "ref": "refs/heads/main",
	})
	res, err := svc.FederateCILogin(ctx, good)
	if err != nil {
		t.Fatalf("exchange: %v", err)
	}
	if res.Binding != "prod" || res.Repository != "org/app" || res.Token == "" {
		t.Fatalf("result: %+v", res)
	}
	// The minted token authorizes as a config-scoped read token.
	if _, scope, err := svc.VerifyServiceToken(ctx, res.Token); err != nil || scope.ID != scopeID {
		t.Fatalf("verify minted: %v %+v", err, scope)
	}

	// Wrong audience → denied (verification failure).
	badAud := idp.signClaims(t, map[string]any{
		"iss": idp.srv.URL, "aud": "someone-else", "sub": "x",
		"exp": time.Now().Add(time.Hour).Unix(), "repository": "org/app", "environment": "prod",
	})
	if _, err := svc.FederateCILogin(ctx, badAud); !errors.Is(err, ErrFederationVerify) {
		t.Fatalf("bad aud: want ErrFederationVerify, got %v", err)
	}
	// No matching binding → ErrFederationNoMatch.
	noMatch := idp.signClaims(t, map[string]any{
		"iss": idp.srv.URL, "aud": "janus", "sub": "x",
		"exp": time.Now().Add(time.Hour).Unix(), "repository": "org/other",
	})
	if _, err := svc.FederateCILogin(ctx, noMatch); !errors.Is(err, ErrFederationNoMatch) {
		t.Fatalf("no match: want ErrFederationNoMatch, got %v", err)
	}
}
```

- [ ] **Step 3: Run to confirm it fails**

Run: `go test ./internal/auth/ -run TestFederateCILogin -count=1`
Expected: FAIL (undefined `FederateCILogin`).

- [ ] **Step 4: Implement** in `internal/auth/oidc_federation.go`:

```go
// federationVerifierFor builds (or returns cached) the go-oidc verifier for the
// configured, enabled federation provider. ErrFederationNotConfigured if none.
func (s *Service) federationVerifierFor(ctx context.Context) (*fedVerifier, error) {
	c, err := s.oidcFedConfig.Get(ctx)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, ErrFederationNotConfigured
		}
		return nil, err
	}
	if !c.Enabled {
		return nil, ErrFederationNotConfigured
	}
	s.fedMu.Lock()
	defer s.fedMu.Unlock()
	if s.fedCache != nil && s.fedCache.issuer == c.Issuer && s.fedCache.audience == c.Audience {
		return s.fedCache, nil
	}
	provider, err := oidc.NewProvider(ctx, c.Issuer)
	if err != nil {
		return nil, err
	}
	v := &fedVerifier{
		issuer:   c.Issuer,
		audience: c.Audience,
		// oidc.Config.ClientID is the expected audience; verification fails on mismatch.
		verifier: provider.Verifier(&oidc.Config{ClientID: c.Audience}),
	}
	s.fedCache = v
	return v, nil
}

// FederateCILogin verifies a CI OIDC token, matches it to a binding, and mints a
// short-lived scoped service token. All failures return a typed sentinel; the
// API layer collapses them to one indistinguishable response and audits the reason.
func (s *Service) FederateCILogin(ctx context.Context, rawJWT string) (*FederationResult, error) {
	v, err := s.federationVerifierFor(ctx)
	if err != nil {
		return nil, err // ErrFederationNotConfigured or infra error
	}
	idt, err := v.verifier.Verify(ctx, rawJWT)
	if err != nil {
		return nil, ErrFederationVerify
	}
	var raw map[string]any
	if err := idt.Claims(&raw); err != nil {
		return nil, ErrFederationVerify
	}
	claims := stringClaims(raw)
	bindings, err := s.oidcFedBindings.List(ctx)
	if err != nil {
		return nil, err
	}
	b, err := matchFederationBinding(claims, bindings)
	if err != nil {
		return nil, err // ErrFederationNoMatch / ErrFederationAmbiguous
	}
	ttl := time.Duration(b.TTLSeconds) * time.Second
	if ttl <= 0 || ttl > federationMaxTTL {
		ttl = federationDefaultTTL // defensive; config validation should prevent
	}
	token, meta, err := s.MintFederatedToken(ctx, b.Name, b.ScopeKind, b.ScopeID, b.Access, ttl, b.ID)
	if err != nil {
		return nil, err
	}
	return &FederationResult{
		Token: token, Meta: meta, Binding: b.Name,
		Repository: claims["repository"], Subject: claims["sub"],
	}, nil
}
```

- [ ] **Step 5: Run to verify pass**

Run: `go test ./internal/auth/ -run TestFederateCILogin -count=1`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/auth/oidc_federation.go internal/auth/oidc_mockidp_test.go internal/auth/oidc_federation_exchange_test.go
git commit -m "feat(auth): federation verifier + FederateCILogin exchange"
```

---

### Task 9: API — exchange endpoint `POST /v1/auth/oidc/federate`

**Files:**
- Create: `internal/api/oidc_federation_handlers.go`
- Modify: `internal/api/server.go` (route)
- Test: `internal/api/oidc_federation_test.go` (+ mock IdP `signClaims` in the api package)

- [ ] **Step 1: Add `signClaims` to the api-package mock IdP** (`internal/api/oidc_mockidp_api_test.go`) — same body as the auth-package one in Task 8 Step 1.

- [ ] **Step 2: Write the failing test** — `internal/api/oidc_federation_test.go`:

```go
package api

import (
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestOIDCFederateExchange(t *testing.T) {
	ts, srv, _, _, configID := authStackFull(t)
	ctx := t.Context()
	idp := newMockIdP(t, "janus")

	if err := srv.auth.SetFederationConfig(ctx, auth.FederationConfigInput{
		Issuer: idp.srv.URL, Audience: "janus", Enabled: true,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := srv.auth.CreateFederationBinding(ctx, auth.FederationBindingInput{
		Name: "prod", MatchClaims: map[string]string{"repository": "org/app"},
		ScopeKind: "config", ScopeID: configID, Access: "read", TTLSeconds: 900, Enabled: true,
	}); err != nil {
		t.Fatal(err)
	}

	sign := func(claims map[string]any) string { return idp.signClaims(t, claims) }
	base := map[string]any{"iss": idp.srv.URL, "aud": "janus", "sub": "s",
		"exp": time.Now().Add(time.Hour).Unix(), "repository": "org/app"}

	// Happy path → 200 with a janus_svc_ token.
	var ok struct {
		Token     string `json:"token"`
		ExpiresAt string `json:"expires_at"`
	}
	body := `{"token":"` + sign(base) + `"}`
	if code := doJSON(t, "POST", ts.URL+"/v1/auth/oidc/federate", body, &ok); code != 200 {
		t.Fatalf("exchange: %d", code)
	}
	if !strings.HasPrefix(ok.Token, "janus_svc_") || ok.ExpiresAt == "" {
		t.Fatalf("token response: %+v", ok)
	}

	// Wrong audience, no match — both must be the SAME indistinguishable denial.
	badAud := map[string]any{"iss": idp.srv.URL, "aud": "x", "sub": "s",
		"exp": time.Now().Add(time.Hour).Unix(), "repository": "org/app"}
	var e1, e2 errEnvelope
	c1 := doJSON(t, "POST", ts.URL+"/v1/auth/oidc/federate", `{"token":"`+sign(badAud)+`"}`, &e1)
	noRepo := map[string]any{"iss": idp.srv.URL, "aud": "janus", "sub": "s",
		"exp": time.Now().Add(time.Hour).Unix(), "repository": "org/nope"}
	c2 := doJSON(t, "POST", ts.URL+"/v1/auth/oidc/federate", `{"token":"`+sign(noRepo)+`"}`, &e2)
	if c1 != 401 || c2 != 401 || e1.Error.Code != "federation_denied" || e2.Error.Code != e1.Error.Code {
		t.Fatalf("denials not indistinguishable: %d/%s %d/%s", c1, e1.Error.Code, c2, e2.Error.Code)
	}
}
```

> `doJSON(t, method, url, body, out) int` and `errEnvelope` already exist in the api tests (used by C1). `authStackFull` returns `configID` as its 5th value — a real config scope. Add the `auth` import.

- [ ] **Step 3: Run to confirm it fails**

Run: `go test ./internal/api/ -run TestOIDCFederateExchange -count=1`
Expected: FAIL (404 — route missing).

- [ ] **Step 4: Implement the handler** — `internal/api/oidc_federation_handlers.go`:

```go
package api

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/steveokay/janus-secrets/internal/auth"
)

func (s *Server) handleOIDCFederate(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Token string `json:"token"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Token == "" {
		// Indistinguishable from any other exchange failure.
		_ = s.record(r, "auth.federate", "auth/oidc/federate", "denied", "federation_denied", "bad request")
		writeError(w, http.StatusUnauthorized, "federation_denied", "federation exchange failed")
		return
	}
	res, err := s.auth.FederateCILogin(r.Context(), req.Token)
	if err != nil {
		// One response for every reason; the audit detail carries the real cause.
		_ = s.record(r, "auth.federate", "auth/oidc/federate", "denied", "federation_denied", federationReason(err))
		writeError(w, http.StatusUnauthorized, "federation_denied", "federation exchange failed")
		return
	}
	if err := s.record(r, "auth.federate", "auth/oidc/federate", "success", "",
		"binding="+res.Binding+" repository="+res.Repository+" sub="+res.Subject); err != nil {
		writeError(w, http.StatusInternalServerError, CodeInternal, "internal error")
		return
	}
	out := map[string]any{
		"token":  res.Token,
		"scope":  map[string]any{"kind": res.Meta.ScopeKind, "id": res.Meta.ScopeID, "access": res.Meta.Access},
	}
	if res.Meta.ExpiresAt != nil {
		out["expires_at"] = res.Meta.ExpiresAt.UTC().Format("2006-01-02T15:04:05Z07:00")
	}
	writeJSON(w, http.StatusOK, out)
}

// federationReason maps a sentinel to a short audit detail (never returned to the caller).
func federationReason(err error) string {
	switch {
	case errors.Is(err, auth.ErrFederationNotConfigured):
		return "not_configured"
	case errors.Is(err, auth.ErrFederationVerify):
		return "verify_failed"
	case errors.Is(err, auth.ErrFederationNoMatch):
		return "no_match"
	case errors.Is(err, auth.ErrFederationAmbiguous):
		return "ambiguous_match"
	default:
		return "error"
	}
}
```

> Confirm `TokenMeta` exposes `ScopeKind`, `ScopeID`, `Access`, `ExpiresAt` (it does — see `internal/auth/tokens.go` `TokenMeta`). `CodeInternal` exists (used by C1 handlers).

- [ ] **Step 5: Add the route** in `internal/api/server.go`, next to the public `oidc/login` route (inside the `/v1/auth` block, outside `RequireAuth`, with the login limiter). It must be behind `RequireUnsealed` like the other `/v1/*` routes:

```go
			r.With(loginLimiter.middleware).Post("/oidc/federate", s.handleOIDCFederate)
```

- [ ] **Step 6: Run to verify pass**

Run: `go test ./internal/api/ -run TestOIDCFederateExchange -count=1`
Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add internal/api/oidc_federation_handlers.go internal/api/server.go internal/api/oidc_mockidp_api_test.go internal/api/oidc_federation_test.go
git commit -m "feat(api): POST /v1/auth/oidc/federate — CI token exchange"
```

---

### Task 10: API — admin config routes `/v1/sys/oidc/federation`

**Files:**
- Modify: `internal/api/oidc_federation_handlers.go` (config GET/PUT/DELETE)
- Modify: `internal/api/server.go` (routes)
- Test: `internal/api/oidc_federation_config_test.go`

- [ ] **Step 1: Write the failing test** — `internal/api/oidc_federation_config_test.go`:

```go
package api

import "testing"

func TestOIDCFederationConfigRBAC(t *testing.T) {
	ts, srv, adminEmail, adminPassword, _ := authStackFull(t)
	ctx := t.Context()
	owner := login(t, ts.URL, adminEmail, adminPassword)

	vid, vpw, err := srv.auth.CreateUser(ctx, "viewer@example.com")
	if err != nil {
		t.Fatal(err)
	}
	if code := doAuthed(t, "PUT", ts.URL+"/v1/instance/members/"+vid, owner, "", `{"role":"viewer"}`, nil); code != 204 {
		t.Fatalf("grant viewer: %d", code)
	}
	viewer := login(t, ts.URL, "viewer@example.com", vpw)

	body := `{"issuer":"https://token.actions.githubusercontent.com","audience":"janus","enabled":true}`
	if code := doAuthed(t, "PUT", ts.URL+"/v1/sys/oidc/federation", owner, "", body, nil); code != 200 {
		t.Fatalf("owner PUT: %d", code)
	}
	if code := doAuthed(t, "PUT", ts.URL+"/v1/sys/oidc/federation", viewer, "", body, nil); code != 403 {
		t.Fatalf("viewer PUT: want 403, got %d", code)
	}
	var got struct{ Audience string `json:"audience"` }
	if code := doAuthed(t, "GET", ts.URL+"/v1/sys/oidc/federation", owner, "", "", &got); code != 200 || got.Audience != "janus" {
		t.Fatalf("owner GET: %d %+v", code, got)
	}
	if code := doAuthed(t, "DELETE", ts.URL+"/v1/sys/oidc/federation", owner, "", "", nil); code != 204 {
		t.Fatalf("owner DELETE: %d", code)
	}
}
```

- [ ] **Step 2: Run to confirm it fails**

Run: `go test ./internal/api/ -run TestOIDCFederationConfigRBAC -count=1`
Expected: FAIL (404).

- [ ] **Step 3: Implement the config handlers** (append to `internal/api/oidc_federation_handlers.go`):

```go
type fedConfigRequest struct {
	Issuer   string `json:"issuer"`
	Audience string `json:"audience"`
	Enabled  bool   `json:"enabled"`
}

func (s *Server) handleFederationConfigGet(w http.ResponseWriter, r *http.Request) {
	v, err := s.auth.GetFederationConfig(r.Context())
	if err != nil {
		if errors.Is(err, auth.ErrNotFound) {
			writeError(w, http.StatusNotFound, "not_found", "not found")
			return
		}
		s.writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, v)
}

func (s *Server) handleFederationConfigPut(w http.ResponseWriter, r *http.Request) {
	var req fedConfigRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, CodeValidation, "invalid body")
		return
	}
	if err := s.auth.SetFederationConfig(r.Context(), auth.FederationConfigInput{
		Issuer: req.Issuer, Audience: req.Audience, Enabled: req.Enabled,
	}); err != nil {
		if errors.Is(err, auth.ErrValidation) {
			writeError(w, http.StatusBadRequest, CodeValidation, "invalid federation config")
			return
		}
		s.writeServiceError(w, err)
		return
	}
	if err := s.record(r, "oidc.federation.config.write", "oidc/federation", "success", "",
		"issuer="+req.Issuer+" audience="+req.Audience); err != nil {
		writeError(w, http.StatusInternalServerError, CodeInternal, "internal error")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleFederationConfigDelete(w http.ResponseWriter, r *http.Request) {
	if err := s.auth.DeleteFederationConfig(r.Context()); err != nil {
		s.writeServiceError(w, err)
		return
	}
	if err := s.record(r, "oidc.federation.config.delete", "oidc/federation", "success", "", ""); err != nil {
		writeError(w, http.StatusInternalServerError, CodeInternal, "internal error")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
```

- [ ] **Step 4: Add routes** in `internal/api/server.go`, in the `/v1/sys` block next to the C1 `/oidc` routes, each guarded like C1 (`if s.auth != nil && s.authz != nil`):

```go
			r.With(RequireAuth(s.auth), s.requireInstance(authz.OIDCManage, "oidc.federation", "oidc")).Get("/oidc/federation", s.handleFederationConfigGet)
			r.With(RequireAuth(s.auth), s.requireInstance(authz.OIDCManage, "oidc.federation", "oidc")).Put("/oidc/federation", s.handleFederationConfigPut)
			r.With(RequireAuth(s.auth), s.requireInstance(authz.OIDCManage, "oidc.federation", "oidc")).Delete("/oidc/federation", s.handleFederationConfigDelete)
```

- [ ] **Step 5: Run to verify pass**

Run: `go test ./internal/api/ -run TestOIDCFederationConfigRBAC -count=1`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/api/oidc_federation_handlers.go internal/api/server.go internal/api/oidc_federation_config_test.go
git commit -m "feat(api): /v1/sys/oidc/federation config (oidc:manage, audited)"
```

---

### Task 11: API — admin binding routes `/v1/sys/oidc/federation/bindings`

**Files:**
- Modify: `internal/api/oidc_federation_handlers.go` (binding list/create/delete)
- Modify: `internal/api/server.go` (routes)
- Test: `internal/api/oidc_federation_bindings_test.go`

- [ ] **Step 1: Write the failing test** — `internal/api/oidc_federation_bindings_test.go`:

```go
package api

import "testing"

func TestOIDCFederationBindings(t *testing.T) {
	ts, srv, adminEmail, adminPassword, configID := authStackFull(t)
	ctx := t.Context()
	owner := login(t, ts.URL, adminEmail, adminPassword)

	vid, vpw, err := srv.auth.CreateUser(ctx, "v@example.com")
	if err != nil {
		t.Fatal(err)
	}
	doAuthed(t, "PUT", ts.URL+"/v1/instance/members/"+vid, owner, "", `{"role":"viewer"}`, nil)
	viewer := login(t, ts.URL, "v@example.com", vpw)

	// Non-owner denied.
	create := `{"name":"prod","match_claims":{"repository":"org/app"},"scope_kind":"config","scope_id":"` + configID + `","access":"read","ttl_seconds":900,"enabled":true}`
	if code := doAuthed(t, "POST", ts.URL+"/v1/sys/oidc/federation/bindings", viewer, "", create, nil); code != 403 {
		t.Fatalf("viewer POST: want 403, got %d", code)
	}
	// Missing repository → 400.
	bad := `{"name":"bad","match_claims":{"environment":"prod"},"scope_kind":"config","scope_id":"` + configID + `","access":"read","ttl_seconds":900,"enabled":true}`
	if code := doAuthed(t, "POST", ts.URL+"/v1/sys/oidc/federation/bindings", owner, "", bad, nil); code != 400 {
		t.Fatalf("missing repository: want 400, got %d", code)
	}
	// Owner create → 200/201, returns id.
	var made struct{ ID string `json:"id"` }
	code := doAuthed(t, "POST", ts.URL+"/v1/sys/oidc/federation/bindings", owner, "", create, &made)
	if code != 200 || made.ID == "" {
		t.Fatalf("owner POST: %d id=%q", code, made.ID)
	}
	// List shows it.
	var list []map[string]any
	if code := doAuthed(t, "GET", ts.URL+"/v1/sys/oidc/federation/bindings", owner, "", "", &list); code != 200 || len(list) != 1 {
		t.Fatalf("list: %d len=%d", code, len(list))
	}
	// Delete.
	if code := doAuthed(t, "DELETE", ts.URL+"/v1/sys/oidc/federation/bindings/"+made.ID, owner, "", "", nil); code != 204 {
		t.Fatalf("delete: %d", code)
	}
}
```

- [ ] **Step 2: Run to confirm it fails**

Run: `go test ./internal/api/ -run TestOIDCFederationBindings -count=1`
Expected: FAIL (404).

- [ ] **Step 3: Implement the binding handlers** (append to `internal/api/oidc_federation_handlers.go`); use chi's URL param for delete:

```go
import "github.com/go-chi/chi/v5" // add to the file's import block

type fedBindingRequest struct {
	Name        string            `json:"name"`
	MatchClaims map[string]string `json:"match_claims"`
	ScopeKind   string            `json:"scope_kind"`
	ScopeID     string            `json:"scope_id"`
	Access      string            `json:"access"`
	TTLSeconds  int               `json:"ttl_seconds"`
	Enabled     bool              `json:"enabled"`
}

func (s *Server) handleFederationBindingsList(w http.ResponseWriter, r *http.Request) {
	list, err := s.auth.ListFederationBindings(r.Context())
	if err != nil {
		s.writeServiceError(w, err)
		return
	}
	if list == nil {
		list = []auth.FederationBindingView{}
	}
	writeJSON(w, http.StatusOK, list)
}

func (s *Server) handleFederationBindingCreate(w http.ResponseWriter, r *http.Request) {
	var req fedBindingRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, CodeValidation, "invalid body")
		return
	}
	b, err := s.auth.CreateFederationBinding(r.Context(), auth.FederationBindingInput{
		Name: req.Name, MatchClaims: req.MatchClaims, ScopeKind: req.ScopeKind,
		ScopeID: req.ScopeID, Access: req.Access, TTLSeconds: req.TTLSeconds, Enabled: req.Enabled,
	})
	if err != nil {
		if errors.Is(err, auth.ErrValidation) {
			writeError(w, http.StatusBadRequest, CodeValidation, "invalid binding")
			return
		}
		if errors.Is(err, auth.ErrNotFound) {
			writeError(w, http.StatusBadRequest, CodeValidation, "unknown scope")
			return
		}
		s.writeServiceError(w, err)
		return
	}
	if err := s.record(r, "oidc.federation.binding.write", "oidc/federation/"+b.Name, "success", "",
		"scope="+b.ScopeKind+":"+b.ScopeID); err != nil {
		writeError(w, http.StatusInternalServerError, CodeInternal, "internal error")
		return
	}
	writeJSON(w, http.StatusOK, b)
}

func (s *Server) handleFederationBindingDelete(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if err := s.auth.DeleteFederationBinding(r.Context(), id); err != nil {
		if errors.Is(err, auth.ErrNotFound) {
			writeError(w, http.StatusNotFound, "not_found", "not found")
			return
		}
		s.writeServiceError(w, err)
		return
	}
	if err := s.record(r, "oidc.federation.binding.delete", "oidc/federation/"+id, "success", "", ""); err != nil {
		writeError(w, http.StatusInternalServerError, CodeInternal, "internal error")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
```

- [ ] **Step 4: Add routes** in `internal/api/server.go` (same `/v1/sys` block, same guard + `requireInstance`):

```go
			r.With(RequireAuth(s.auth), s.requireInstance(authz.OIDCManage, "oidc.federation", "oidc")).Get("/oidc/federation/bindings", s.handleFederationBindingsList)
			r.With(RequireAuth(s.auth), s.requireInstance(authz.OIDCManage, "oidc.federation", "oidc")).Post("/oidc/federation/bindings", s.handleFederationBindingCreate)
			r.With(RequireAuth(s.auth), s.requireInstance(authz.OIDCManage, "oidc.federation", "oidc")).Delete("/oidc/federation/bindings/{id}", s.handleFederationBindingDelete)
```

> Confirm the chi import path/alias already used in `server.go` (`github.com/go-chi/chi/v5`) and reuse it.

- [ ] **Step 5: Run to verify pass**

Run: `go test ./internal/api/ -run TestOIDCFederationBindings -count=1`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/api/oidc_federation_handlers.go internal/api/server.go internal/api/oidc_federation_bindings_test.go
git commit -m "feat(api): /v1/sys/oidc/federation/bindings CRUD (oidc:manage, audited)"
```

---

### Task 12: Leak test — raw JWT never in logs or audit rows

**Files:**
- Test: `internal/api/oidc_federation_leak_test.go`

- [ ] **Step 1: Write the leak test** (model it on `internal/api/oidc_leak_test.go`'s captured-logger manual stack): boot a manual stack with a captured `slog` logger + real Postgres + shamir 1-of-1, init/unseal, configure federation pointing at the mock IdP, create a binding, drive `POST /v1/auth/oidc/federate` with a **canary marker embedded in a claim value** (e.g. `repository: "org/CANARY-fed-4f2a9e"`) so a real JWT flows through verify + audit, then assert the **raw JWT string** and the canary do not leak where they must not.

```go
package api

import (
	"strings"
	"testing"
	"time"
)

func TestOIDCFederationJWTNeverLeaks(t *testing.T) {
	// Reuse the captured-logger stack builder from oidc_leak_test.go
	// (extract a shared helper if oidc_leak_test.go has one; otherwise mirror it).
	// Pseudocode outline — implement against the real helpers:
	//   ts, logBuf, st := newCapturingStack(t)
	//   idp := newMockIdP(t, "janus")
	//   srv.auth.SetFederationConfig(ctx, {Issuer: idp.URL, Audience: "janus", Enabled: true})
	//   srv.auth.CreateFederationBinding(ctx, {repository: "org/app", scope config, ...})
	//   jwt := idp.signClaims(t, {iss,aud:"janus",repository:"org/app",exp,...})
	//   resp := POST /v1/auth/oidc/federate {"token": jwt}
	//   assert 200
	//   assert !strings.Contains(logBuf.String(), jwt)          // raw JWT not logged
	//   assert audit rows contain no substring of jwt           // via store.NewAuditRepo(st).Iterate
	_ = strings.Contains
	_ = time.Now
	t.Skip("implement against oidc_leak_test.go's captured-stack helpers")
}
```

> Replace the `t.Skip` with the real implementation: copy the stack-construction and audit-scan pattern verbatim from `internal/api/oidc_leak_test.go` (it already builds a captured-logger server, configures OIDC, drives a flow, and scans `audit_events` via `store.NewAuditRepo(st).Iterate`). The assertions: the compact JWT string appears in **no** log line and **no** audit row's `Action/Resource/Detail/Result` fields. (A successful federation audit legitimately records `repository=org/app` and `binding=…`; assert specifically that the **raw JWT** is absent — that is the secret, not the repository name.)

- [ ] **Step 2: Run to verify pass**

Run: `go test ./internal/api/ -run TestOIDCFederationJWTNeverLeaks -count=1`
Expected: PASS.

- [ ] **Step 3: Commit**

```bash
git add internal/api/oidc_federation_leak_test.go
git commit -m "test(oidc): federation JWT never leaks to logs or audit rows"
```

---

### Task 13: Full gate sweep

**Files:** none (verification only; fixes as needed).

- [ ] **Step 1: Run the gates** — all must pass:

```bash
go build ./...
go vet ./...
go test ./... -count=1
go test ./internal/crypto/ -cover -count=1        # expect 100.0% (untouched)
gosec -exclude-dir=internal/crypto/shamir ./...    # expect 0 issues
govulncheck ./...                                  # expect 0 affecting
```

- [ ] **Step 2: Fix any findings.** If `gosec` flags new code, fix it or add a justified `#nosec` mirroring existing ones. If `govulncheck` flags a dependency, bump it (`go get module@fixed && go mod tidy`) and re-run. Report the exact numbers (gosec issues, govulncheck affecting, crypto coverage %).

- [ ] **Step 3: Commit** (only if fixes were made)

```bash
git add -A
git commit -m "chore(oidc): gate-sweep fixes for CI federation"
```

---

### Task 14: Docs + trackers

**Files:**
- Create: `docs/ci-federation.md`
- Modify: `docs/oidc.md` (cross-link the C2 follow-up), `status.md`

- [ ] **Step 1: Write `docs/ci-federation.md`** covering: the purpose (CI auth without stored secrets); the exchange flow (verify → match → mint); the endpoint `POST /v1/auth/oidc/federate` with request/response shapes; provider config (`issuer` default, required `audience`, `enabled`) and binding fields (`match_claims` with mandatory `repository`, `scope_kind`/`scope_id`, `access`, `ttl_seconds` capped at 1h/default 15m, `enabled`); the safety rules (repository-required, exactly-one-match / deny-ambiguous, audience exact-match, TTL cap); the admin API (`/v1/sys/oidc/federation[...]`, `oidc:manage`); the security invariants (raw JWT never logged/audited; indistinguishable denials; short-lived revocable tokens); and a **GitHub Actions usage example** (workflow requesting an OIDC token with `audience: janus` via `actions/github-script` or the `ACTIONS_ID_TOKEN_REQUEST_URL`, then `curl`-ing the exchange endpoint and using the returned `janus_svc_` token with the CLI). Match the tone of `docs/oidc.md`.

- [ ] **Step 2: Cross-link** — in `docs/oidc.md`, update the "Follow-up" section to say C2 is **implemented** and point to `docs/ci-federation.md`.

- [ ] **Step 3: Update `status.md`** — add a "Phase 2 · Sub-project C2 — OIDC CI federation ✅ complete" section (mirror the C1 section format): scope delivered, endpoints, the safety rules, migration `000009`, the `MintFederatedToken`/nullable-`created_by` change, verification results, and note this completes the CLAUDE.md "Federation" Phase-2 item (both human C1 + machine C2).

- [ ] **Step 4: Verify + commit**

```bash
go build ./... && go test ./internal/api/ ./internal/auth/ -count=1
git add docs/ci-federation.md docs/oidc.md status.md
git commit -m "docs(oidc): CI federation (C2) flow, endpoints, GitHub Actions example"
```

---

## Self-review notes (for the executor)

- **Spec coverage:** structured claim conditions (Task 7), one configurable provider (Tasks 2/6/8), per-binding server-capped TTL (Task 6 validation + Task 8 defensive cap), `repository`-required + deny-ambiguous (Tasks 6/7), audience exact-match (Task 8 via `oidc.Config.ClientID`), reuse of service-token issuance with nullable minter (Tasks 3/4), exchange endpoint + indistinguishable denials + audit (Task 9), admin config/bindings gated by `oidc:manage` (Tasks 10/11), leak test (Task 12), gates (Task 13), docs (Task 14), migration `000009` (Task 1).
- **Type consistency:** `FederationConfigInput`/`FederationBindingInput`/`FederationBindingView`/`FederationResult` names and the four `ErrFederation*` sentinels are used identically across auth + api tasks; store models `OIDCFederationConfig`/`OIDCFederationBinding` match between definition (Task 1) and repos (Task 2); `MintFederatedToken` (Task 4) is called only by `FederateCILogin` (Task 8); `CreateFederated` (Task 3) is called only by `MintFederatedToken`.
- **Forward-dependency caveats flagged in-line:** Task 4's test references `CreateFederationBinding` (Task 6) — the note tells the implementer to use the store repo directly to keep the test self-contained if executing strictly in order. `seedConfigScope`/captured-stack helpers are integration seams: the steps say to grep the neighboring C1 `*_test.go` for the real helper names (`newTestService`, `authStackFull`, the leak-test captured stack, `store.NewAuditRepo(...).Iterate`) rather than inventing them.
- **Coordination:** Go-only diff in a worktree parallel to the UI agent. Migration `000009` is the next free number (main tops at `000008` after D). The `service_tokens` change (nullable `created_by` + `federation_binding`) is additive and does not alter existing token behavior. Only `status.md`/`docs/oidc.md` overlap the UI agent's surface (append/section edits). After Task 14 → final adversarial review, then finishing-a-development-branch (PR to main).
