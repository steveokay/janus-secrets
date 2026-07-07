# OIDC login

Janus lets human operators sign in to the web UI through an external OpenID
Connect provider instead of an email + password. This is sub-project **C1** —
human login. Machine identity for CI (C2, GitHub Actions JWT exchange) is a
separate follow-up slice and is not covered here.

Because Janus is single-tenant, exactly **one** OIDC provider is configured at a
time. The implementation targets any generic, spec-compliant OIDC provider; it
is exercised in tests against a mock IdP and designed for GitHub and Google.

An OIDC user is an ordinary Janus user: after a successful login they receive
the same `janus_session` cookie a password login issues, and RBAC treats them
identically. OIDC changes *how* a human authenticates, not *what* they are.

## Flow

Login is the **Authorization Code** flow hardened with **PKCE** (S256),
**state**, and **nonce**:

1. The SPA calls `GET /v1/auth/oidc/status`. If it returns `{"enabled":true}`,
   the login screen shows a "Sign in with OIDC" button.
2. The button is a full-page navigation to `GET /v1/auth/oidc/login`. The server
   generates a single-use `state` (CSRF defense), a `nonce` (ID-token replay
   defense), and a PKCE verifier, persists them server-side (in
   `oidc_auth_requests`, keyed by `state`, with a short expiry), sets the `state`
   in a short-lived `HttpOnly`, `SameSite=Lax` cookie (`janus_oidc_state`,
   scoped to `/v1/auth/oidc`) that **binds the flow to this browser**, and
   redirects the browser (302) to the provider's authorize URL carrying the
   state, nonce, and PKCE S256 challenge.
3. The user authenticates at the provider, which redirects back to
   `GET /v1/auth/oidc/callback?code=…&state=…`.
