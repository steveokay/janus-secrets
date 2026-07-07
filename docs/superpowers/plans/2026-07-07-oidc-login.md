# OIDC Human Login (sub-project C1) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Let humans sign in through a DB-configured generic OIDC provider (Authorization Code + PKCE), mapped to a pre-provisioned Janus user by stable `(issuer, subject)`, issuing the normal session cookie.

**Architecture:** OIDC is folded onto the existing `auth.Service` (new repos + methods in `internal/auth/oidc.go`), so `api.New(...)` is unchanged. The client_secret is master-key-encrypted at rest. Token verification uses `coreos/go-oidc/v3` + `x/oauth2` (approved CLAUDE.md crypto-lib exception). A mock-IdP httptest harness drives deterministic e2e.

**Tech Stack:** Go 1.26, chi, pgx, testcontainers, `github.com/coreos/go-oidc/v3`, `golang.org/x/oauth2`, `github.com/go-jose/go-jose/v4` (tests).

**Spec:** `docs/superpowers/specs/2026-07-07-oidc-login-design.md`.

**Worktree:** already on branch `worktree-oidc-federation` (isolated; Go-only diff). Run every command from `C:/Users/Athelos/Desktop/claude/janus-secrets/.claude/worktrees/oidc-federation`.

**Conventions to honor everywhere:** parameterized SQL only; `mapError` for driver errors; zeroize secret byte-slices after use; no secret value/credential in logs, errors, or audit rows; `internal/crypto` stays 100% coverage; gates = `go build ./...`, `go vet ./...`, `go test ./... -count=1` (Docker/testcontainers), `gosec -exclude-dir=internal/crypto/shamir ./...` (0 issues), `govulncheck ./...` (0 affecting). Trust `go build`/`go test` over editor diagnostics for new-in-branch symbols.

---

## File structure

| File | Responsibility |
|---|---|
| `go.mod` / `go.sum` | add `go-oidc/v3`, `x/oauth2` (+ `go-jose/v4` test) |
| `internal/crypto/keys.go` | `OIDCClientSecretAAD()` |
| `internal/crypto/keyring.go` | `WrapOIDCClientSecret` / `UnwrapOIDCClientSecret` (arbitrary-length → `Encrypt`/`Decrypt`) |
| `migrations/000007_oidc.up.sql` / `.down.sql` | `oidc_providers`, `oidc_identities`, `oidc_auth_requests` |
| `internal/store/models.go` | `OIDCProvider`, `OIDCIdentity`, `OIDCAuthRequest` structs |
| `internal/store/oidc.go` | `OIDCProviderRepo`, `OIDCIdentityRepo`, `OIDCAuthRequestRepo` |
| `internal/authz/actions.go` | `OIDCManage` action + matrix |
| `internal/auth/service.go` | `Service` gains OIDC repos + verifier cache; `NewService` builds them |
| `internal/auth/sessions.go` | extract `createSession` helper (reused by password login + OIDC) |
| `internal/auth/oidc.go` | provider config CRUD, auth-URL build, callback verify, user resolution, session issue |
| `internal/auth/oidc_mockidp_test.go` | mock IdP (discovery + JWKS + token endpoint) test harness |
| `internal/api/oidc_handlers.go` | `status`/`login`/`callback` + `GET/PUT/DELETE /v1/sys/oidc` |
| `internal/api/server.go` | route registration |
| `docs/oidc.md`, `CLAUDE.md`, `status.md` | docs + crypto-deps carve-out + tracker |

---

### Task 1: Add OIDC dependencies

**Files:**
- Modify: `go.mod`, `go.sum`
- Test: `internal/auth/oidc_deps_test.go` (temporary import smoke; deleted in Task 10 once real tests import the libs — or keep as a build guard)

- [ ] **Step 1: Add the modules**

Run:
```bash
go get github.com/coreos/go-oidc/v3@v3.11.0
go get golang.org/x/oauth2@v0.24.0
go get github.com/go-jose/go-jose/v4@v4.0.4
go mod tidy
```
Expected: `go.mod` gains `github.com/coreos/go-oidc/v3`, `golang.org/x/oauth2`, `github.com/go-jose/go-jose/v4` in the `require` block. (Versions are a floor; `go mod tidy` may adjust patch levels — that is fine as long as `govulncheck` stays clean in Task 14.)

- [ ] **Step 2: Write an import smoke test** — `internal/auth/oidc_deps_test.go`

```go
package auth

import (
	"testing"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"
)

// TestOIDCDepsImportable guards that the OIDC libraries resolve and link.
func TestOIDCDepsImportable(t *testing.T) {
	_ = oidc.Config{}
	_ = oauth2.Config{}
}
```

- [ ] **Step 3: Verify it builds and passes**

Run: `go build ./... && go test ./internal/auth/ -run TestOIDCDepsImportable -count=1`
Expected: PASS.

- [ ] **Step 4: Commit**

```bash
git add go.mod go.sum internal/auth/oidc_deps_test.go
git commit -m "build(oidc): add go-oidc/v3 + x/oauth2 (+ go-jose test dep)"
```

---

### Task 2: Crypto — wrap the OIDC client_secret

**Files:**
- Modify: `internal/crypto/keys.go` (add `OIDCClientSecretAAD`)
- Modify: `internal/crypto/keyring.go` (add wrap/unwrap)
- Test: `internal/crypto/keyring_test.go`

**Why `Encrypt`/`Decrypt`, not `WrapKey`:** `WrapKey`/`UnwrapKey` enforce `len == KeySize` (32). A client_secret is an arbitrary-length string, so we call `Encrypt`/`Decrypt` directly (they seal arbitrary plaintext with the same AES-256-GCM + AAD).

- [ ] **Step 1: Write the failing test** — append to `internal/crypto/keyring_test.go`

```go
func TestOIDCClientSecretWrapRoundTrip(t *testing.T) {
	kr := newTestUnsealedKeyring(t) // existing helper in this package's tests
	secret := []byte("super-secret-oidc-client-value-of-arbitrary-length")

	ct, err := kr.WrapOIDCClientSecret(secret)
	if err != nil {
		t.Fatalf("wrap: %v", err)
	}
	got, err := kr.UnwrapOIDCClientSecret(ct)
	if err != nil {
		t.Fatalf("unwrap: %v", err)
	}
	if string(got) != string(secret) {
		t.Fatalf("round-trip mismatch: got %q", got)
	}

	// Tampered ciphertext fails closed.
	ct.Data[0] ^= 0xff
	if _, err := kr.UnwrapOIDCClientSecret(ct); err == nil {
		t.Fatal("expected error unwrapping tampered ciphertext")
	}
}

func TestOIDCClientSecretWrapSealed(t *testing.T) {
	kr := NewKeyring() // sealed
	if _, err := kr.WrapOIDCClientSecret([]byte("x")); err != ErrSealed {
		t.Fatalf("want ErrSealed, got %v", err)
	}
	if _, err := kr.UnwrapOIDCClientSecret(Ciphertext{Nonce: make([]byte, NonceSize)}); err != ErrSealed {
		t.Fatalf("want ErrSealed, got %v", err)
	}
}
```

> If the existing test helper for an unsealed keyring has a different name, grep `keyring_test.go` for how other tests obtain an unsealed `*Keyring` (e.g. a `newTestUnsealedKeyring` / `testKeyring` helper) and use that exact one.

- [ ] **Step 2: Run to verify failure**

Run: `go test ./internal/crypto/ -run TestOIDCClientSecret -count=1`
Expected: FAIL (undefined `WrapOIDCClientSecret`).

- [ ] **Step 3: Add the AAD** — in `internal/crypto/keys.go`, after `AuthKeyAAD`

```go
// OIDCClientSecretAAD binds a wrapped OIDC client secret to the auth domain so
// it can never be confused with a project KEK, the token-HMAC key, or any other
// wrapped value.
func OIDCClientSecretAAD() []byte {
	return []byte("janus:auth:oidc-client-secret")
}
```

- [ ] **Step 4: Add wrap/unwrap** — in `internal/crypto/keyring.go`, after `UnwrapAuthKey`

```go
// WrapOIDCClientSecret encrypts an OIDC provider client secret (arbitrary
// length) under the master key. Unlike WrapAuthKey it does not require
// 32-byte input, so it calls Encrypt directly rather than WrapKey.
func (k *Keyring) WrapOIDCClientSecret(secret []byte) (Ciphertext, error) {
	k.mu.RLock()
	defer k.mu.RUnlock()
	if k.master == nil {
		return Ciphertext{}, ErrSealed
	}
	return Encrypt(k.master, secret, OIDCClientSecretAAD())
}

// UnwrapOIDCClientSecret decrypts a secret wrapped by WrapOIDCClientSecret.
func (k *Keyring) UnwrapOIDCClientSecret(ct Ciphertext) ([]byte, error) {
	k.mu.RLock()
	defer k.mu.RUnlock()
	if k.master == nil {
		return nil, ErrSealed
	}
	return Decrypt(k.master, ct, OIDCClientSecretAAD())
}
```

- [ ] **Step 5: Verify pass + coverage**

Run: `go test ./internal/crypto/ -run TestOIDCClientSecret -count=1`
Expected: PASS.
Run: `go test ./internal/crypto/ -cover -count=1`
Expected: `coverage: 100.0% of statements` (both new funcs and the AAD are exercised by the tests above; if coverage dropped, add the missing branch — likely the sealed unwrap path, already covered).

- [ ] **Step 6: Commit**

