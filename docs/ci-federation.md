# CI federation (OIDC machine identity)

CI federation lets a CI job authenticate to Janus using its provider-issued
OIDC token instead of a stored long-lived secret. This is sub-project **C2** —
machine identity. Human sign-in (C1) is covered in [`docs/oidc.md`](oidc.md).

A CI job presents its ephemeral OIDC token; Janus verifies it, matches its
claims against a pre-registered **trust binding**, and returns a **short-lived,
scoped `janus_svc_` service token**. That token is an ordinary service token —
it authorizes downstream requests exactly like any other, appears in the token
list, is revocable, and expires on its own. Nothing long-lived is stored in CI.

Four CI OIDC providers are supported out of the box: **GitHub Actions**,
**GitLab CI/CD**, **Buildkite**, and **CircleCI**. The federation issuer is a
config value and **one provider is active at a time** (a single
`FederationConfig`); switching providers is a config change, not new code. The
mandatory-claim rule is provider-aware — see [Trust bindings](#trust-bindings).
Binding multiple providers simultaneously (each binding carrying its own issuer)
is a possible future follow-up; today, point the single issuer at whichever CI
provider you federate from.

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
5. Janus mints a service token for the matched binding's scope/access with the
   binding's `ttl_seconds` (capped at 1h **when the binding was created**, not at
   mint time — see Safety rules below), and returns it.

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
| `issuer` | OIDC issuer; discovery + JWKS resolved from it. Must be a well-formed absolute `http(s)` URL. Empty defaults to `https://token.actions.githubusercontent.com`. |
| `audience` | Required, non-empty. The token's `aud` must equal this exactly. |
| `enabled` | Whether the exchange is live. |

Known issuer URLs:

| Provider | Issuer |
|---|---|
| GitHub Actions | `https://token.actions.githubusercontent.com` |
| GitLab CI/CD (SaaS) | `https://gitlab.com` (self-hosted: your instance base URL) |
| Buildkite | `https://agent.buildkite.com` |
| CircleCI | `https://oidc.circleci.com/org/<ORG_ID>` (org-specific) |

The web UI (Integrations → CI federation) offers these as a **provider preset**
dropdown that fills the issuer URL and hints the claim to bind, so admins don't
hand-type issuer URLs.

## Trust bindings

Each binding maps a set of claim conditions to a scope + access + TTL. Fields:

| Field | Notes |
|---|---|
| `name` | Unique label. |
| `match_claims` | JSON object `{claim: value}`; **all** entries must match the token's claims (exact string equality, AND-ed). **Must constrain at least one strong identifying claim for the configured issuer** (see below). |
| `scope_kind` | `config` or `environment`. |
| `scope_id` | The target config/environment id (must exist). |
| `access` | `read` or `readwrite`. |
| `ttl_seconds` | Minted-token lifetime; `1 ≤ ttl ≤ 3600`. Omitting it (0) defaults to 900 (15m). |
| `enabled` | Whether the binding can mint. |

#### Provider-aware required claim

The old rule required a literal `repository` claim, which only fits GitHub. The
requirement is now **provider-aware**: a binding must constrain at least one
**strong identifying claim** appropriate to the configured issuer. Which claim
counts as strong depends on the issuer:

| Issuer | Required strong claim | Recommended extra narrowing |
|---|---|---|
| `https://token.actions.githubusercontent.com` (GitHub) | `repository` | `environment`, `ref` |
| `https://gitlab.com` (GitLab) | `project_path` | `ref`, `ref_type`, `environment` |
| `https://agent.buildkite.com` (Buildkite) | `organization_slug` | `pipeline_slug`, `build_branch` |
| `https://oidc.circleci.com/org/<ORG_ID>` (CircleCI) | `oidc.circleci.com/project-id` (or `aud`) | project/context claims |
| any other / self-hosted / custom | *any single non-empty match claim* | — |

For an unknown or self-hosted issuer, the rule falls back to "at least one
non-empty match claim is required" — a claim-less binding is always rejected.

### Safety rules (non-negotiable)

- **A strong identifying claim is required** on every binding (provider-aware,
  per the table above) — an owner-wide or claim-less binding is rejected at
  config time. Empty match-claim *values* are also rejected (they would match
  tokens that lack the claim entirely).
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

## GitLab CI/CD example

- **Issuer:** `https://gitlab.com` (or your self-hosted GitLab base URL).
- **Audience:** whatever Janus is configured to require (e.g. `janus`).
- **Bind:** `project_path` (GitLab's `org/group/project`), optionally narrowed by
  `ref` / `ref_type` / `environment`.

GitLab injects an ID token when you declare it in `id_tokens`:

```yaml
deploy:
  id_tokens:
    JANUS_JWT:
      aud: janus            # must equal Janus's configured audience
  script:
    - >
      TOKEN=$(curl -sS -X POST https://janus.internal/v1/auth/oidc/federate
      -H 'Content-Type: application/json'
      -d "{\"token\":\"$JANUS_JWT\"}" | jq -r '.token')
    - JANUS_TOKEN="$TOKEN" janus run -- ./deploy.sh
```

Binding:

```json
{
  "name": "gitlab-prod",
  "match_claims": { "project_path": "acme/atlas-api", "ref": "main" },
  "scope_kind": "config", "scope_id": "<prod config id>",
  "access": "read", "ttl_seconds": 900, "enabled": true
}
```

## Buildkite example

- **Issuer:** `https://agent.buildkite.com`.
- **Audience:** Janus's configured audience.
- **Bind:** `organization_slug`, and recommend also binding `pipeline_slug`.

Buildkite mints an OIDC token via `buildkite-agent`:

```yaml
steps:
  - command: |
      JWT=$(buildkite-agent oidc request-token --audience janus)
      TOKEN=$(curl -sS -X POST https://janus.internal/v1/auth/oidc/federate \
        -H 'Content-Type: application/json' \
        -d "{\"token\":\"$JWT\"}" | jq -r '.token')
      JANUS_TOKEN="$TOKEN" janus run -- ./deploy.sh
```

Binding:

```json
{
  "name": "buildkite-deploy",
  "match_claims": { "organization_slug": "acme", "pipeline_slug": "atlas-deploy" },
  "scope_kind": "config", "scope_id": "<prod config id>",
  "access": "read", "ttl_seconds": 900, "enabled": true
}
```

## CircleCI example

- **Issuer:** `https://oidc.circleci.com/org/<ORG_ID>` — org-specific; get
  `<ORG_ID>` from Organization Settings.
- **Audience:** by default CircleCI sets `aud` to your organization ID; configure
  Janus's audience to match (or a custom audience if you set one).
- **Bind:** `oidc.circleci.com/project-id` (the project's UUID), or narrow by
  context/VCS claims.

CircleCI exposes the token as `$CIRCLE_OIDC_TOKEN`:

```yaml
jobs:
  deploy:
    steps:
      - run: |
          TOKEN=$(curl -sS -X POST https://janus.internal/v1/auth/oidc/federate \
            -H 'Content-Type: application/json' \
            -d "{\"token\":\"$CIRCLE_OIDC_TOKEN\"}" | jq -r '.token')
          JANUS_TOKEN="$TOKEN" janus run -- ./deploy.sh
```

Binding:

```json
{
  "name": "circleci-deploy",
  "match_claims": { "oidc.circleci.com/project-id": "00000000-0000-0000-0000-000000000000" },
  "scope_kind": "config", "scope_id": "<prod config id>",
  "access": "read", "ttl_seconds": 900, "enabled": true
}
```
