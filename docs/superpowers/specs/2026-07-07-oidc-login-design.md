# OIDC Login (human, Authorization Code) — Design Spec

**Phase 2, sub-project C1.** Date: 2026-07-07. Branch: `worktree-oidc-federation`
(isolated worktree; the UI agent owns `web/`, this work is Go-backend only).

## Goal

Let humans sign in to Janus through a configured **generic OIDC provider**
(validated against GitHub and Google) via the browser Authorization-Code flow,
and receive the **same session cookie** a password login yields. OIDC-authenticated
humans are ordinary `KindUser` principals — RBAC, audit, and middleware are
unchanged. This slice also builds the **shared OIDC verification core** that the
follow-up slice (C2, OIDC-federated CI machine identity) will reuse.

## Scope

**In scope (C1):**

- Shared OIDC core: provider discovery, JWKS, ID-token verification — via
  `github.com/coreos/go-oidc/v3` + `golang.org/x/oauth2` (see Dependencies).
- Migration `000007`: `oidc_providers` (single active provider; client_secret
  master-key-wrapped) + `oidc_identities` (durable `(issuer, subject)` → user
  link) + `oidc_auth_requests` (short-lived single-use login state).
- `internal/store` repos for the three tables (crypto-blind: stores the wrapped
  secret bytes and opaque link/state rows, never plaintext credentials).
- `internal/crypto`: `WrapOIDCClientSecret` / `UnwrapOIDCClientSecret` +
  `OIDCClientSecretAAD()` (mirrors `WrapAuthKey`; 100% coverage preserved).
- `internal/auth`: OIDC login orchestration — build auth request, handle
  callback, verify ID token, resolve to a pre-provisioned user, mint the existing
  session. No new `PrincipalKind`.
- `internal/api`: `GET /v1/auth/oidc/status|login|callback` (public,
  rate-limited, behind `RequireUnsealed`) + `GET|PUT|DELETE /v1/sys/oidc` (admin
  provider config, new `oidc:manage` instance action, audited).
- A **mock OIDC provider** test harness (httptest: discovery + JWKS + token
  endpoint, test-key-signed ID tokens) for full-flow e2e without a real IdP.

**Out of scope (deferred):**

- **C2 — OIDC-federated CI machine identity** (GitHub Actions JWT exchange →
  short-lived scoped credential). Next slice; reuses this core. It will add a new
  federated credential/principal type; C1 does not.
- **UI**: the "Sign in with OIDC" button and the provider-config screen belong to
  the parallel UI agent. C1 exposes the endpoints they consume
  (`/v1/auth/oidc/status` gates the button; `/v1/sys/oidc` backs the config UI).
- **Multiple simultaneous providers / provider picker** — schema allows more rows
  (unique `name`), but C1 ships and flows a single enabled provider.
- **JIT auto-provisioning** — pre-provisioned-only policy this slice (a
  deliberate security choice; JIT + domain allowlist can be added later without
  schema rework — `oidc_identities` and the resolver already isolate the policy).
- Refresh-token storage / OIDC single-logout / dynamic client registration.

## Dependencies (explicit CLAUDE.md exception)

CLAUDE.md: "Go stdlib `crypto/*` + `golang.org/x/crypto` ONLY … never add
third-party crypto libraries without explicit discussion." **Steve approved the
exception (2026-07-07)** to use audited OIDC/JOSE libraries rather than hand-roll
JWT verification (the safer choice for verifying externally-controlled tokens):

- `github.com/coreos/go-oidc/v3` — OIDC discovery + ID-token verification.
- `golang.org/x/oauth2` — Authorization-Code exchange (x-family).
- `github.com/go-jose/go-jose/v4` — transitive (JWS/JWKS).

`govulncheck`/`gosec` run over these as usual; pin to current clean versions.
**Action:** add an "OIDC libraries" carve-out note to CLAUDE.md's crypto-deps
rule so future agents know this was a considered decision, not drift.

## Identity model & schema (migration `000007`)

### `oidc_providers`

| Column | Type | Notes |
|---|---|---|
| `id` | uuid PK (`gen_random_uuid()`) | |
| `name` | text, unique | operator label, e.g. `default` |
| `issuer` | text | OIDC issuer URL; go-oidc discovery key |
| `client_id` | text | |
| `wrapped_client_secret` | bytea | master-key-wrapped (AAD `janus:auth:oidc-client-secret`) |
| `scopes` | text[] | default `{openid,email,profile}` |
| `redirect_url` | text | **explicit** public callback URL (never derived from the `Host` header — anti-spoofing), e.g. `https://janus.example.com/v1/auth/oidc/callback` |
| `enabled` | bool | when false, `status` reports unavailable and `login` 404s |
| `created_at` / `updated_at` | timestamptz | |