```bash
git add internal/crypto/keys.go internal/crypto/keyring.go internal/crypto/keyring_test.go
git commit -m "feat(crypto): wrap OIDC client secret under the master key"
```

---

### Task 3: Migration 000007 + store models

**Files:**
- Create: `migrations/000007_oidc.up.sql`, `migrations/000007_oidc.down.sql`
- Modify: `internal/store/models.go`

- [ ] **Step 1: Write the up migration** — `migrations/000007_oidc.up.sql`

```sql
CREATE TABLE oidc_providers (
  id                    uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  name                  text NOT NULL UNIQUE,
  issuer                text NOT NULL,
  client_id             text NOT NULL,
  wrapped_client_secret bytea NOT NULL,
  scopes                text[] NOT NULL DEFAULT '{openid,email,profile}',
  redirect_url          text NOT NULL,
  enabled               bool NOT NULL DEFAULT true,
  created_at            timestamptz NOT NULL DEFAULT now(),
  updated_at            timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE oidc_identities (
  id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  user_id       uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  issuer        text NOT NULL,
  subject       text NOT NULL,
  created_at    timestamptz NOT NULL DEFAULT now(),
  last_login_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE (issuer, subject)
);

CREATE TABLE oidc_auth_requests (
  state         text PRIMARY KEY,
  nonce         text NOT NULL,
  pkce_verifier text NOT NULL,
  provider_id   uuid NOT NULL REFERENCES oidc_providers(id) ON DELETE CASCADE,
  created_at    timestamptz NOT NULL DEFAULT now(),
  expires_at    timestamptz NOT NULL
);
```

- [ ] **Step 2: Write the down migration** — `migrations/000007_oidc.down.sql`

```sql
DROP TABLE oidc_auth_requests;
DROP TABLE oidc_identities;
DROP TABLE oidc_providers;
```

- [ ] **Step 3: Add models** — append to `internal/store/models.go`

```go
// OIDCProvider is a configured OIDC identity provider. WrappedClientSecret is
// the master-key-wrapped client secret (never plaintext at rest).
type OIDCProvider struct {
	ID                  string
	Name                string
	Issuer              string
	ClientID            string
	WrappedClientSecret []byte
	Scopes              []string
	RedirectURL         string
	Enabled             bool
	CreatedAt           time.Time
	UpdatedAt           time.Time
}

// OIDCIdentity links a provider subject to a Janus user. (Issuer, Subject) is
// the durable federated identity; email is only used to match on first login.
type OIDCIdentity struct {
	ID          string
	UserID      string
	Issuer      string
	Subject     string
	CreatedAt   time.Time
	LastLoginAt time.Time
}

// OIDCAuthRequest is a short-lived, single-use login state row created at the
// start of an Authorization-Code flow and consumed at the callback.
type OIDCAuthRequest struct {
	State        string
	Nonce        string
	PKCEVerifier string
	ProviderID   string
	CreatedAt    time.Time
	ExpiresAt    time.Time
}
```

> If `internal/store/models.go` does not already import `time`, it does (existing models use `time.Time`). Confirm the build in the next task.

- [ ] **Step 4: Verify build**

