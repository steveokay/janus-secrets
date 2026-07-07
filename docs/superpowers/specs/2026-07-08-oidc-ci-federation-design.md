# OIDC CI Federation (sub-project C2) — design

**Status:** approved 2026-07-08. Follows sub-project C1 (OIDC human login,
`docs/oidc.md`), reusing its go-oidc / JWKS verification path. Go-backend only.

## Goal

Let a CI job authenticate to Janus with its provider-issued OIDC token instead
of a stored long-lived secret. A GitHub Actions workflow presents its ephemeral
ID token; Janus verifies it, matches its claims against a pre-registered trust
binding, and returns a **short-lived, scoped `janus_svc_` service token**. This
removes long-lived Janus credentials from CI configuration.

## Architecture & reuse

The credential Janus hands back is an ordinary **service token** minted through
the existing `auth.MintServiceToken` path (already supports scope, access, TTL
via `ExpiresAt`, HMAC-at-rest, listing, and revocation). No new credential type
and no new authorization path: the CI token is verified and scoped exactly like
any other service token in downstream requests.

Verification reuses C1's pattern — a cached go-oidc provider + `IDTokenVerifier`
built from a configured issuer, discovering JWKS from the issuer's well-known
endpoint. C2 points that machinery at the **federation issuer** (GitHub Actions'
`https://token.actions.githubusercontent.com`) with a **required audience**,
rather than at the human-login provider.

New code:
- `internal/auth/oidc_federation.go` — federation config/bindings CRUD, the
  claim-matching resolver, and `FederateCILogin` (verify → match → mint).
- `internal/auth/tokens.go` — a `MintFederatedToken` path (see *Minting*).
- `internal/store/oidc_federation.go` — repos for the config row and bindings;
  migration `000009_oidc_federation`.
- `internal/api/oidc_federation_handlers.go` — the exchange endpoint + admin
  config/binding routes; wired in `server.go`.

## Data model (migration `000009_oidc_federation`)

**`oidc_federation_config`** — single row (like `oidc_providers`):

| Column | Notes |
|---|---|
| `id` | uuid PK |
| `issuer` | default `https://token.actions.githubusercontent.com` |
| `audience` | required, non-empty; exact-matched against the token `aud` |
| `enabled` | bool; exchange returns "not configured" when false/absent |
| `created_at`, `updated_at` | timestamps |

**`oidc_federation_bindings`** — the trust policies:

| Column | Notes |
|---|---|
| `id` | uuid PK |
| `name` | unique, human label |
| `match_claims` | JSONB object `{claim: value}`; all entries AND-ed, exact string equality. **Must include `repository`.** |
| `scope_kind` | `config` \| `environment` |
| `scope_id` | uuid of the target config/environment |
| `access` | `read` \| `readwrite` |
| `ttl_seconds` | minted-token lifetime; validated `1 ≤ ttl ≤ max` at config time |
| `enabled` | bool |
| `created_at`, `updated_at` | timestamps |

**`service_tokens`** change: `created_by` becomes **nullable**, and a nullable
`federation_binding` uuid column is added (FK to `oidc_federation_bindings`,
`ON DELETE SET NULL`) recording which binding minted a federated token. Existing
user-minted tokens keep a non-null `created_by`; deleting a binding does not
delete already-minted tokens (they simply expire).

## Exchange flow

`POST /v1/auth/oidc/federate` — public (the JWT *is* the credential), behind
`RequireUnsealed` (minting needs the unsealed token-HMAC key), rate-limited via
the existing login limiter.

Request: `{"token": "<github-actions-oidc-jwt>"}` (the workflow obtains it from
`ACTIONS_ID_TOKEN_REQUEST_URL` with `audience=<configured audience>`).

Steps:
1. Load the enabled federation config; if none, return the single indistinguishable
   denial (see *Errors*).
2. Verify the JWT with the cached go-oidc verifier for the configured issuer:
   JWKS signature, `iss`, `exp`, and `aud` **exactly equal** to the configured
   audience.
3. Extract claims into a `map[string]string` view (`repository`, `ref`,
   `environment`, `sub`, `repository_owner`, `job_workflow_ref`, …).
4. Resolve the binding: over all **enabled** bindings, keep those whose every
   `match_claims` entry equals the token's corresponding claim. Require **exactly
   one** survivor (see *Safety rules*).
5. Mint a service token for the binding's `scope_kind`/`scope_id`/`access` with
   `ttl = min(binding.ttl_seconds, max_ttl)`, attributed to the binding.
6. Audit success; respond `200` with
   `{"token":"janus_svc_…","expires_at":"<rfc3339>","scope":{"kind":…,"id":…,"access":…}}`.

Response `token` is shown exactly once (like any mint); only its HMAC is stored.

## Trust-matching safety rules (non-negotiable)

- **`repository` required:** a binding whose `match_claims` lacks a non-empty
  `repository` is rejected at config time (`PUT`/`POST` → 400). Prevents
  owner-wide or claim-less bindings that would over-match.