### `oidc_identities`

| Column | Type | Notes |
|---|---|---|
| `id` | uuid PK | |
| `user_id` | uuid FK → users (ON DELETE CASCADE) | |
| `issuer` | text | |
| `subject` | text | the IdP's stable `sub` |
| `created_at` / `last_login_at` | timestamptz | |

Unique `(issuer, subject)`. This is the durable link — `email` is used only to
match a pre-provisioned user on first login, then never trusted for identity
(emails can be reassigned; `sub` cannot).

### `oidc_auth_requests`

| Column | Type | Notes |
|---|---|---|
| `state` | text PK | random 32-byte base64url; the callback's `state` param |
| `nonce` | text | random; bound into the ID token, checked on callback |
| `pkce_verifier` | text | random; S256 challenge sent to the IdP |
| `provider_id` | uuid FK → oidc_providers | which provider this request targets |
| `created_at` / `expires_at` | timestamptz | short TTL (e.g. 10 min) |

Consumed single-use on callback (deleted). Expired rows swept opportunistically
on lookup + at boot (same discipline as `sessions`). Values here are not
credentials, but they are single-use CSRF/replay guards.

## Flow

### Initiate — `GET /v1/auth/oidc/login`

1. Load the enabled provider (404 `oidc_not_configured` if none/disabled).
2. Generate `state`, `nonce`, PKCE `verifier` (+ S256 `challenge`); insert an
   `oidc_auth_requests` row (TTL 10 min).
3. Build the provider's authorization URL (endpoint from cached discovery) with
   `client_id`, `redirect_url`, `scope`, `state`, `nonce`, `code_challenge`,
   `code_challenge_method=S256`.
4. **302** to that URL. (No cookie needed — state lives server-side, keyed by the
   `state` param the IdP echoes back.)

### Callback — `GET /v1/auth/oidc/callback?code&state`

1. If the IdP returned `error`, render a terminal auth-error (no internals).
2. Look up + **delete** the `oidc_auth_requests` row by `state` (missing/expired →
   `invalid_oidc_state`, 400). This makes state single-use.
3. Exchange `code` via `x/oauth2` (`redirect_url` + PKCE `verifier` + client_secret
   unwrapped just-in-time, then zeroized).
4. **go-oidc verifies** the returned ID token: JWKS signature, `iss` == configured
   issuer, `aud` contains `client_id`, `exp`/`iat`, and **`nonce`** == the stored
   nonce. Extract claims `sub`, `email`, `email_verified`.
5. Resolve the user (see Resolution). On success, mint a session via the existing
   `auth.Service` session path → `Set-Cookie: janus_session` → **302 to `/`**
   (the SPA). On failure → terminal auth-error page, audited as a denied login.

### Resolution (pre-provisioned policy)

```
link := identities.Get(iss, sub)
if link != nil:
    user := users.Get(link.user_id)
    if user.disabled -> deny
    identities.TouchLastLogin(link); return user
else:
    if !email_verified -> deny            # can't trust email to match
    user := users.GetByEmail(email)       # case-insensitive, existing only
    if user == nil -> deny                # NO auto-provision
    if user.disabled -> deny
    identities.Create(user.id, iss, sub); return user
```

All denials return one indistinguishable terminal error to the browser (no
account-existence oracle) and an audited `denied` login event server-side.

## HTTP surface

New error codes: `oidc_not_configured` (404), `invalid_oidc_state` (400),
`oidc_exchange_failed` (401/502 as appropriate, no internals), `oidc_denied` (the
resolution failures, one shape). Provider-config errors reuse existing envelope
codes.