Run: `go build ./...`
Expected: builds (SQL isn't compiled; migrations are validated by the store test in Task 4 which applies them against a real Postgres).

- [ ] **Step 5: Commit**

```bash
git add migrations/000007_oidc.up.sql migrations/000007_oidc.down.sql internal/store/models.go
git commit -m "feat(store): migration 000007 + OIDC models (providers, identities, auth requests)"
```

---

### Task 4: `OIDCProviderRepo`

**Files:**
- Create: `internal/store/oidc.go`
- Test: `internal/store/oidc_test.go`

Single-provider model: `Put` upserts on `name`; `Get` returns the one row (LIMIT 1); `Delete` clears it.

- [ ] **Step 1: Write the failing test** — `internal/store/oidc_test.go`

```go
package store

import (
	"testing"
)

func TestOIDCProviderRepo(t *testing.T) {
	st := newTestStore(t) // existing testcontainers helper; grep other *_test.go for its exact name
	r := NewOIDCProviderRepo(st)
	ctx := testCtx(t)

	if _, err := r.Get(ctx); err != ErrNotFound {
		t.Fatalf("empty Get: want ErrNotFound, got %v", err)
	}

	in := OIDCProvider{
		Name: "default", Issuer: "https://issuer.example",
		ClientID: "cid", WrappedClientSecret: []byte{1, 2, 3},
		Scopes: []string{"openid", "email"}, RedirectURL: "https://app/cb", Enabled: true,
	}
	if err := r.Put(ctx, in); err != nil {
		t.Fatalf("put: %v", err)
	}
	got, err := r.Get(ctx)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Issuer != in.Issuer || got.ClientID != "cid" || !got.Enabled ||
		len(got.Scopes) != 2 || string(got.WrappedClientSecret) != string([]byte{1, 2, 3}) {
		t.Fatalf("mismatch: %+v", got)
	}

	// Upsert on name replaces.
	in.Issuer = "https://issuer2.example"
	in.Enabled = false
	if err := r.Put(ctx, in); err != nil {
		t.Fatalf("re-put: %v", err)
	}
	got, _ = r.Get(ctx)
	if got.Issuer != "https://issuer2.example" || got.Enabled {
		t.Fatalf("upsert not applied: %+v", got)
	}

	if err := r.Delete(ctx); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := r.Get(ctx); err != ErrNotFound {
		t.Fatalf("post-delete Get: want ErrNotFound, got %v", err)
	}
}
```

> Grep an existing `internal/store/*_test.go` (e.g. `transit_test.go`) for the real store/testcontainers helper names and reuse them verbatim (`newTestStore`/`testCtx` are placeholders for whatever the suite already uses).

- [ ] **Step 2: Run to verify failure**

Run: `go test ./internal/store/ -run TestOIDCProviderRepo -count=1`
Expected: FAIL (undefined `NewOIDCProviderRepo`).

- [ ] **Step 3: Implement** — `internal/store/oidc.go`

```go
package store

import (
	"context"

	"github.com/jackc/pgx/v5"
)

const oidcProviderCols = `id::text, name, issuer, client_id, wrapped_client_secret,
	scopes, redirect_url, enabled, created_at, updated_at`

// OIDCProviderRepo persists the (single) configured OIDC provider. It is
// crypto-blind: wrapped_client_secret is stored and returned as opaque bytes.
type OIDCProviderRepo struct{ s *Store }

func NewOIDCProviderRepo(s *Store) *OIDCProviderRepo { return &OIDCProviderRepo{s: s} }

// Put upserts the provider keyed by name.
func (r *OIDCProviderRepo) Put(ctx context.Context, p OIDCProvider) error {
	_, err := r.s.pool.Exec(ctx,
		`INSERT INTO oidc_providers
		   (name, issuer, client_id, wrapped_client_secret, scopes, redirect_url, enabled, updated_at)
		 VALUES ($1,$2,$3,$4,$5,$6,$7, now())
		 ON CONFLICT (name) DO UPDATE SET
		   issuer=$2, client_id=$3, wrapped_client_secret=$4, scopes=$5,
		   redirect_url=$6, enabled=$7, updated_at=now()`,
		p.Name, p.Issuer, p.ClientID, p.WrappedClientSecret, p.Scopes, p.RedirectURL, p.Enabled)
	return mapError(err)
}

// Get returns the single configured provider (LIMIT 1), or ErrNotFound.
func (r *OIDCProviderRepo) Get(ctx context.Context) (*OIDCProvider, error) {
	row := r.s.pool.QueryRow(ctx,
		`SELECT `+oidcProviderCols+` FROM oidc_providers ORDER BY created_at LIMIT 1`)
	return scanOIDCProvider(row)
}

// Delete removes all provider rows.
func (r *OIDCProviderRepo) Delete(ctx context.Context) error {
	_, err := r.s.pool.Exec(ctx, `DELETE FROM oidc_providers`)
	return mapError(err)
}

func scanOIDCProvider(row pgx.Row) (*OIDCProvider, error) {
	var p OIDCProvider
	if err := row.Scan(&p.ID, &p.Name, &p.Issuer, &p.ClientID, &p.WrappedClientSecret,
		&p.Scopes, &p.RedirectURL, &p.Enabled, &p.CreatedAt, &p.UpdatedAt); err != nil {
		return nil, mapError(err)
	}
	return &p, nil
}
```

- [ ] **Step 4: Verify pass**

Run: `go test ./internal/store/ -run TestOIDCProviderRepo -count=1`
Expected: PASS (migration 000007 applies automatically via the store test harness).

- [ ] **Step 5: Commit**

```bash
git add internal/store/oidc.go internal/store/oidc_test.go
git commit -m "feat(store): OIDCProviderRepo (upsert/get/delete)"
```

---

### Task 5: `OIDCIdentityRepo`

**Files:**
- Modify: `internal/store/oidc.go`
- Test: `internal/store/oidc_test.go`

- [ ] **Step 1: Write the failing test** — append to `internal/store/oidc_test.go`

```go
func TestOIDCIdentityRepo(t *testing.T) {
	st := newTestStore(t)
	ctx := testCtx(t)
	users := NewUserRepo(st)
	u, err := users.Create(ctx, "oidc-user@example.com", nil) // NULL password (OIDC-only)
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	r := NewOIDCIdentityRepo(st)

	if _, err := r.GetBySubject(ctx, "https://iss", "sub-123"); err != ErrNotFound {
		t.Fatalf("empty: want ErrNotFound, got %v", err)
	}
	id, err := r.Create(ctx, u.ID, "https://iss", "sub-123")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	got, err := r.GetBySubject(ctx, "https://iss", "sub-123")
	if err != nil || got.UserID != u.ID {
		t.Fatalf("get: %+v err=%v", got, err)
	}
	if err := r.TouchLastLogin(ctx, id.ID); err != nil {
		t.Fatalf("touch: %v", err)
	}
	// (issuer, subject) is unique.
	if _, err := r.Create(ctx, u.ID, "https://iss", "sub-123"); err != ErrAlreadyExists {
		t.Fatalf("dup: want ErrAlreadyExists, got %v", err)
	}
}
```

> Confirm `UserRepo.Create`'s signature accepts a `*string` password (it does: `Create(ctx, email string, passwordHash *string)`); pass `nil` for OIDC-only users.

- [ ] **Step 2: Run to verify failure**

Run: `go test ./internal/store/ -run TestOIDCIdentityRepo -count=1`
Expected: FAIL (undefined `NewOIDCIdentityRepo`).

- [ ] **Step 3: Implement** — append to `internal/store/oidc.go`

```go
const oidcIdentityCols = `id::text, user_id::text, issuer, subject, created_at, last_login_at`

// OIDCIdentityRepo links provider subjects to Janus users.
type OIDCIdentityRepo struct{ s *Store }

func NewOIDCIdentityRepo(s *Store) *OIDCIdentityRepo { return &OIDCIdentityRepo{s: s} }

// GetBySubject returns the identity for (issuer, subject), or ErrNotFound.
func (r *OIDCIdentityRepo) GetBySubject(ctx context.Context, issuer, subject string) (*OIDCIdentity, error) {
	row := r.s.pool.QueryRow(ctx,
		`SELECT `+oidcIdentityCols+` FROM oidc_identities WHERE issuer=$1 AND subject=$2`, issuer, subject)
	return scanOIDCIdentity(row)
}

// Create links a subject to a user. Duplicate (issuer, subject) → ErrAlreadyExists.
func (r *OIDCIdentityRepo) Create(ctx context.Context, userID, issuer, subject string) (*OIDCIdentity, error) {
	row := r.s.pool.QueryRow(ctx,
		`INSERT INTO oidc_identities (user_id, issuer, subject)
		 VALUES ($1::uuid, $2, $3) RETURNING `+oidcIdentityCols,
		userID, issuer, subject)
	return scanOIDCIdentity(row)
}

// TouchLastLogin bumps last_login_at.
func (r *OIDCIdentityRepo) TouchLastLogin(ctx context.Context, id string) error {
	_, err := r.s.pool.Exec(ctx, `UPDATE oidc_identities SET last_login_at=now() WHERE id=$1::uuid`, id)
	return mapError(err)
}

func scanOIDCIdentity(row pgx.Row) (*OIDCIdentity, error) {
	var i OIDCIdentity
	if err := row.Scan(&i.ID, &i.UserID, &i.Issuer, &i.Subject, &i.CreatedAt, &i.LastLoginAt); err != nil {
		return nil, mapError(err)
	}
	return &i, nil
}
```

- [ ] **Step 4: Verify pass**

Run: `go test ./internal/store/ -run TestOIDCIdentityRepo -count=1`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/store/oidc.go internal/store/oidc_test.go
git commit -m "feat(store): OIDCIdentityRepo (link by issuer+subject)"
```

---

### Task 6: `OIDCAuthRequestRepo`

**Files:**
- Modify: `internal/store/oidc.go`
- Test: `internal/store/oidc_test.go`

`Consume` is a single-use SELECT-then-DELETE inside a transaction, returning `ErrNotFound` for missing/expired rows.

- [ ] **Step 1: Write the failing test** — append to `internal/store/oidc_test.go`

```go
func TestOIDCAuthRequestRepo(t *testing.T) {
	st := newTestStore(t)
	ctx := testCtx(t)
	pr := NewOIDCProviderRepo(st)
	if err := pr.Put(ctx, OIDCProvider{Name: "default", Issuer: "i", ClientID: "c",
		WrappedClientSecret: []byte{1}, Scopes: []string{"openid"}, RedirectURL: "r", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	prov, _ := pr.Get(ctx)
	r := NewOIDCAuthRequestRepo(st)

	future := time.Now().Add(10 * time.Minute)
	if err := r.Create(ctx, "state-a", "nonce-a", "verifier-a", prov.ID, future); err != nil {
		t.Fatalf("create: %v", err)
	}
	got, err := r.Consume(ctx, "state-a")
	if err != nil || got.Nonce != "nonce-a" || got.PKCEVerifier != "verifier-a" {
		t.Fatalf("consume: %+v err=%v", got, err)
	}
	// Single-use: second consume misses.
	if _, err := r.Consume(ctx, "state-a"); err != ErrNotFound {
		t.Fatalf("re-consume: want ErrNotFound, got %v", err)
	}
	// Expired rows are not returned and get swept.
	past := time.Now().Add(-1 * time.Minute)
	_ = r.Create(ctx, "state-old", "n", "v", prov.ID, past)
	if _, err := r.Consume(ctx, "state-old"); err != ErrNotFound {
		t.Fatalf("expired consume: want ErrNotFound, got %v", err)
	}
	if err := r.DeleteExpired(ctx); err != nil {
		t.Fatalf("sweep: %v", err)
	}
}
```

> Add `import "time"` to the test file if not present.

- [ ] **Step 2: Run to verify failure**

Run: `go test ./internal/store/ -run TestOIDCAuthRequestRepo -count=1`
Expected: FAIL (undefined `NewOIDCAuthRequestRepo`).

- [ ] **Step 3: Implement** — append to `internal/store/oidc.go`

Add `"time"` to the file's imports, then:

```go
// OIDCAuthRequestRepo persists single-use login state.
type OIDCAuthRequestRepo struct{ s *Store }

func NewOIDCAuthRequestRepo(s *Store) *OIDCAuthRequestRepo { return &OIDCAuthRequestRepo{s: s} }

// Create inserts a login-state row.
func (r *OIDCAuthRequestRepo) Create(ctx context.Context, state, nonce, verifier, providerID string, expiresAt time.Time) error {
	_, err := r.s.pool.Exec(ctx,
		`INSERT INTO oidc_auth_requests (state, nonce, pkce_verifier, provider_id, expires_at)
		 VALUES ($1,$2,$3,$4::uuid,$5)`,
		state, nonce, verifier, providerID, expiresAt)
	return mapError(err)
}

// Consume atomically returns and deletes the row for state, but only if it has
// not expired. Missing or expired → ErrNotFound (single-use, replay-safe).
func (r *OIDCAuthRequestRepo) Consume(ctx context.Context, state string) (*OIDCAuthRequest, error) {
	var a OIDCAuthRequest
	err := r.s.withTx(ctx, func(tx pgx.Tx) error {
		row := tx.QueryRow(ctx,
			`DELETE FROM oidc_auth_requests
			 WHERE state=$1 AND expires_at > now()
			 RETURNING state, nonce, pkce_verifier, provider_id::text, created_at, expires_at`, state)
		return row.Scan(&a.State, &a.Nonce, &a.PKCEVerifier, &a.ProviderID, &a.CreatedAt, &a.ExpiresAt)
	})
	if err != nil {
		return nil, mapError(err)
	}
	return &a, nil
}

// DeleteExpired removes stale rows (called at boot).
func (r *OIDCAuthRequestRepo) DeleteExpired(ctx context.Context) error {
	_, err := r.s.pool.Exec(ctx, `DELETE FROM oidc_auth_requests WHERE expires_at <= now()`)
	return mapError(err)
}
```

> `withTx` is the same helper `TransitRepo.Create` uses. Confirm its exact name in `internal/store/store.go` and match it.

- [ ] **Step 4: Verify pass**

Run: `go test ./internal/store/ -run TestOIDCAuthRequestRepo -count=1`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/store/oidc.go internal/store/oidc_test.go
git commit -m "feat(store): OIDCAuthRequestRepo (single-use, expiring login state)"
```

---

### Task 7: authz `OIDCManage` action

**Files:**
- Modify: `internal/authz/actions.go`
- Test: `internal/authz/*_test.go` (add to the existing matrix test file)

- [ ] **Step 1: Write the failing test** — add to the authz matrix test (grep for an existing `TestCan`/matrix test and mirror its style)

```go
func TestOIDCManageMatrix(t *testing.T) {
	eng := New(nil) // engine with no bindings needed for role→action checks; mirror existing matrix tests' construction
	cases := []struct {
		role Role
		want bool
	}{
		{RoleViewer, false},
		{RoleDeveloper, false},
		{RoleAdmin, true},
		{RoleOwner, true},
	}
	for _, c := range cases {
		if got := roleActions[c.role][OIDCManage]; got != c.want {
			t.Fatalf("role %s OIDCManage: got %v want %v", c.role, got, c.want)
		}
	}
}
```

> If the matrix is not exposed via `roleActions` directly to tests, mirror however the existing action-matrix test asserts membership (e.g. via `EffectiveRole`/`Can` against an instance binding). Match the existing test's mechanism exactly.

- [ ] **Step 2: Run to verify failure**

Run: `go test ./internal/authz/ -run TestOIDCManageMatrix -count=1`
Expected: FAIL (undefined `OIDCManage`).

- [ ] **Step 3: Implement** — in `internal/authz/actions.go`

Add the constant to the instance-scoped group:
```go
	OIDCManage Action = "oidc:manage" // instance-scoped
```
Add it to `adminActions` (so admin + owner inherit it):
```go
	adminActions = union(developerActions, setOf(
		ConfigDelete, EnvCreate, EnvDelete, ProjectCreate, MemberManage,
		TokenRead, TokenMint, TokenRevoke, UserManage, AuditRead, SysSeal, TransitManage, OIDCManage))
```

- [ ] **Step 4: Verify pass + coverage**

Run: `go test ./internal/authz/ -count=1 -cover`
Expected: PASS; `internal/authz` stays 100%.

- [ ] **Step 5: Commit**

```bash
git add internal/authz/actions.go internal/authz/*_test.go
git commit -m "feat(authz): oidc:manage instance action (admin+owner)"
```

---

### Task 8: Extract `createSession` helper

**Files:**
- Modify: `internal/auth/sessions.go`
- Test: `internal/auth/sessions_test.go`

Reused by password `Login` and OIDC callback. Behavior-preserving refactor.

- [ ] **Step 1: Write the failing test** — append to `internal/auth/sessions_test.go`

```go
func TestCreateSessionForUser(t *testing.T) {
	svc, ctx, userID := newLoginHarness(t) // grep sessions_test.go for the existing unsealed-service+user harness and reuse it
	cookie, err := svc.createSession(ctx, userID)
	if err != nil {
		t.Fatalf("createSession: %v", err)
	}
	p, err := svc.VerifySession(ctx, cookie)
	if err != nil || p.Kind != KindUser || p.ID != userID {
		t.Fatalf("verify minted session: p=%+v err=%v", p, err)
	}
}
```

> Reuse the existing test harness that yields an unsealed `*Service` + a created user (whatever `sessions_test.go`/`harness_test.go` already provides). `createSession` is unexported, so this test lives in `package auth`.

- [ ] **Step 2: Run to verify failure**

Run: `go test ./internal/auth/ -run TestCreateSessionForUser -count=1`
Expected: FAIL (undefined `createSession`).

- [ ] **Step 3: Refactor** — in `internal/auth/sessions.go`

Add the helper:
```go
// createSession mints a session cookie for an already-authenticated user
// (password verified, or OIDC identity resolved). The caller owns the auth
// decision; this only issues the credential.
func (s *Service) createSession(ctx context.Context, userID string) (string, error) {
	cookie, err := randToken(32)
	if err != nil {
		return "", err
	}
	key, err := s.hmacKey(ctx)
	if err != nil {
		return "", err
	}
	defer zeroize(key)
	if _, err := s.sessions.Create(ctx, userID, mac(key, cookie), time.Now().Add(sessionTTL)); err != nil {
		return "", err
	}
	return cookie, nil
}
```

Replace the tail of `Login` (the `cookie, err := randToken(32)` block through the `sessions.Create` call) with:
```go
	return s.createSession(ctx, u.ID)
```
so `Login`'s final lines become the rehash best-effort followed by `return s.createSession(ctx, u.ID)`.

- [ ] **Step 4: Verify pass (incl. existing login tests)**

Run: `go test ./internal/auth/ -run 'TestCreateSessionForUser|Login' -count=1`
Expected: PASS (existing login lifecycle unaffected).

- [ ] **Step 5: Commit**

```bash
git add internal/auth/sessions.go internal/auth/sessions_test.go
git commit -m "refactor(auth): extract createSession helper for reuse by OIDC"
```

---

### Task 9: OIDC provider config on `auth.Service`

**Files:**
- Modify: `internal/auth/service.go` (Service fields + NewService + errors as needed)
- Create: `internal/auth/oidc.go` (config CRUD portion)
- Test: `internal/auth/oidc_config_test.go`

- [ ] **Step 1: Write the failing test** — `internal/auth/oidc_config_test.go`

```go
package auth

import "testing"

func TestOIDCProviderConfigRoundTrip(t *testing.T) {
	svc, ctx := newUnsealedService(t) // grep harness_test.go for the existing unsealed *Service constructor

	if _, err := svc.GetOIDCProvider(ctx); err != ErrNotFound {
		t.Fatalf("empty: want ErrNotFound, got %v", err)
	}
	err := svc.SetOIDCProvider(ctx, OIDCProviderInput{
		Name: "default", Issuer: "https://issuer.example", ClientID: "cid",
		ClientSecret: "the-secret", Scopes: []string{"openid", "email"},
		RedirectURL: "https://app/cb", Enabled: true,
	})
	if err != nil {
		t.Fatalf("set: %v", err)
	}
	view, err := svc.GetOIDCProvider(ctx)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if view.Issuer != "https://issuer.example" || view.ClientID != "cid" || !view.SecretSet || !view.Enabled {
		t.Fatalf("view mismatch: %+v", view)
	}
	// The plaintext secret must never surface in the view.
	if got := view.ClientID + view.Issuer + view.RedirectURL; got == "the-secret" {
		t.Fatal("secret leaked into view")
	}
	if err := svc.DeleteOIDCProvider(ctx); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := svc.GetOIDCProvider(ctx); err != ErrNotFound {
		t.Fatalf("post-delete: want ErrNotFound, got %v", err)
	}
}
```

- [ ] **Step 2: Run to verify failure**

Run: `go test ./internal/auth/ -run TestOIDCProviderConfigRoundTrip -count=1`
Expected: FAIL (undefined types/methods).

- [ ] **Step 3: Extend `Service`** — in `internal/auth/service.go`

Add fields to the `Service` struct:
```go
	oidcProviders  *store.OIDCProviderRepo
	oidcIdentities *store.OIDCIdentityRepo
	oidcAuthReqs   *store.OIDCAuthRequestRepo
```
In `NewService`, build them:
```go
		oidcProviders:  store.NewOIDCProviderRepo(st),
		oidcIdentities: store.NewOIDCIdentityRepo(st),
		oidcAuthReqs:   store.NewOIDCAuthRequestRepo(st),
```

- [ ] **Step 4: Implement config CRUD** — `internal/auth/oidc.go`

```go
package auth

import (
	"context"
	"errors"

	"github.com/steveokay/janus-secrets/internal/crypto"
	"github.com/steveokay/janus-secrets/internal/store"
)

// OIDCProviderInput is the admin-supplied provider configuration.
type OIDCProviderInput struct {
	Name         string
	Issuer       string
	ClientID     string
	ClientSecret string // plaintext in; wrapped before storage
	Scopes       []string
	RedirectURL  string
	Enabled      bool
}

// OIDCProviderView is the non-secret provider config for admin display.
type OIDCProviderView struct {
	Name        string   `json:"name"`
	Issuer      string   `json:"issuer"`
	ClientID    string   `json:"client_id"`
	Scopes      []string `json:"scopes"`
	RedirectURL string   `json:"redirect_url"`
	Enabled     bool     `json:"enabled"`
	SecretSet   bool     `json:"secret_set"`
}

// SetOIDCProvider wraps the client secret under the master key and upserts the
// provider. Requires an unsealed keyring (surfaces crypto.ErrSealed otherwise).
func (s *Service) SetOIDCProvider(ctx context.Context, in OIDCProviderInput) error {
	if in.Name == "" {
		in.Name = "default"
	}
	if in.Issuer == "" || in.ClientID == "" || in.ClientSecret == "" || in.RedirectURL == "" {
		return ErrValidation
	}
	if len(in.Scopes) == 0 {
		in.Scopes = []string{"openid", "email", "profile"}
	}
	secret := []byte(in.ClientSecret)
	ct, err := s.keyring.WrapOIDCClientSecret(secret)
	zeroize(secret)
	if err != nil {
		return err
	}
	err = s.oidcProviders.Put(ctx, store.OIDCProvider{
		Name: in.Name, Issuer: in.Issuer, ClientID: in.ClientID,
		WrappedClientSecret: ct.Marshal(), Scopes: in.Scopes,
		RedirectURL: in.RedirectURL, Enabled: in.Enabled,
	})
	if err != nil {
		return err
	}
	s.invalidateOIDCVerifier() // defined in Task 10
	return nil
}

// GetOIDCProvider returns the non-secret provider view, or ErrNotFound.
func (s *Service) GetOIDCProvider(ctx context.Context) (*OIDCProviderView, error) {
	p, err := s.oidcProviders.Get(ctx)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &OIDCProviderView{
		Name: p.Name, Issuer: p.Issuer, ClientID: p.ClientID, Scopes: p.Scopes,
		RedirectURL: p.RedirectURL, Enabled: p.Enabled,
		SecretSet: len(p.WrappedClientSecret) > 0,
	}, nil
}

// DeleteOIDCProvider removes the provider.
func (s *Service) DeleteOIDCProvider(ctx context.Context) error {
	if err := s.oidcProviders.Delete(ctx); err != nil {
		return err
	}
	s.invalidateOIDCVerifier()
	return nil
}

// unwrapClientSecret returns the plaintext secret for a stored provider. The
// caller must zeroize the result.
func (s *Service) unwrapClientSecret(p *store.OIDCProvider) ([]byte, error) {
	ct, err := crypto.ParseCiphertext(p.WrappedClientSecret)
	if err != nil {
		return nil, err
	}
	return s.keyring.UnwrapOIDCClientSecret(ct)
}
```

> `ErrValidation` and `ErrNotFound` already exist in `internal/auth/errors.go` (used by `CreateInitialAdmin`/`hmacKey`). Reuse them. `invalidateOIDCVerifier` is added in Task 10 — to keep this task compiling on its own, add a temporary no-op `func (s *Service) invalidateOIDCVerifier() {}` in `oidc.go` now and replace its body in Task 10.

- [ ] **Step 5: Verify pass**

Run: `go test ./internal/auth/ -run TestOIDCProviderConfigRoundTrip -count=1`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/auth/service.go internal/auth/oidc.go internal/auth/oidc_config_test.go
git commit -m "feat(auth): OIDC provider config CRUD with master-key-wrapped secret"
```

---

### Task 10: Mock IdP harness + OIDC verifier/exchange core

**Files:**
- Create: `internal/auth/oidc_mockidp_test.go` (test harness)
- Modify: `internal/auth/oidc.go` (verifier cache, `StartOIDCLogin`, token exchange + verify)
- Modify: `internal/auth/service.go` (verifier-cache fields)
- Test: `internal/auth/oidc_flow_test.go`

This task builds the mock IdP and the flow up to *verified claims* (resolution + session issuance is Task 11).

- [ ] **Step 1: Write the mock IdP harness** — `internal/auth/oidc_mockidp_test.go`

```go
package auth

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	jose "github.com/go-jose/go-jose/v4"
	"github.com/go-jose/go-jose/v4/jwt"
)

// mockIdP is a minimal OIDC provider for tests: discovery, JWKS, and a token
// endpoint that mints a signed ID token for scripted claims.
type mockIdP struct {
	srv      *httptest.Server
	key      *rsa.PrivateKey
	keyID    string
	clientID string
	// scripted claims for the next token exchange:
	sub, email string
	emailVer   bool
	nonce      string // echoed into the id_token
}

func newMockIdP(t *testing.T, clientID string) *mockIdP {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	m := &mockIdP{key: key, keyID: "test-key-1", clientID: clientID}
	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, r *http.Request) {
		writeJSONT(w, map[string]any{
			"issuer":                 m.srv.URL,
			"authorization_endpoint": m.srv.URL + "/authorize",
			"token_endpoint":         m.srv.URL + "/token",
			"jwks_uri":               m.srv.URL + "/jwks",
			"id_token_signing_alg_values_supported": []string{"RS256"},
		})
	})
	mux.HandleFunc("/jwks", func(w http.ResponseWriter, r *http.Request) {
		writeJSONT(w, jose.JSONWebKeySet{Keys: []jose.JSONWebKey{{
			Key: key.Public(), KeyID: m.keyID, Algorithm: "RS256", Use: "sig",
		}}})
	})
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		writeJSONT(w, map[string]any{
			"access_token": "at", "token_type": "Bearer",
			"id_token": m.signIDToken(t),
		})
	})
	m.srv = httptest.NewServer(mux)
	t.Cleanup(m.srv.Close)
	return m
}