- **Exactly one match:** zero matches → deny; more than one match → deny as
  *ambiguous*. No "most-specific wins" resolution — ambiguity is an admin error,
  not something the server guesses through.
- **Audience required & exact:** empty configured audience is invalid; the token
  `aud` must equal it exactly. Blocks replay of a token minted for another service.
- **TTL cap:** `max_ttl` = **1h** (server constant), default **15m** when a
  binding omits `ttl_seconds`. A binding above the cap is rejected at config time.
- **Enabled gates:** a disabled config or disabled binding never mints.

## Minting (the `created_by` wrinkle)

`MintServiceToken` today requires `by.Kind == KindUser` because `created_by`
references `users`. A federated mint has no human minter, so C2 adds
`MintFederatedToken(ctx, name, scopeKind, scopeID, access, ttl, bindingID)`
which stores `created_by = NULL` and `federation_binding = bindingID`, reusing
the same random-token + HMAC + `tokens.Create` core (the store `Create` signature
gains the nullable minter + binding, with the user-facing mint passing its user
id and a nil binding). Everything else — verify, expiry, revoke, list — is shared.

## Admin config API

All under `/v1/sys/oidc/federation`, gated by the existing **`oidc:manage`**
instance action (admin/owner), audited, denials fail-closed via `requireInstance`
(consistent with C1's `/v1/sys/oidc`; a federated exchange is still just a scoped
service token, so a separate action would add surface without adding safety):

| Method & path | Behavior |
|---|---|
| `GET /v1/sys/oidc/federation` | Config view: `issuer, audience, enabled` (+ timestamps). |
| `PUT /v1/sys/oidc/federation` | Upsert config. Validates non-empty audience. Audited `oidc.federation.config.write`. |
| `DELETE /v1/sys/oidc/federation` | Remove config (204). Audited. |
| `GET /v1/sys/oidc/federation/bindings` | List bindings. |
| `POST /v1/sys/oidc/federation/bindings` | Create a binding. Validates `repository` present, scope exists, access valid, ttl ≤ cap. Audited `oidc.federation.binding.write`. |
| `DELETE /v1/sys/oidc/federation/bindings/{id}` | Remove a binding (204). Audited. |

The federation config carries no secret (unlike C1's client secret) — GitHub
Actions is a public-key/JWKS trust relationship, so there is nothing to wrap.

## Audit, errors, security invariants

- Every exchange writes an audit event — success or denial — recording the
  matched binding name, `repository`, and `sub` (stable identifiers). The **raw
  JWT is never logged or audited**; a leak test enforces this over logs + audit
  rows (mirrors C1's `TestOIDCClientSecretNeverLeaks`).
- All exchange failures (not configured, bad signature/iss/aud/exp, no match,
  ambiguous match) return **one indistinguishable** `federation_denied` (401)
  error to the caller; the server-side audit records the real reason. Config-time
  validation errors on the admin routes are ordinary `400 validation` (not
  security-sensitive).
- Minted tokens appear in `ListTokens`, are revocable, and expire; the short TTL
  bounds blast radius even without explicit revocation.
- Constant-time HMAC comparison (reused) for the eventual token verification.
- Exchange behind `RequireUnsealed` (503 while sealed) and rate-limited.

## Testing

Reuse C1's in-package mock IdP harness (RS256 + JWKS), extended to emit
GitHub-Actions-shaped claims (`repository`, `ref`, `environment`, `sub`):

- Happy path: valid token + single matching binding → scoped token with the
  expected `expires_at` and scope; the token then authorizes a scoped read.
- Wrong audience → denied. Expired token → denied. Bad signature → denied.
- No matching binding → denied. **Ambiguous** (two enabled bindings match) →
  denied, and audited as ambiguous.
- TTL cap: binding `ttl_seconds > max` rejected at config time; a binding under
  the cap yields `expires_at` at that TTL.
- Config validation: a binding without `repository` rejected; empty audience
  rejected.
- RBAC: non-owner denied on every `/v1/sys/oidc/federation[...]` route.
- Leak test: raw JWT absent from logs and every `audit_events` row.
- Store repos and the claim-matcher get focused table-driven unit tests
  (matcher: subset match, missing claim, extra token claims ignored, value
  mismatch, disabled binding skipped).

Gates (same as every milestone): `go build`/`go vet`/`go test ./... -count=1`
(Docker/testcontainers), `internal/crypto` 100% (untouched), `gosec`
(shamir-excluded) 0, `govulncheck` 0 affecting.

## Scope boundaries / non-goals

- **GitHub Actions is the shipped, tested issuer.** The issuer is configurable,
  so pointing at GitLab CI or a generic OIDC CI provider is a config change, not
  new code — but only GitHub-shaped claim matching (`repository`, `environment`,
  …) is exercised in this milestone.
- No dynamic secrets, no auto-provisioning: bindings are pre-registered by an
  admin; an unmatched identity gets nothing.
- No change to how downstream requests authorize — a federated token is a normal
  scoped service token.
- This is one focused subsystem → one implementation plan.