| Route | Auth | Behavior |
|---|---|---|
| `GET /v1/auth/oidc/status` | none; unsealed | `200 {"enabled":bool,"name":string}` — the SPA's "Sign in with OIDC" button gate; no secrets |
| `GET /v1/auth/oidc/login` | none; rate-limited; unsealed | 302 → IdP authorize URL, or 404 if unconfigured |
| `GET /v1/auth/oidc/callback` | none; rate-limited; unsealed | verify + mint session + 302 → `/`, or terminal auth-error |
| `GET /v1/sys/oidc` | `oidc:manage` | `200 {name,issuer,client_id,scopes,redirect_url,enabled,secret_set:bool}` — **never** the secret |
| `PUT /v1/sys/oidc` | `oidc:manage` | upsert `{name,issuer,client_id,client_secret,scopes,redirect_url,enabled}`; wraps + stores the secret; validates issuer discovery reachable; audited |
| `DELETE /v1/sys/oidc` | `oidc:manage` | remove the provider; audited |

`oidc:manage` is a new instance-scoped action (admin + owner) added to
`internal/authz` alongside the existing instance actions; deny-by-default.
Login/callback/status all sit behind the existing `RequireUnsealed` like the
rest of `/v1/auth/*` — login/callback need to unwrap the secret and mint a
session, and `status` only matters on the login screen, which the SPA shows
*after* unseal (while sealed it shows the unseal screen). `status` exposes only
`{enabled,name}`.

### Wiring

`api.Boot` constructs the OIDC pieces alongside `auth.NewService` and injects the
`*crypto.Keyring` (already available) so the client-secret can be unwrapped at
login. Discovery/JWKS are fetched lazily and cached in-process (go-oidc's
provider/verifier), refreshed on issuer/config change. A per-provider verifier is
rebuilt when `PUT /v1/sys/oidc` changes the issuer or client_id.

## Security posture

- **CSRF / replay:** single-use, expiring `state`; single-use `nonce` bound into
  the ID token and checked; PKCE S256; `code` single-use (IdP-enforced).
- **Token validation:** signature via JWKS, `iss`/`aud`/`exp`/`iat`/`nonce` all
  enforced by go-oidc (rejects `alg=none`, no RS256/HS256 confusion).
  `email_verified` required before an email is ever used to match a user.
- **No secret at rest in the clear:** client_secret master-key-wrapped; unwrapped
  just-in-time, zeroized after the exchange; `GET /v1/sys/oidc` returns
  `secret_set` only; never logged, never in errors, never audited.
- **No enumeration:** all resolution denials return one browser-terminal error.
- **Sealed coherence:** login/callback 503 while sealed (consistent with all auth
  — verification needs the unwrapped secret + the HMAC key to mint the session).
- **redirect_url** is explicit config, never taken from the request Host.
- **Audit:** login success → `auth.login` (method `oidc`, resolved user actor);
  login failure → `denied` (anonymous); provider config write/delete → audited
  fail-closed recording issuer + client_id, **never** the secret. A leak test
  asserts the client_secret and any token never reach logs, errors, or audit rows.

## Testing

- **Mock IdP harness:** an httptest server exposing `/.well-known/openid-configuration`,
  a JWKS built from a generated RSA (and one ES256) key, and a token endpoint that
  returns a signed ID token for a scripted `sub`/`email`/`email_verified`/`nonce`.
  go-oidc points at it. Enables deterministic full-flow e2e.
- **Flow e2e (against the mock):** login→callback happy path issues a working
  session; wrong/expired/replayed `state`; nonce mismatch; bad signature; wrong
  `aud`/`iss`; expired token — each denied with the right code and no session.
- **Resolution matrix (unit):** by-`sub` link hit; first-login email match creates
  the link; unknown email → deny; `email_verified=false` → deny; disabled user →
  deny.
- **Crypto:** `WrapOIDCClientSecret`/`Unwrap…` round-trip + tamper + AAD
  injectivity; `internal/crypto` stays 100%.
- **Config API:** `oidc:manage` RBAC matrix (viewer/developer denied; admin/owner
  allowed); `GET` omits the secret; `PUT` validates issuer discovery; audited.
- **Leak test:** canary client_secret + a token pushed through login/config paths
  never appear in captured logs, error bodies, or `audit_events`.
- **Gates:** `go build`/`vet`/`go test ./...` (testcontainers-backed suites run),
  `gosec` (shamir excluded) 0 issues, `govulncheck` clean (now covering
  go-oidc/go-jose/oauth2), `internal/crypto` 100%. Toolchain pinned `go1.26.4`.

## Verification gates

Same bar as prior milestones. Isolated worktree; when complete, finish via a PR
to `main` (coordinated so it doesn't collide with the UI agent's merges — Go-only
diff, so conflict surface is limited to `go.mod`/`go.sum`, `migrations/`, and
`internal/api` route wiring).