func (m *mockIdP) signIDToken(t *testing.T) string {
	t.Helper()
	sig, err := jose.NewSigner(
		jose.SigningKey{Algorithm: jose.RS256, Key: m.key},
		(&jose.SignerOptions{}).WithType("JWT").WithHeader("kid", m.keyID))
	if err != nil {
		t.Fatal(err)
	}
	claims := map[string]any{
		"iss": m.srv.URL, "sub": m.sub, "aud": m.clientID,
		"exp": time.Now().Add(time.Hour).Unix(), "iat": time.Now().Unix(),
		"email": m.email, "email_verified": m.emailVer, "nonce": m.nonce,
	}
	raw, err := jwt.Signed(sig).Claims(claims).Serialize()
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func writeJSONT(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

// oidcTestContext returns a context usable for go-oidc discovery against the
// mock (its issuer == its own URL, so no insecure override is needed).
func oidcTestContext() context.Context { return context.Background() }
```

> The `go-jose/v4` signing/JWKS API is stable; if a symbol differs in the resolved patch version, adjust the call (e.g. `jwt.Signed(sig).Claims(claims).Serialize()` vs `.CompactSerialize()`) — the intent is: RS256-sign a JWT with a `kid` header matching the JWKS. Verify with `go doc github.com/go-jose/go-jose/v4/jwt`.

- [ ] **Step 2: Write the failing flow test** — `internal/auth/oidc_flow_test.go`

```go
package auth

import (
	"strings"
	"testing"
)

func TestOIDCStartAndVerify(t *testing.T) {
	svc, ctx := newUnsealedService(t)
	idp := newMockIdP(t, "test-client")
	if err := svc.SetOIDCProvider(ctx, OIDCProviderInput{
		Name: "default", Issuer: idp.srv.URL, ClientID: "test-client",
		ClientSecret: "shh", Scopes: []string{"openid", "email"},
		RedirectURL: "https://app/cb", Enabled: true,
	}); err != nil {
		t.Fatal(err)
	}

	// Start: returns an authorize URL carrying our state + nonce + PKCE challenge,
	// and persists the auth-request row.
	authURL, err := svc.StartOIDCLogin(ctx)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	if !strings.HasPrefix(authURL, idp.srv.URL+"/authorize") ||
		!strings.Contains(authURL, "code_challenge=") || !strings.Contains(authURL, "state=") {
		t.Fatalf("authURL missing params: %s", authURL)
	}
	state := extractQuery(t, authURL, "state")
	nonce := extractQuery(t, authURL, "nonce")

	// The IdP will mint a token for this subject/nonce on exchange.
	idp.sub, idp.email, idp.emailVer, idp.nonce = "sub-1", "who@example.com", true, nonce

	claims, err := svc.verifyOIDCCallback(ctx, state, "any-code")
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if claims.Subject != "sub-1" || claims.Email != "who@example.com" || !claims.EmailVerified {
		t.Fatalf("claims mismatch: %+v", claims)
	}

	// Replayed state is rejected (single-use).
	if _, err := svc.verifyOIDCCallback(ctx, state, "any-code"); err == nil {
		t.Fatal("expected replayed state to be rejected")
	}
}
```

Add the small helper `extractQuery` at the bottom of this file:
```go
func extractQuery(t *testing.T, rawurl, key string) string {
	t.Helper()
	u, err := url.Parse(rawurl)
	if err != nil {
		t.Fatal(err)
	}
	return u.Query().Get(key)
}
```
(import `net/url`).

- [ ] **Step 3: Run to verify failure**

Run: `go test ./internal/auth/ -run TestOIDCStartAndVerify -count=1`
Expected: FAIL (undefined `StartOIDCLogin` / `verifyOIDCCallback`).

- [ ] **Step 4: Implement the verifier core** — in `internal/auth/service.go` add cache fields to `Service`:

```go
	oidcMu    sync.Mutex
	oidcCache *oidcVerifier // nil until first use; keyed implicitly by the single provider
```
(add `"sync"` to imports).

In `internal/auth/oidc.go` add:

```go
import (
	// add to the existing import block:
	"crypto/rand"
	"encoding/base64"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"
)

const oidcAuthRequestTTL = 10 * time.Minute

// oidcVerifier bundles the go-oidc provider/verifier and the oauth2 config for
// the currently-configured provider.
type oidcVerifier struct {
	issuer      string
	clientID    string
	verifier    *oidc.IDTokenVerifier
	oauth2      *oauth2.Config
}

// OIDCClaims are the verified ID-token claims we consume.
type OIDCClaims struct {
	Issuer        string
	Subject       string
	Email         string
	EmailVerified bool
}

// invalidateOIDCVerifier drops the cached verifier (config changed).
func (s *Service) invalidateOIDCVerifier() {
	s.oidcMu.Lock()
	s.oidcCache = nil
	s.oidcMu.Unlock()
}

// oidcVerifierFor builds (or returns cached) the verifier for the enabled
// provider. Returns ErrNotFound if no enabled provider is configured.
func (s *Service) oidcVerifierFor(ctx context.Context) (*oidcVerifier, error) {
	p, err := s.oidcProviders.Get(ctx)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	if !p.Enabled {
		return nil, ErrNotFound
	}
	s.oidcMu.Lock()
	defer s.oidcMu.Unlock()
	if s.oidcCache != nil && s.oidcCache.issuer == p.Issuer && s.oidcCache.clientID == p.ClientID {
		return s.oidcCache, nil
	}
	secret, err := s.unwrapClientSecret(p)
	if err != nil {
		return nil, err
	}
	provider, err := oidc.NewProvider(ctx, p.Issuer) // fetches discovery + JWKS
	if err != nil {
		zeroize(secret)
		return nil, err
	}
	v := &oidcVerifier{
		issuer:   p.Issuer,
		clientID: p.ClientID,
		verifier: provider.Verifier(&oidc.Config{ClientID: p.ClientID}),
		oauth2: &oauth2.Config{
			ClientID:     p.ClientID,
			ClientSecret: string(secret),
			Endpoint:     provider.Endpoint(),
			RedirectURL:  p.RedirectURL,
			Scopes:       p.Scopes,
		},
	}
	zeroize(secret)
	s.oidcCache = v
	return v, nil
}

// StartOIDCLogin persists a login-state row and returns the provider authorize
// URL (with state, nonce, and a PKCE S256 challenge). ErrNotFound if OIDC is
// not configured/enabled.
func (s *Service) StartOIDCLogin(ctx context.Context) (string, error) {
	v, err := s.oidcVerifierFor(ctx)
	if err != nil {
		return "", err
	}
	p, err := s.oidcProviders.Get(ctx)
	if err != nil {
		return "", err
	}
	state, err := randToken(32)
	if err != nil {
		return "", err
	}
	nonce, err := randToken(32)
	if err != nil {
		return "", err
	}
	verifier := oauth2.GenerateVerifier()
	if err := s.oidcAuthReqs.Create(ctx, state, nonce, verifier, p.ID, time.Now().Add(oidcAuthRequestTTL)); err != nil {
		return "", err
	}
	return v.oauth2.AuthCodeURL(state,
		oidc.Nonce(nonce),
		oauth2.S256ChallengeOption(verifier),
	), nil
}

// verifyOIDCCallback consumes the state row, exchanges the code, verifies the
// ID token (signature, iss, aud, exp, nonce), and returns the claims. It does
// NOT resolve a user or issue a session (Task 11).
func (s *Service) verifyOIDCCallback(ctx context.Context, state, code string) (*OIDCClaims, error) {
	req, err := s.oidcAuthReqs.Consume(ctx, state)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, ErrInvalidOIDCState
		}
		return nil, err
	}
	v, err := s.oidcVerifierFor(ctx)
	if err != nil {
		return nil, err
	}
	tok, err := v.oauth2.Exchange(ctx, code, oauth2.VerifierOption(req.PKCEVerifier))
	if err != nil {
		return nil, ErrOIDCExchange
	}
	rawID, ok := tok.Extra("id_token").(string)
	if !ok || rawID == "" {
		return nil, ErrOIDCExchange
	}
	idt, err := v.verifier.Verify(ctx, rawID)
	if err != nil {
		return nil, ErrOIDCExchange
	}
	if idt.Nonce != req.Nonce {
		return nil, ErrOIDCExchange
	}
	var c struct {
		Email         string `json:"email"`
		EmailVerified bool   `json:"email_verified"`
	}
	if err := idt.Claims(&c); err != nil {
		return nil, ErrOIDCExchange
	}
	return &OIDCClaims{Issuer: idt.Issuer, Subject: idt.Subject, Email: c.Email, EmailVerified: c.EmailVerified}, nil
}
```

Replace the temporary `invalidateOIDCVerifier` no-op from Task 9 with the real one above (delete the stub).

Add the new sentinels to `internal/auth/errors.go`:
```go
	ErrInvalidOIDCState = errors.New("auth: invalid or expired oidc state")
	ErrOIDCExchange     = errors.New("auth: oidc token exchange or verification failed")
	ErrOIDCDenied       = errors.New("auth: oidc login denied") // used in Task 11