4. The callback first requires the `janus_oidc_state` cookie to be present and
   equal (constant-time) to the query `state` — proving the same browser began
   the flow — then consumes the `state` row (single-use; deleted on read,
   expired rows rejected), exchanges the `code` for tokens using the stored PKCE
   verifier, and verifies the ID token: issuer, audience (client_id), signature
   (against the provider's JWKS), and that the ID-token `nonce` matches the one
   stored for this state.
5. The callback requires `email_verified` to be true, matches the token's email
   to a **pre-provisioned** Janus user, links the identity by the stable
   `(issuer, subject)` pair, sets the `janus_session` cookie (24h), and redirects
   (302) to `/`.

Any failure — bad/expired state, exchange failure, verification failure,
unverified email, or no matching user — returns a single indistinguishable error
so the callback cannot be used to enumerate accounts.

## Endpoints

### Public (under `/v1/auth`, no auth, behind `RequireUnsealed`, rate-limited)

| Method & path | Behavior |
|---|---|
| `GET /v1/auth/oidc/status` | `{"enabled": bool}` — whether a provider is configured **and** enabled. |
| `GET /v1/auth/oidc/login` | 302 to the provider authorize URL (sets state/nonce/PKCE). `404 oidc_not_configured` if no enabled provider. |
| `GET /v1/auth/oidc/callback?code=&state=` | Verifies and completes login; sets `janus_session`; 302 to `/`. Single indistinguishable error on any failure. |

Because login and callback sit behind `RequireUnsealed`, they return **503**
while the server is sealed — OIDC login cannot succeed until the master key is
unsealed (the callback must unwrap the client secret and mint a session).

### Admin (under `/v1/sys`, gated by the `oidc:manage` instance action)

`oidc:manage` is an instance-scoped action held by **admin** and **owner**.
Denials are audited fail-closed by the `requireInstance` middleware.

| Method & path | Behavior |
|---|---|
| `GET /v1/sys/oidc` | Provider view: `name, issuer, client_id, scopes, redirect_url, enabled, secret_set`. **Never** returns the client secret. |
| `PUT /v1/sys/oidc` | Upsert the provider. Success audited as `oidc.config.write`, recording issuer + client_id only. |
| `DELETE /v1/sys/oidc` | Remove the provider (204). Audited `oidc.config.delete`. |

## Provider configuration

`PUT /v1/sys/oidc` accepts:

| Field | Notes |
|---|---|
| `name` | Human label for the provider. |
| `issuer` | OIDC issuer URL; discovery (`/.well-known/openid-configuration`) and JWKS are resolved from it. |
| `client_id` | OAuth client id; also the expected ID-token audience. |
| `client_secret` | Write-only. AES-256-GCM wrapped under the master key (AAD `janus:auth:oidc-client-secret`) and stored as `wrapped_client_secret`. Never logged, returned, or audited. |
| `scopes` | Requested scopes (openid + email + profile). |
| `redirect_url` | Explicit callback URL registered with the provider (points at `/v1/auth/oidc/callback`). |
| `enabled` | Whether login is live. `status` reports `false` until this is true. |

The client secret is only readable while the keyring is unsealed. `GET` reports
its presence as `secret_set: true|false` and never the value itself; to change it
you re-`PUT` a new secret.

## User mapping policy — pre-provisioned only

Janus does **not** auto-provision accounts from OIDC. A user must already exist
in Janus (created by an admin) before they can log in through OIDC.

- On first OIDC login, the verified `email` from the ID token is matched to an
  existing Janus user; that match creates an `oidc_identities` row linking the
  user to the stable `(issuer, subject)`.
- Subsequent logins resolve directly by `(issuer, subject)` — email changes at
  the provider do not break the link, and the subject (not the email) is the
  durable identifier.
- `email_verified` must be true; an unverified email is rejected.
- No matching user → the same single indistinguishable denial as any other
  callback failure (no account enumeration).

## Storage

Migration `000007_oidc` adds three tables:

- `oidc_providers` — the single provider row, including `wrapped_client_secret`.
- `oidc_identities` — `(user_id, issuer, subject)` with `UNIQUE(issuer, subject)`
  and `last_login_at`.
- `oidc_auth_requests` — in-flight login state: `state` (PK), `nonce`,
  `pkce_verifier`, `provider_id`, `expires_at`. Rows are single-use (deleted on
  callback) and expiring; a boot-time sweep clears any orphaned by a crash.

## Security properties

- The client secret is never logged, returned, or written to an audit entry;
  config-write audit records only issuer + client_id. A dedicated leak test
  (`TestOIDCClientSecretNeverLeaks`) drives a full configure + login flow with a
  canary secret and asserts it appears in no log line, no response body, and no
  `audit_events` row.
- `state` rows are single-use and expiring, sweeping CSRF and replay windows;
  expired rows are also cleared at boot. The `state` is additionally bound to the
  initiating browser via the `HttpOnly` `janus_oidc_state` cookie and checked
  constant-time at the callback, so a captured callback URL cannot be replayed in
  a victim's browser to fixate them into the attacker's account (login-CSRF /
  session-fixation defense, RFC 9700 §4.7).
- ID-token verification (issuer, audience, JWKS signature, nonce) uses the
  audited `go-oidc` / `x-oauth2` / `go-jose` libraries rather than hand-rolled
  JOSE (see the crypto-lib exception in `CLAUDE.md`).

## UI handoff

For the SPA / UI agent:

- **Login screen:** show a "Sign in with OIDC" button **only** when
  `GET /v1/auth/oidc/status` returns `{"enabled":true}`. The button must be a
  **full-page navigation** to `GET /v1/auth/oidc/login` — never a `fetch`/XHR,
  because the endpoint returns a 302 redirect chain that ends by setting the
  session cookie in the browser.
- **Admin settings:** an "OIDC provider" screen backed by
  `GET/PUT/DELETE /v1/sys/oidc`. The form exposes a **write-only** client-secret
  field; `GET` renders `secret_set` as "a secret is set" (or similar) without
  ever revealing the value. These endpoints require an admin/owner session.

## Follow-up

Sub-project **C2** — OIDC-federated CI machine identity (GitHub Actions JWT
exchange → scoped short-lived credential) — is **implemented**, reusing this same
generic OIDC verification path. See [`docs/ci-federation.md`](ci-federation.md).
