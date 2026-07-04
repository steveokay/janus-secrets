# Auth (passwords, sessions, service tokens) — Design Spec

**Milestone 5 (Phase 1).** Date: 2026-07-04.

## Goal

Give Janus its first identities. An admin user is created during the init
ceremony; humans log in with email + Argon2id-verified password and get a
revocable session cookie; automation gets `kh_svc_…` service tokens (scoped,
HMAC-stored, shown exactly once). A `RequireAuth` middleware resolves every
credential to a single `Principal` type — the seam that RBAC, audit, and
Phase-2 federation all build on. `POST /v1/sys/seal` becomes authenticated,
closing Milestone 4's documented caveat.

## Scope

**In scope:**

- Migration `000002`: `users`, `sessions`, `service_tokens`, `auth_config`
  tables; matching `internal/store` repositories (store stays secret-blind —
  it persists PHC hash strings and HMAC digests, never raw credentials).
- New package `internal/auth`: `Principal`, Argon2id hashing/verification,
  session lifecycle, service-token mint/verify/list/revoke, HMAC keying,
  bootstrap helpers.
- Two small `internal/crypto` additions (100% coverage bar maintained):
  `Keyring.WrapAuthKey` / `UnwrapAuthKey` + `AuthKeyAAD()` — the master key
  wraps the token-HMAC key under a fixed `janus:auth:token-hmac` AAD label.
- `internal/api`: `/v1/auth/*` and `/v1/tokens*` endpoints, `RequireAuth`
  middleware, per-IP rate limiting on credential endpoints, auth-gating
  `POST /v1/sys/seal`, init-ceremony admin bootstrap, first-unseal HMAC-key
  bootstrap.
- `janus init` CLI output extended with the one-time admin credential.
- New dependency: `golang.org/x/time/rate` (x-family) for rate limiting.
  `golang.org/x/crypto/argon2` is already a module dependency.

**Out of scope (deferred, documented):**

- User CRUD and roles — the RBAC milestone owns them. In M5 the only user is
  the init-created admin, so "authenticated" and "admin" coincide.
- OIDC login and OIDC-federated CI machine identity (GitHub Actions JWT
  exchange) — Phase 2, tracked in status.md. The `Principal` seam and the
  nullable `password_hash` / `kh_<type>_` namespace exist so this lands
  without schema or middleware rework.
- Scope *enforcement* for service tokens (RBAC/API milestones); M5 stores and
  validates scope at mint time only.