```

> `randToken`, `zeroize` already exist in `service.go`. `oauth2.GenerateVerifier`, `oauth2.S256ChallengeOption`, `oauth2.VerifierOption`, and `oidc.Nonce` are provided by the pinned versions; confirm with `go doc golang.org/x/oauth2` / `go doc github.com/coreos/go-oidc/v3/oidc` and adjust if a helper name differs.

- [ ] **Step 5: Verify pass**

Run: `go test ./internal/auth/ -run TestOIDCStartAndVerify -count=1`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/auth/oidc.go internal/auth/service.go internal/auth/errors.go internal/auth/oidc_mockidp_test.go internal/auth/oidc_flow_test.go
git commit -m "feat(auth): OIDC verifier core + start/verify against a mock IdP"
```

---

### Task 11: User resolution + session issuance

**Files:**
- Modify: `internal/auth/oidc.go`
- Test: `internal/auth/oidc_resolve_test.go`

- [ ] **Step 1: Write the failing test** — `internal/auth/oidc_resolve_test.go`

```go
package auth

import (
	"testing"
)

func TestOIDCResolveMatrix(t *testing.T) {
	svc, ctx := newUnsealedService(t)
	// Pre-provision a user by email (no OIDC link yet).
	uid, _, err := svc.CreateUser(ctx, "match@example.com")
	if err != nil {
		t.Fatal(err)
	}

	// First login: matched by verified email → link created → session issued.
	cookie, err := svc.resolveOIDCLogin(ctx, &OIDCClaims{
		Issuer: "https://iss", Subject: "sub-1", Email: "match@example.com", EmailVerified: true,
	})
	if err != nil {
		t.Fatalf("first login: %v", err)
	}
	p, err := svc.VerifySession(ctx, cookie)
	if err != nil || p.ID != uid {
		t.Fatalf("session: p=%+v err=%v", p, err)
	}

	// Second login: resolved by (iss, sub) link (email irrelevant now).
	if _, err := svc.resolveOIDCLogin(ctx, &OIDCClaims{
		Issuer: "https://iss", Subject: "sub-1", Email: "changed@example.com", EmailVerified: true,
	}); err != nil {
		t.Fatalf("second login: %v", err)
	}

	// Unknown email → deny (no auto-provision).
	if _, err := svc.resolveOIDCLogin(ctx, &OIDCClaims{
		Issuer: "https://iss", Subject: "sub-2", Email: "nobody@example.com", EmailVerified: true,
	}); err != ErrOIDCDenied {
		t.Fatalf("unknown email: want ErrOIDCDenied, got %v", err)
	}

	// Unverified email → deny even if a user exists.
	if _, err := svc.resolveOIDCLogin(ctx, &OIDCClaims{
		Issuer: "https://iss", Subject: "sub-3", Email: "match@example.com", EmailVerified: false,
	}); err != ErrOIDCDenied {
		t.Fatalf("unverified: want ErrOIDCDenied, got %v", err)
	}

	// Disabled user → deny.
	if err := svc.DisableUser(ctx, uid); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.resolveOIDCLogin(ctx, &OIDCClaims{
		Issuer: "https://iss", Subject: "sub-1", Email: "x", EmailVerified: true,
	}); err != ErrOIDCDenied {
		t.Fatalf("disabled: want ErrOIDCDenied, got %v", err)
	}
}
```

