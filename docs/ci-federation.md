# CI federation (OIDC machine identity)

CI federation lets a CI job authenticate to Janus using its provider-issued
OIDC token instead of a stored long-lived secret. This is sub-project **C2** —
machine identity. Human sign-in (C1) is covered in [`docs/oidc.md`](oidc.md).

A CI job presents its ephemeral OIDC token; Janus verifies it, matches its
claims against a pre-registered **trust binding**, and returns a **short-lived,
scoped `janus_svc_` service token**. That token is an ordinary service token —
it authorizes downstream requests exactly like any other, appears in the token
list, is revocable, and expires on its own. Nothing long-lived is stored in CI.

GitHub Actions is the shipped, tested provider. The federation issuer is
configurable, so pointing at another OIDC CI provider is a config change rather
than new code — but only GitHub-Actions-shaped claim matching is exercised in
this milestone.

## Flow

The exchange is verify → match → mint:

1. The CI job requests an OIDC token from its provider **with the audience Janus
   is configured to require** (for GitHub Actions, the `audience` input on the
   token request).
2. The job `POST`s the token to `POST /v1/auth/oidc/federate`.
3. Janus verifies the token against the configured federation issuer: JWKS
   signature, `iss`, `exp`, and `aud` **exactly equal** to the configured
   audience (a token minted for another audience is rejected).
4. Janus projects the verified claims to their string values and finds the
   **single** enabled binding whose every `match_claims` entry equals the token's
   claim. Zero matches → denied; more than one → denied as ambiguous.
5. Janus mints a service token for the matched binding's scope/access with a TTL
   of `min(binding.ttl_seconds, 1h)`, and returns it.

Every failure — not configured, bad signature/issuer/audience/expiry, no match,
ambiguous match — returns one indistinguishable `federation_denied` (401); the
server-side audit records the real reason.

## Endpoints

### Exchange (public, under `/v1/auth`, behind `RequireUnsealed`, rate-limited)

| Method & path | Behavior |
|---|---|
| `POST /v1/auth/oidc/federate` | Body `{"token":"<oidc-jwt>"}`. On success `200` with `{"token":"janus_svc_…","expires_at":"<rfc3339>","scope":{"kind":…,"id":…,"access":…}}`. Any failure → `401 federation_denied`. |

Because it sits behind `RequireUnsealed`, the endpoint returns **503** while the
server is sealed (minting needs the unsealed token-HMAC key).

### Admin config (under `/v1/sys`, gated by the `oidc:manage` instance action)

`oidc:manage` is held by **admin** and **owner** (the same action that gates C1's
`/v1/sys/oidc`); denials are audited fail-closed.

| Method & path | Behavior |
|---|---|
| `GET /v1/sys/oidc/federation` | Provider config: `issuer, audience, enabled`. |
| `PUT /v1/sys/oidc/federation` | Upsert config. Validates non-empty audience. Audited `oidc.federation.config.write`. |
| `DELETE /v1/sys/oidc/federation` | Remove config (204). Audited. |
| `GET /v1/sys/oidc/federation/bindings` | List bindings. |
| `POST /v1/sys/oidc/federation/bindings` | Create a binding. Audited `oidc.federation.binding.write`. |
| `DELETE /v1/sys/oidc/federation/bindings/{id}` | Remove a binding (204). Audited. |

The federation config holds **no secret** — GitHub Actions is a public-key /
JWKS trust relationship, so there is nothing to wrap.

## Provider config

`PUT /v1/sys/oidc/federation`:

| Field | Notes |
|---|---|
| `issuer` | OIDC issuer; discovery + JWKS resolved from it. Empty defaults to `https://token.actions.githubusercontent.com`. |
| `audience` | Required, non-empty. The token's `aud` must equal this exactly. |
| `enabled` | Whether the exchange is live. |

## Trust bindings

Each binding maps a set of claim conditions to a scope + access + TTL. Fields:

| Field | Notes |
|---|---|
| `name` | Unique label. |
| `match_claims` | JSON object `{claim: value}`; **all** entries must match the token's claims (exact string equality, AND-ed). **Must include a non-empty `repository`.** |
| `scope_kind` | `config` or `environment`. |
| `scope_id` | The target config/environment id (must exist). |
| `access` | `read` or `readwrite`. |
| `ttl_seconds` | Minted-token lifetime; `1 ≤ ttl ≤ 3600`. Omitting it (0) defaults to 900 (15m). |
| `enabled` | Whether the binding can mint. |

GitHub Actions tokens carry claims like `repository` (`org/app`), `ref`
(`refs/heads/main`), `environment` (`prod`), `repository_owner`,
`job_workflow_ref`, and `sub`. Match on `repository` plus whatever narrows the
grant (e.g. `environment`), following GitHub's own hardening guidance.

### Safety rules (non-negotiable)

- **`repository` is required** on every binding — an owner-wide or claim-less
  binding is rejected at config time.
- **Exactly one binding must match.** Zero or multiple → denied. There is no
  "most-specific wins" resolution; ambiguity is an admin error.
- **Audience is required and exact-matched.**
- **TTL is capped at 1h**, default 15m; a binding over the cap is rejected at
  config time.

## Security properties

- The raw CI JWT is a bearer credential: it is never logged, echoed, or written
  to an audit entry. A leak test (`TestOIDCFederationJWTNeverLeaks`) drives a
  full exchange and asserts the raw JWT and the minted token appear in no log
  line and no `audit_events` row. Success audits record only `binding`,
  `repository`, and `sub`; denials record only the reason.
- All exchange failures are indistinguishable to the caller.
- Minted tokens are short-lived and revocable; the TTL bounds blast radius even
  without explicit revocation.
- Federated tokens have no human minter — `service_tokens.created_by` is NULL and
  `federation_binding` records the minting binding (forensics + FK integrity).

## GitHub Actions example

Configure the provider and a binding (as an admin), then in a workflow:

```yaml
permissions:
  id-token: write   # allow the job to request an OIDC token

jobs:
  deploy:
    runs-on: ubuntu-latest
    steps:
      - name: Get a Janus token
        id: janus
        run: |
          # Request a GitHub OIDC token with the audience Janus requires.
          RESP=$(curl -sS \
            "$ACTIONS_ID_TOKEN_REQUEST_URL&audience=janus" \
            -H "Authorization: Bearer $ACTIONS_ID_TOKEN_REQUEST_TOKEN")
          JWT=$(echo "$RESP" | jq -r '.value')

          # Exchange it for a short-lived scoped Janus token.
          TOKEN=$(curl -sS -X POST https://janus.internal/v1/auth/oidc/federate \
            -H 'Content-Type: application/json' \
            -d "{\"token\":\"$JWT\"}" | jq -r '.token')
          echo "::add-mask::$TOKEN"
          echo "token=$TOKEN" >> "$GITHUB_OUTPUT"

      - name: Use it
        env:
          JANUS_TOKEN: ${{ steps.janus.outputs.token }}
        run: janus run -- ./deploy.sh
```

The binding for this workflow would be, for example:

```json
{
  "name": "prod-deploy",
  "match_claims": { "repository": "org/app", "environment": "prod" },
  "scope_kind": "config",
  "scope_id": "<prod config id>",
  "access": "read",
  "ttl_seconds": 900,
  "enabled": true
}
```