- Account lockout/backoff beyond rate limiting, MFA, password-policy knobs.
- `kh login` / any new CLI commands (the API is curl-able; interactive login
  is the UI's job; `kh` is a later milestone).

## Identity model & schema (migration `000002`)

### `users`

| Column | Type | Notes |
|---|---|---|
| `id` | uuid PK (`gen_random_uuid()`) | |
| `email` | text, unique on `lower(email)` | |
| `password_hash` | text **nullable** | Argon2id PHC string; NULL reserves room for Phase-2 OIDC-only users |
| `created_at` / `updated_at` | timestamptz | |
| `disabled_at` | timestamptz nullable | disabled users fail login indistinguishably |

No role column — RBAC owns roles. The PHC string
(`$argon2id$v=19$m=…,t=…,p=…$salt$hash`) is self-describing, so parameter
upgrades re-hash lazily at next login with no schema change.

### `sessions`

| Column | Type | Notes |
|---|---|---|
| `id` | uuid PK | |
| `user_id` | uuid FK → users | |
| `token_hmac` | bytea, unique | HMAC-SHA256 of the cookie value; the raw value is never stored |
| `created_at` / `expires_at` / `last_seen_at` | timestamptz | 24h expiry, sliding `last_seen` |

Expired rows are deleted opportunistically on lookup plus a sweep at boot; no
background job.

### `service_tokens`

| Column | Type | Notes |
|---|---|---|
| `id` | uuid PK | |
| `name` | text | operator label, non-secret |
| `token_hmac` | bytea, unique | HMAC-SHA256 of the raw token |
| `created_by` | uuid FK → users | |
| `scope_kind` | text | `config` or `environment` |
| `scope_id` | uuid | validated to exist at mint; **enforced by later milestones** |
| `access` | text | `read` or `readwrite` |
| `created_at` | timestamptz | |
| `expires_at` | timestamptz nullable | long-lived by default |
| `revoked_at` | timestamptz nullable | |

### `auth_config`

Single row (`id = 1`) holding `wrapped_token_hmac_key (bytea)`: a random
256-bit key generated at first unseal, wrapped by the master key
(AAD `janus:auth:token-hmac`). Sessions and service tokens are both HMAC'd
with this key, so a database dump yields nothing verifiable offline.
**Consequence, stated plainly: credential verification requires an unsealed
server.** This is coherent — every route a credential protects is 503 while
sealed, and `/v1/auth/*` sits behind the existing `RequireUnsealed`
middleware.

## `internal/auth`

### `Principal` — the federation seam

```go
type PrincipalKind string

const (
    KindUser         PrincipalKind = "user"
    KindServiceToken PrincipalKind = "service_token"
    // Phase 2 adds federated kinds; middleware/RBAC/audit are unaffected.
)

type Principal struct {
    Kind PrincipalKind
    ID   string // users.id or service_tokens.id
    Name string // email or token name — display/audit, never secret
}
```

Everything downstream (middleware context, RBAC, audit) consumes `Principal`
and nothing else. Federation later = new verifiers emitting the same struct.

### Service

`auth.Service` holds the store repos + the injected `*crypto.Keyring`
(mirroring `secrets.Service`):

- **Passwords** — `HashPassword([]byte) (string, error)` /
  `VerifyPassword(hash string, pw []byte) (ok, needsRehash bool, err error)`
  using `x/crypto/argon2.IDKey` at OWASP-recommended parameters (m = 19 MiB,
  t = 2, p = 1), PHC-encoded. Comparison via `subtle.ConstantTimeCompare`.
  Password byte-slices zeroized after use.
- **Sessions** — `Login(ctx, email, password) (cookie string, err)`: verifies
  the password, mints 32 random bytes (base64url), stores the HMAC, 24h
  expiry. `VerifySession(ctx, cookie) (Principal, error)` (bumps
  `last_seen`), `Logout(ctx, cookie)`. Wrong password, unknown email, and
  disabled user all return one indistinguishable `ErrInvalidCredentials`.
- **Service tokens** — `MintServiceToken(ctx, by Principal, name, scopeKind,
  scopeID, access string, ttl *time.Duration) (raw string, meta TokenMeta,
  err)`: raw = `"kh_svc_" + base64url(32 random bytes)`, returned exactly
  once; scope target existence validated. `VerifyServiceToken(ctx, raw)
  (Principal, error)` checks HMAC match + not-revoked + not-expired.
  `ListTokens(ctx) ([]TokenMeta, error)` — `TokenMeta` has **no raw-token
  field** (structural no-leak, like `SecretMeta`). `RevokeToken(ctx, id)`.
  The `kh_<type>_` prefix namespace leaves room for Phase-2 federated
  credential types.
- **HMAC keying** — every verification loads `auth_config`, unwraps the HMAC
  key via `Keyring.UnwrapAuthKey`, uses it, and zeroizes it. No caching —
  same discipline as `internal/secrets`; an AES-GCM unwrap is microseconds.
  Sealed keyring → `crypto.ErrSealed` surfaces (API maps to 503).
- **Bootstrap** — `CreateInitialAdmin(ctx, email) (oneTimePassword string,
  err)`: generates a random password (24+ chars base64url), hashes, inserts
  the user; called only from the init ceremony. `EnsureHMACKey(ctx)`:
  generate + wrap + `INSERT … ON CONFLICT DO NOTHING` + re-read — called at
  the first-unseal transition; concurrent racers converge on one key.
- **`ChangePassword(ctx, userID, old, new)`** — so the generated bootstrap
  password isn't permanent.

### Crypto additions

`Keyring.WrapAuthKey(k []byte) (Ciphertext, error)` and
`UnwrapAuthKey(ct Ciphertext) ([]byte, error)`, plus `AuthKeyAAD() []byte` —
identical shape to `WrapProjectKEK`/`UnwrapProjectKEK` with a fixed AAD label.
The master key never leaves the keyring. Tests preserve 100% coverage.

## Bootstrap flow (two halves)

The token-HMAC key must be wrapped by the master key, but after a **Shamir**
init the server is still sealed (the master key was split and zeroized —
never in the keyring). So:

1. **At init** (`POST /v1/sys/init`, both seal types, serialized by the
   existing `initMu`): create the admin user — an Argon2id hash needs no
   master key. The init request gains optional `admin_email` (default
   `admin@localhost`); the response gains
   `{"admin":{"email":"…","password":"<one-time>"}}`. `janus init` prints it
   alongside the shares with the same "will not be shown again" warning
   (`--json` includes it).
2. **At first unseal** (`unsealNow` success path): if `auth_config` is empty,
   `EnsureHMACKey` generates, wraps, and stores the key. No operator-visible
   artifact; sessions/tokens are unusable while sealed anyway, so deferring
   loses nothing.

## HTTP surface

All JSON, project error envelope. New error codes: `unauthenticated` (401),
`invalid_credentials` (401), `rate_limited` (429).

| Route | Auth | Behavior |
|---|---|---|
| `POST /v1/auth/login` | none; rate-limited | `{email,password}` → `Set-Cookie: janus_session=<value>; HttpOnly; SameSite=Strict; Path=/; Secure (when TLS)` + `200 {"user":{"id","email"}}` |
| `POST /v1/auth/logout` | session | delete the session row, expire the cookie → 204 |
| `GET /v1/auth/me` | any principal | `200 {"kind","id","name"}` |
| `POST /v1/auth/password` | session; rate-limited | `{old,new}` → 204; re-verifies old password |
| `POST /v1/tokens` | any principal | `{name, scope:{kind,id}, access, ttl_seconds?}` → `200 {token, id, name, scope, access, expires_at}` — token shown once; scope target must exist (404 otherwise) |
| `GET /v1/tokens` | any principal | `200 {tokens:[metadata…]}` |
| `DELETE /v1/tokens/{id}` | any principal | 204; unknown id → 404 |
| `POST /v1/sys/seal` | **any principal (new)** | closes the M4 caveat. Edge: while sealed, credentials cannot verify — sealing a sealed server is a no-op, so nothing is lost |

### Wiring

`api.Boot` constructs `auth.NewService(st, kr)` alongside `secrets.NewService`
and passes it to `api.New` (one new parameter); `api.Server` holds it for the
auth handlers, the `RequireAuth` middleware, and the first-unseal
`EnsureHMACKey` call inside `unsealNow`.

### Middleware

`RequireAuth` (route-scoped): reads `Authorization: Bearer kh_svc_…` (service
token) or the `janus_session` cookie (session), dispatches to the matching
verifier, injects the `Principal` into the request context (`api.PrincipalFrom
(ctx)` accessor for later milestones); failure → 401 `unauthenticated`. Chain
order stays `requestLogger → RequireUnsealed → (per-route) RequireAuth`; the
existing `RequireUnsealed` already 503s `/v1/auth/*` while sealed, which
matches the HMAC-key reality exactly.

### Rate limiting

In-memory per-client-IP token buckets (`golang.org/x/time/rate`) on `login`
and `password`: 10 requests/minute sustained, burst 5, per IP; exceeded →
429 `rate_limited`. Buckets pruned on a coarse interval to bound memory.
Right-sized for the supported single-node topology; distributed limiting is a
non-goal.

## Security posture

- **Nothing secret at rest**: PHC strings + HMAC digests only; the HMAC key
  is master-key-wrapped. A full DB dump is not verifiable offline.
- **No credential material in logs or errors**: body-free logger (existing);
  all auth errors are static sentinels; one indistinguishable
  `invalid_credentials` for wrong-password / unknown-user / disabled-user (no
  enumeration oracle). Enforced by an auth-layer leak test (canary password +
  minted token across every error path).
- **Constant-time comparisons** for password verification; token lookup is by
  HMAC digest (computing the HMAC is the equalizer; the unique-index probe
  reveals nothing about token bytes).
- **One-time displays**: bootstrap password and minted tokens follow the
  share ceremony — shown once, never persisted raw, never re-shown.
- **Zeroization**: passwords, raw token bytes, and the unwrapped HMAC key are
  wiped after use (best-effort, consistent with the rest of the codebase).

## Testing

Store-level tests via the existing testcontainers harness; auth-service tests
against real Postgres; API tests via httptest with the in-memory seal store
where the DB isn't needed and testcontainers where it is.

1. **Password unit tests**: round-trip, wrong password, PHC parse/tamper,
   `needsRehash` on parameter change.
2. **Full lifecycle e2e**: init (admin credential present exactly once) →
   unseal (auth_config materializes) → login → `me` → mint token →
   authenticate as the token → list (no raw token) → revoke → 401 → logout →
   401.
3. **Seal coherence**: login while sealed → 503; `sys/seal` without auth →
   401, with auth → 200; post-seal, an existing session → 503.
4. **Negative auth**: wrong-password vs unknown-email byte-identical
   responses; expired session; expired/revoked token; malformed bearer;
   session cookie in the bearer slot and vice versa.
5. **Bootstrap races**: concurrent first-unseal → exactly one `auth_config`
   row; concurrent init already covered by `initMu` (extend the existing
   concurrent-init test for the admin row).
6. **Rate limiting**: exceeding the window → 429 envelope; distinct IPs
   independent; bucket pruning.
7. **Leak test**: canary password + minted token never appear in captured
   logs or any response after the one-time mint.
8. **Gates**: full suite (Docker-backed suites must run), gosec v2.27.1
   (shamir excluded) 0 issues, govulncheck clean, `internal/crypto` coverage
   100.0%.

## Verification gates

Same bar as Milestones 2–4: `go build`, `go vet`, `go test ./...`
(testcontainers-backed), gosec, govulncheck, crypto coverage. Toolchain pinned
`go1.26.4`.