- [ ] **Step 2: Run to verify failure**

Run: `go test ./internal/auth/ -run TestOIDCResolveMatrix -count=1`
Expected: FAIL (undefined `resolveOIDCLogin`).

- [ ] **Step 3: Implement** — append to `internal/auth/oidc.go`

```go
// resolveOIDCLogin maps verified claims to a pre-provisioned user and issues a
// session cookie. Policy: link by (issuer, subject) if present; else match an
// existing user by verified email (no auto-provision); deny disabled users and
// unverified/unknown emails. All denials return ErrOIDCDenied (no enumeration).
func (s *Service) resolveOIDCLogin(ctx context.Context, c *OIDCClaims) (string, error) {
	link, err := s.oidcIdentities.GetBySubject(ctx, c.Issuer, c.Subject)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		return "", err
	}
	var userID string
	if err == nil {
		u, uErr := s.users.Get(ctx, link.UserID)
		if uErr != nil || u.DisabledAt != nil {
			return "", ErrOIDCDenied
		}
		userID = u.ID
		_ = s.oidcIdentities.TouchLastLogin(ctx, link.ID)
	} else {
		if !c.EmailVerified {
			return "", ErrOIDCDenied
		}
		u, uErr := s.users.GetByEmail(ctx, c.Email)
		if uErr != nil || u.DisabledAt != nil {
			return "", ErrOIDCDenied // unknown or disabled — indistinguishable
		}
		if _, cErr := s.oidcIdentities.Create(ctx, u.ID, c.Issuer, c.Subject); cErr != nil {
			return "", cErr
		}
		userID = u.ID
	}
	return s.createSession(ctx, userID)
}

// CompleteOIDCLogin is the public entry the API calls: verify the callback then
// resolve + issue a session. Returns the session cookie.
func (s *Service) CompleteOIDCLogin(ctx context.Context, state, code string) (string, error) {
	claims, err := s.verifyOIDCCallback(ctx, state, code)
	if err != nil {
		return "", err
	}
	return s.resolveOIDCLogin(ctx, claims)
}
```

- [ ] **Step 4: Verify pass**

Run: `go test ./internal/auth/ -run 'TestOIDCResolveMatrix|TestOIDCStartAndVerify' -count=1`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/auth/oidc.go internal/auth/oidc_resolve_test.go
git commit -m "feat(auth): resolve OIDC claims to a pre-provisioned user + issue session"
```

---

### Task 12: API — status / login / callback + routes

**Files:**
- Create: `internal/api/oidc_handlers.go`
- Modify: `internal/api/server.go` (routes)
- Test: `internal/api/oidc_login_test.go`

- [ ] **Step 1: Write the failing test** — `internal/api/oidc_login_test.go`

```go
package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestOIDCLoginEndToEnd(t *testing.T) {
	// Stand up an unsealed server wired to a real store (testcontainers) — reuse
	// the existing helper the auth e2e tests use (grep for newTestServerUnsealed
	// or equivalent). Then configure a provider pointing at a mock IdP.
	ts, idp := newOIDCTestServer(t) // helper defined below in Step 3
	defer ts.Close()

	// status reports enabled.
	res, _ := http.Get(ts.URL + "/v1/auth/oidc/status")
	if res.StatusCode != 200 {
		t.Fatalf("status: %d", res.StatusCode)
	}

	// login → 302 to the IdP authorize endpoint.
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	res, _ = client.Get(ts.URL + "/v1/auth/oidc/login")
	if res.StatusCode != http.StatusFound {
		t.Fatalf("login: want 302, got %d", res.StatusCode)
	}
	loc := res.Header.Get("Location")
	if !strings.HasPrefix(loc, idp.srv.URL+"/authorize") {
		t.Fatalf("login location: %s", loc)
	}
	state := extractQ(t, loc, "state")
	nonce := extractQ(t, loc, "nonce")

	// Drive the IdP to mint a token for a pre-provisioned user, then hit callback.
	idp.sub, idp.email, idp.emailVer, idp.nonce = "sub-1", "seed@example.com", true, nonce
	res, _ = client.Get(ts.URL + "/v1/auth/oidc/callback?state=" + state + "&code=xyz")
	if res.StatusCode != http.StatusFound {
		t.Fatalf("callback: want 302, got %d", res.StatusCode)
	}
	if !hasCookie(res, "janus_session") {
		t.Fatal("callback did not set janus_session")
	}
}
```

> `newOIDCTestServer` must: build the unsealed api server against a testcontainers store, create a user `seed@example.com`, spin up the `mockIdP` (reuse the harness from Task 10 — extract it to a shared `internal/api` test helper or duplicate minimally), and `PUT /v1/sys/oidc` (or call `authSvc.SetOIDCProvider`) with the mock issuer. `extractQ`/`hasCookie` are tiny local helpers. Model this on the existing auth e2e test setup.

- [ ] **Step 2: Run to verify failure**

Run: `go test ./internal/api/ -run TestOIDCLoginEndToEnd -count=1`
Expected: FAIL (routes/handlers undefined).

- [ ] **Step 3: Implement handlers** — `internal/api/oidc_handlers.go`

```go
package api

