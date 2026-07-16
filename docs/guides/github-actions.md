# GitHub Actions integration

Janus and GitHub Actions connect in two independent directions, and it is
worth being clear about which one you want before you start:

- **Push** — Janus mirrors a config's secrets *into* GitHub as native
  GitHub Actions secrets, so workflows (or third-party actions) read them
  the ordinary GitHub way. This is the **sync** provider.
- **Pull** — a workflow authenticates *to* Janus at run time using its
  GitHub-issued OIDC token, receives a short-lived scoped token, and reads
  secrets directly from Janus. Nothing is stored in GitHub. This is
  **CI federation**.

They are not alternatives to each other so much as answers to different
questions. Part A covers push, Part B covers pull, and Part C is a short
table to help you pick. For most "my workflow needs to read a secret"
cases, reach for federation (Part B).

## Part A — Push secrets into GitHub Actions (sync)

Use this when something on the GitHub side expects a **native GitHub
Actions secret** — for example a third-party action that reads
`${{ secrets.FOO }}` and has no way to call Janus. A sync target
replicates one config's resolved secrets (references expanded,
inheritance merged) into a repository's — or a repository
*environment*'s — Actions secrets, on an interval and on demand.

Create the target with the `github` provider:

```sh
janus sync create --config $CONFIG --provider github \
  --owner acme --repo widgets \
  --interval-seconds 3600 \
  --pat github_pat_...
```

To target a repository *environment* rather than the whole repo, add
`--environment`:

```sh
janus sync create --config $CONFIG --provider github \
  --owner acme --repo widgets --environment production \
  --interval-seconds 3600 --pat github_pat_...
```

### Credentials (least privilege)

Supply a **fine-grained personal access token** scoped to only the target
repository, with the **"Secrets" repository permission set to
read/write** and nothing else. Janus never uploads a value in the clear:
it fetches the repo's (or environment's) public key from the GitHub
secrets API and encrypts each value client-side as a libsodium
**sealed box** before uploading — the same scheme GitHub's own docs and
the `gh` CLI use. The PAT is never logged and never echoed back by the
API; `get`/`list` responses mask it.

### Full-mirror and prune

By default (`--prune=true`) a target is a full mirror: a key Janus
previously wrote that is no longer in the config's resolved secrets is
deleted from GitHub on the next reconcile. Keys in the repo that Janus
never wrote are left alone. Pass `--prune=false` for strict
add-and-update-never-delete behavior.

### Key-name constraints

GitHub Actions secret names must match `^[A-Za-z_][A-Za-z0-9_]*$`, be
**100 characters or fewer**, and must **not** start with the reserved
`GITHUB_` prefix (checked case-insensitively). A Janus key that does not
conform is **skipped, not fatal** — the reconcile still applies every
other key and reports the skipped key (with a value-free reason) in the
result. Rename the offending key in Janus rather than expecting Janus to
coerce it.

Sync applies to Kubernetes as well, and has more to say about change
detection, backoff, the scheduler, and cross-project reference refusal.
For the complete reference see [../ops/sync.md](../ops/sync.md).

## Part B — Pull secrets from Janus in a workflow (federation)

Use this when a workflow needs to **read secrets from Janus at run time**
and you would rather not store a long-lived token in GitHub at all. The
workflow presents its ephemeral GitHub OIDC token to Janus; Janus
verifies it, matches its claims against a pre-registered **trust
binding**, and returns a **short-lived, scoped `janus_svc_` token**. That
token is an ordinary service token — revocable, listed, and self-expiring
— so the workflow then uses `janus run` / `janus secrets` exactly as any
other service-token caller would.

### The exchange

The endpoint is `POST /v1/auth/oidc/federate`. The request body carries
the raw OIDC JWT under a `token` field:

```json
{ "token": "<github-oidc-jwt>" }
```

On success it returns `200` with the minted service token under `token`,
the granted `scope` (`kind` / `id` / `access`), and — when the binding
sets a TTL — an `expires_at` timestamp:

```json
{
  "token": "janus_svc_...",
  "expires_at": "2026-07-16T12:34:56Z",
  "scope": { "kind": "config", "id": "<config-id>", "access": "read" }
}
```

Every failure — not configured, bad signature/issuer/audience/expiry, no
matching binding, or an ambiguous match — returns one indistinguishable
`401 federation_denied`; the server-side audit records the real reason.
Because the endpoint sits behind the unseal gate, it returns **503** while
the instance is sealed.

### Workflow snippet

The job needs `id-token: write` permission to request an OIDC token from
GitHub. Request the token **with the audience Janus is configured to
require**, POST it to the federate endpoint, capture the returned
`janus_svc_` token, and hand it to the CLI via `JANUS_TOKEN`:

```yaml
permissions:
  id-token: write   # allow the job to request a GitHub OIDC token

jobs:
  deploy:
    runs-on: ubuntu-latest
    steps:
      - name: Obtain a Janus token
        id: janus
        run: |
          # 1. Ask GitHub for an OIDC JWT with the audience Janus requires.
          RESP=$(curl -sS \
            "$ACTIONS_ID_TOKEN_REQUEST_URL&audience=janus" \
            -H "Authorization: Bearer $ACTIONS_ID_TOKEN_REQUEST_TOKEN")
          JWT=$(echo "$RESP" | jq -r '.value')

          # 2. Exchange it for a short-lived, scoped Janus token.
          TOKEN=$(curl -sS -X POST https://janus.internal/v1/auth/oidc/federate \
            -H 'Content-Type: application/json' \
            -d "{\"token\":\"$JWT\"}" | jq -r '.token')

          echo "::add-mask::$TOKEN"
          echo "token=$TOKEN" >> "$GITHUB_OUTPUT"

      - name: Run with secrets injected
        env:
          JANUS_TOKEN: ${{ steps.janus.outputs.token }}
          JANUS_ADDR: https://janus.internal
        run: janus run -- ./deploy.sh
```

The `audience` value (`janus` above) must equal the audience the admin
configured — a token minted for another audience is rejected. `janus run`
then injects the config's secrets into `./deploy.sh` as environment
variables; the minted token authorizes that read like any other service
token. See [injecting-secrets.md](./injecting-secrets.md) for `janus run`
in depth and [service-tokens.md](./service-tokens.md) for how service
tokens behave once minted.

### Trust bindings (admin, one-time)

For any of this to work an **admin** must first configure the federation
provider (issuer + audience) and at least one trust binding via the
`oidc:manage` admin API. A binding maps a set of claim conditions to a
scope, an access level, and a TTL. The safety rules are non-negotiable: a
binding **must** include a non-empty `repository` claim, **exactly one**
binding may match a given token (zero or multiple → denied), the audience
is exact-matched, and the TTL is **capped at 1h** (default 15m). A
representative binding:

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

Setting up the provider, the full claim-matching semantics, and the
security properties of the exchange are covered in
[../ci-federation.md](../ci-federation.md).

## Part C — Which one?

|                    | Sync (Part A)                              | Federation (Part B)                          |
|--------------------|--------------------------------------------|----------------------------------------------|
| Direction          | Push: Janus → GitHub                       | Pull: workflow → Janus                       |
| Where secrets live | Stored in GitHub as Actions secrets        | Never stored in GitHub; read at run time     |
| Credential in CI   | None (Janus holds a PAT server-side)       | None long-lived; ephemeral OIDC → short token|
| Freshness          | As of the last reconcile (interval / manual)| Always current at read time                  |
| Consumed as        | `${{ secrets.FOO }}` in any action         | `JANUS_TOKEN` + `janus run` / `janus secrets`|
| Best for           | Third-party actions that need native secrets| Your own workflow steps reading secrets      |

Prefer **federation** whenever your own workflow steps read secrets: it
stores nothing in GitHub, the token is short-lived and scoped, and values
are always current. Reach for **sync** only when something on the GitHub
side genuinely requires a native GitHub Actions secret — most commonly a
third-party action you do not control.