import (
	"errors"
	"net/http"

	"github.com/steveokay/janus-secrets/internal/auth"
)

func (s *Server) handleOIDCStatus(w http.ResponseWriter, r *http.Request) {
	v, err := s.auth.GetOIDCProvider(r.Context())
	if err != nil || !v.Enabled {
		writeJSON(w, http.StatusOK, map[string]any{"enabled": false})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"enabled": true, "name": v.Name})
}

func (s *Server) handleOIDCLogin(w http.ResponseWriter, r *http.Request) {
	url, err := s.auth.StartOIDCLogin(r.Context())
	if err != nil {
		if errors.Is(err, auth.ErrNotFound) {
			writeError(w, http.StatusNotFound, "oidc_not_configured", "OIDC login is not configured")
			return
		}
		s.writeServiceError(w, err) // crypto.ErrSealed → 503
		return
	}
	http.Redirect(w, r, url, http.StatusFound)
}

func (s *Server) handleOIDCCallback(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	if e := q.Get("error"); e != "" {
		s.recordActor(r, anonActor(), "auth.login", "auth/oidc", "denied", "oidc_denied", "provider error")
		writeError(w, http.StatusBadRequest, "oidc_denied", "authentication failed")
		return
	}
	cookie, err := s.auth.CompleteOIDCLogin(r.Context(), q.Get("state"), q.Get("code"))
	if err != nil {
		code := "oidc_denied"
		status := http.StatusUnauthorized
		switch {
		case errors.Is(err, auth.ErrInvalidOIDCState):
			code, status = "invalid_oidc_state", http.StatusBadRequest
		case errors.Is(err, auth.ErrOIDCExchange):
			code = "oidc_denied"
		}
		s.recordActor(r, anonActor(), "auth.login", "auth/oidc", "denied", code, "")
		writeError(w, status, code, "authentication failed")
		return
	}
	setSessionCookie(w, r, cookie) // reuse the exact helper handleLogin uses to set janus_session
	http.Redirect(w, r, "/", http.StatusFound)
}
```

> Reuse the **exact** session-cookie-setting the existing `handleLogin` uses (grep `internal/api` for where it writes `janus_session` — mirror its flags: `HttpOnly`, `SameSite`, `Path=/`, conditional `Secure`). If it's inline, extract a `setSessionCookie(w, r, cookie)` helper and use it in both places. `anonActor()` — if the audit helpers don't already expose an anonymous actor constructor, use `s.record(r, ...)` (which defaults to the anonymous actor when no principal is in context) instead of `recordActor`.

- [ ] **Step 4: Register routes** — in `internal/api/server.go`, inside the `if s.auth != nil { r.Route("/v1/auth", ...) }` block, add public (no `RequireAuth`) OIDC routes alongside `login`:

```go
		r.With(loginLimiter.middleware).Get("/oidc/status", s.handleOIDCStatus)
		r.With(loginLimiter.middleware).Get("/oidc/login", s.handleOIDCLogin)
		r.With(loginLimiter.middleware).Get("/oidc/callback", s.handleOIDCCallback)
```
(These sit outside the `RequireAuth` `r.Group`, like `login`. They remain behind the global `RequireUnsealed`.)

- [ ] **Step 5: Verify pass**

Run: `go test ./internal/api/ -run TestOIDCLoginEndToEnd -count=1`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/api/oidc_handlers.go internal/api/server.go internal/api/oidc_login_test.go
git commit -m "feat(api): OIDC status/login/callback endpoints (session on success)"
```

---

### Task 13: API — provider config (`/v1/sys/oidc`) + RBAC + audit

**Files:**
- Modify: `internal/api/oidc_handlers.go`
- Modify: `internal/api/server.go`
- Test: `internal/api/oidc_config_test.go`

- [ ] **Step 1: Write the failing test** — `internal/api/oidc_config_test.go`

```go
package api

import (
	"net/http"
	"strings"
	"testing"
)

func TestOIDCConfigRBACAndSecret(t *testing.T) {
	ts, _ := newOIDCTestServer(t) // same helper as Task 12
	defer ts.Close()

	// A viewer/dev token cannot manage OIDC; owner can. Use the suite's helpers
	// to mint a scoped credential / session for each role (mirror the existing
	// RBAC matrix e2e in internal/api). Assertions:
	//  - PUT /v1/sys/oidc as non-admin → 403
	//  - PUT as owner → 200
	//  - GET as owner → 200 and body has "secret_set":true and NEVER the secret
	body := `{"name":"default","issuer":"` + ts.idpURL + `","client_id":"test-client","client_secret":"top-secret","redirect_url":"https://app/cb","enabled":true}`
	res := ts.doAs(t, "owner", http.MethodPut, "/v1/sys/oidc", body)
	if res.StatusCode != 200 {
		t.Fatalf("owner PUT: %d", res.StatusCode)
	}
	res = ts.doAs(t, "viewer", http.MethodPut, "/v1/sys/oidc", body)
	if res.StatusCode != 403 {
		t.Fatalf("viewer PUT: want 403, got %d", res.StatusCode)
	}
	res = ts.doAs(t, "owner", http.MethodGet, "/v1/sys/oidc", "")
	got := readBody(t, res)
	if !strings.Contains(got, `"secret_set":true`) || strings.Contains(got, "top-secret") {
		t.Fatalf("GET leaked or missing secret_set: %s", got)
	}
}
```

> This mirrors the existing RBAC-matrix e2e style in `internal/api`. Reuse whatever helper the suite already has for "do request as role X"; the names above (`doAs`, `readBody`, `ts.idpURL`) are placeholders for the suite's real helpers — wire `newOIDCTestServer` to expose them.

- [ ] **Step 2: Run to verify failure**

Run: `go test ./internal/api/ -run TestOIDCConfigRBACAndSecret -count=1`
Expected: FAIL (routes/handlers undefined).

- [ ] **Step 3: Implement config handlers** — append to `internal/api/oidc_handlers.go`

```go
import (
	// add:
	"encoding/json"

	"github.com/steveokay/janus-secrets/internal/authz"
)

type oidcConfigRequest struct {
	Name         string   `json:"name"`
	Issuer       string   `json:"issuer"`
	ClientID     string   `json:"client_id"`
	ClientSecret string   `json:"client_secret"`
	Scopes       []string `json:"scopes"`
	RedirectURL  string   `json:"redirect_url"`
	Enabled      bool     `json:"enabled"`
}

func (s *Server) handleOIDCConfigGet(w http.ResponseWriter, r *http.Request) {
	if !s.authorize(w, r, authz.OIDCManage, authz.Instance(), "oidc.config.read", "oidc") {
		return
	}
	v, err := s.auth.GetOIDCProvider(r.Context())
	if err != nil {
		s.writeServiceError(w, err) // ErrNotFound → 404
		return
	}
	writeJSON(w, http.StatusOK, v) // OIDCProviderView has secret_set, never the secret
}

func (s *Server) handleOIDCConfigPut(w http.ResponseWriter, r *http.Request) {
	if !s.authorize(w, r, authz.OIDCManage, authz.Instance(), "oidc.config.write", "oidc") {
		return
	}
	var req oidcConfigRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, CodeValidation, "invalid body")
		return
	}
	err := s.auth.SetOIDCProvider(r.Context(), auth.OIDCProviderInput{
		Name: req.Name, Issuer: req.Issuer, ClientID: req.ClientID,
		ClientSecret: req.ClientSecret, Scopes: req.Scopes,
		RedirectURL: req.RedirectURL, Enabled: req.Enabled,
	})
	if err != nil {
		s.writeServiceError(w, err)
		return
	}
	// Audit: record issuer + client_id, NEVER the secret.
	if err := s.record(r, "oidc.config.write", "oidc", "success", "", "issuer="+req.Issuer+" client_id="+req.ClientID); err != nil {
		writeError(w, http.StatusInternalServerError, CodeInternal, "internal error")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleOIDCConfigDelete(w http.ResponseWriter, r *http.Request) {
	if !s.authorize(w, r, authz.OIDCManage, authz.Instance(), "oidc.config.delete", "oidc") {
		return
	}
	if err := s.auth.DeleteOIDCProvider(r.Context()); err != nil {
		s.writeServiceError(w, err)
		return
	}
	if err := s.record(r, "oidc.config.delete", "oidc", "success", "", ""); err != nil {
		writeError(w, http.StatusInternalServerError, CodeInternal, "internal error")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
```

> Confirm the exact `writeServiceError` mapping already turns `auth.ErrValidation`→400 and `auth.ErrNotFound`→404; if `auth.ErrValidation`/`auth.ErrNotFound` aren't in its switch, add those cases (mirroring how it handles `store.ErrNotFound`). `authz.Instance()` is the instance-scope constructor used by `handleProjectCreate`.

- [ ] **Step 4: Register routes** — in `internal/api/server.go`, in the public `/v1/sys` route block, add (behind `RequireAuth` + `oidc:manage`, mirroring the `sys/seal` pattern):

```go
	if s.auth != nil && s.authz != nil {
		r.With(RequireAuth(s.auth), s.requireInstance(authz.OIDCManage, "oidc.config", "oidc")).Get("/oidc", s.handleOIDCConfigGet)
		r.With(RequireAuth(s.auth), s.requireInstance(authz.OIDCManage, "oidc.config", "oidc")).Put("/oidc", s.handleOIDCConfigPut)
		r.With(RequireAuth(s.auth), s.requireInstance(authz.OIDCManage, "oidc.config", "oidc")).Delete("/oidc", s.handleOIDCConfigDelete)
	}
```

> If `requireInstance` already performs the authorize+audit-denial, the in-handler `s.authorize(...)` call becomes redundant — follow whichever single pattern the existing `sys/seal` route uses (it uses `requireInstance`). If you keep `requireInstance` on the route, drop the `s.authorize(...)` guard inside the handlers to avoid double-recording; keep only `s.record(...)` on success. Match the existing convention exactly.

- [ ] **Step 5: Verify pass**

Run: `go test ./internal/api/ -run TestOIDCConfigRBACAndSecret -count=1`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/api/oidc_handlers.go internal/api/server.go internal/api/oidc_config_test.go
git commit -m "feat(api): /v1/sys/oidc provider config (oidc:manage, audited, secret never returned)"
```

---

### Task 14: Leak test + full gate sweep

**Files:**
- Test: `internal/api/oidc_leak_test.go`
- Modify (if needed): `internal/api/boot.go` (sweep `oidc_auth_requests` at boot)

- [ ] **Step 1: Add a boot sweep** — in `internal/api/boot.go` where `SweepExpiredSessions` is called at boot (grep for it), add alongside it:

```go
	_ = authSvc.SweepExpiredOIDCRequests(ctx) // best-effort, like the session sweep
```
And add to `internal/auth/oidc.go`:
```go
// SweepExpiredOIDCRequests removes stale login-state rows (called at boot).
func (s *Service) SweepExpiredOIDCRequests(ctx context.Context) error {
	return s.oidcAuthReqs.DeleteExpired(ctx)
}
```
> If Boot has no existing session sweep call, add the sweep right after `authSvc` is constructed and the server is known-unsealed is NOT required (the sweep is a plain DELETE, no crypto). Place it near the migrate/boot bootstrap. Keep it best-effort (ignore the error like the session sweep).

- [ ] **Step 2: Write the leak test** — `internal/api/oidc_leak_test.go`

```go
package api

import (
	"strings"
	"testing"
)

// TestOIDCClientSecretNeverLeaks drives provider config + a login and asserts
// the client secret never appears in captured logs, error bodies, or any
// audit_events row.
func TestOIDCClientSecretNeverLeaks(t *testing.T) {
	const canary = "CANARY-oidc-client-secret-4f2a"
	ts, capturedLogs := newOIDCTestServerCapturingLogs(t, canary) // configures provider with client_secret=canary
	defer ts.Close()

	// GET config must not echo the secret.
	body := readBody(t, ts.doAs(t, "owner", "GET", "/v1/sys/oidc", ""))
	if strings.Contains(body, canary) {
		t.Fatal("client secret leaked in GET /v1/sys/oidc")
	}
	// Captured request logs must not contain it.
	if strings.Contains(capturedLogs.String(), canary) {
		t.Fatal("client secret leaked into logs")
	}
	// audit_events must not contain it (query the store the suite exposes).
	if ts.auditContains(t, canary) {
		t.Fatal("client secret leaked into an audit row")
	}
}
```

> Reuse the log-capture pattern from the existing auth/secret leak tests (`TestNoSecretValueInLogsOrErrorResponse` / the M5 credential leak test) — they already show how to capture the request logger and scan `audit_events`. Wire `newOIDCTestServerCapturingLogs` on top of `newOIDCTestServer`.

- [ ] **Step 3: Run the leak test**

Run: `go test ./internal/api/ -run TestOIDCClientSecretNeverLeaks -count=1`
Expected: PASS.

- [ ] **Step 4: Full gate sweep**

Run each; all must pass:
```bash
go build ./...
go vet ./...
go test ./... -count=1
go test ./internal/crypto/ -cover -count=1        # expect 100.0%
gosec -exclude-dir=internal/crypto/shamir ./...    # expect 0 issues
govulncheck ./...                                  # expect 0 affecting (incl. go-oidc/go-jose/oauth2)
```
Expected: all green. If `govulncheck` flags a go-jose/go-oidc/oauth2 advisory, bump that module to the fixed version (`go get module@fixed && go mod tidy`) and re-run. If `gosec` flags something in the new code, address it or add a justified `#nosec` with a comment (mirror existing `#nosec` justifications).

- [ ] **Step 5: Commit**

```bash
git add internal/api/oidc_leak_test.go internal/api/boot.go internal/auth/oidc.go
git commit -m "test(oidc): client-secret leak test + boot sweep of expired login state"
```

---

### Task 15: Docs + CLAUDE.md carve-out + tracker

**Files:**
- Create: `docs/oidc.md`
- Modify: `CLAUDE.md` (crypto-deps carve-out note)
- Modify: `status.md`

- [ ] **Step 1: Write `docs/oidc.md`**

Document: the flow (Authorization Code + PKCE + state + nonce), the endpoints (`/v1/auth/oidc/status|login|callback`, `/v1/sys/oidc`), provider config fields (esp. the explicit `redirect_url` and that the client_secret is master-key-wrapped), the pre-provisioned mapping policy (link by `(iss, sub)`; match existing user by verified email; no auto-provision), the sealed-server behavior (login 503 while sealed), and the **UI handoff**: the SPA shows a "Sign in with OIDC" button when `GET /v1/auth/oidc/status` returns `{"enabled":true}`, links the button to `GET /v1/auth/oidc/login` (full-page navigation, not fetch), and the config screen is backed by `GET/PUT/DELETE /v1/sys/oidc`. Note C2 (CI federation) is the next slice.

- [ ] **Step 2: Add the CLAUDE.md carve-out** — under the Cryptography rules, note:

```
- OIDC/JOSE exception (approved 2026-07-07): OIDC login (sub-project C) uses
  audited third-party libraries — github.com/coreos/go-oidc/v3, golang.org/x/oauth2,
  and (transitively) github.com/go-jose/go-jose/v4 — for JWT/JWKS verification,
  rather than hand-rolling JOSE. This is the sole third-party crypto-adjacent
  exception; the envelope/transit/unseal crypto remains stdlib + x/crypto only.
```

- [ ] **Step 3: Update `status.md`** — add a "Phase 2 · Sub-project C1 — OIDC login" section summarizing scope delivered, the endpoints, the pre-provisioned policy, the crypto-lib exception, and the verification results. Note C2 (CI federation) is the follow-up.

- [ ] **Step 4: Verify docs build nothing / final full sweep**

Run: `go build ./... && go test ./... -count=1`
Expected: green.

- [ ] **Step 5: Commit**

```bash
git add docs/oidc.md CLAUDE.md status.md
git commit -m "docs(oidc): C1 login flow, endpoints, UI handoff; record crypto-lib exception"
```

---

## Self-review notes (for the executor)

- **Spec coverage:** tasks map to every spec section — schema (T3–T6), crypto wrap (T2), provider config + `oidc:manage` (T7, T9, T13), flow with PKCE/state/nonce (T10), pre-provisioned resolution matrix (T11), endpoints (T12–T13), sealed coherence (login/callback behind `RequireUnsealed`, verified in T12 setup), audit + leak (T13–T14), mock-IdP e2e (T10, T12), deps + CLAUDE.md exception (T1, T15).
- **Type consistency:** `OIDCProviderInput`/`OIDCProviderView`/`OIDCClaims` names are used identically across T9–T13; store methods (`Put/Get/Delete`, `GetBySubject/Create/TouchLastLogin`, `Create/Consume/DeleteExpired`) match between definition and callers; `createSession` (T8) is the single session-mint path reused in T11.
- **Placeholders:** every code step contains real code. Where a task must reuse an existing helper whose exact name the plan can't see (`newTestStore`, `newUnsealedService`, the session-cookie setter, the leak-test log capture, the RBAC-matrix `doAs`), the step says explicitly to grep the neighboring `*_test.go`/handler for the real name and mirror it — these are integration seams, not logic gaps.
- **Finish:** after T15, run the finishing-a-development-branch skill → open a PR to `main`. Coordinate timing with the UI agent's merges; the Go-only diff limits conflicts to `go.mod`/`go.sum`, `migrations/` (next free number is 000007 — confirm the UI agent didn't also add a migration before pushing), and `internal/api/server.go`/`boot.go` route wiring.
