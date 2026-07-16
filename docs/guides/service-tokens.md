# Service tokens

A **service token** is a long-lived, scoped credential that a non-human
identity — an application, a server, a CI job — uses to authenticate to Janus.
It is the machine counterpart to a user login: where a person signs in with
email + password (or OIDC) and gets a session cookie, a machine presents a
service token as a bearer credential on every request.

Service tokens are shown **once** at creation and never again. Janus stores only
an HMAC of the token, never the raw value, so a token you fail to capture is
gone and must be re-minted. Each token is scoped to a single config or
environment with read or read/write access, and every token-touching request is
recorded in the audit log.

This guide covers minting, scoping, using, listing, and revoking service
tokens, plus the security rules that govern their handling. For the RBAC model
those permissions live in, see [../operations.md](../operations.md); for using a
token to inject secrets into a process, see
[injecting-secrets.md](./injecting-secrets.md).

## When to use a service token

| Situation | Credential |
| --- | --- |
| A person working in the web UI or the CLI interactively | User login session (password / OIDC) |
| An app or server reading its config at boot / runtime | Service token |
| A CI job that needs secrets for a build or deploy | Service token, or OIDC federation (see below) |
| A one-off admin action from your own terminal | Your login session (`janus login`) |

Reach for a service token whenever there is no human at the keyboard. A session
cookie is tied to a browser or a `janus login` on your workstation and expires
on inactivity; a service token is stable, scoped narrowly, and revocable
independently of any person's account.

For GitHub Actions specifically, prefer **OIDC-federated machine identity** over
a stored long-lived token — the workflow exchanges its GitHub OIDC JWT for a
short-lived scoped token at run time, so there is no secret to store or rotate.
See [../ci-federation.md](../ci-federation.md) and
[github-actions.md](./github-actions.md).

## Minting a token

Tokens are minted through the REST API at `POST /v1/tokens`. There is **no
`janus tokens` CLI subcommand** — you either call the API directly with an
admin/owner bearer credential, or use the web UI (Settings → Tokens, see
[../web.md](../web.md)), which calls the same endpoint. The caller needs the
`token:mint` permission at the token's scope.

### Request

The request body carries the token's name, its scope, its access level, and an
optional TTL:

| Field | Type | Notes |
| --- | --- | --- |
| `name` | string | Human label for the token (required, non-empty). |
| `scope.kind` | string | `config` or `environment` (`transit` also exists for the transit engine). |
| `scope.id` | string | The id of the config or environment the token is scoped to. |
| `access` | string | `read` or `readwrite` for config/environment scopes. |
| `ttl_seconds` | number | Optional. Positive number of seconds until expiry; omit for a long-lived token. |

### Example

```sh
# ADDR is your instance; ADMIN is an admin/owner bearer credential
# (a session token, or another service token with token:mint at this scope).
curl -XPOST "$ADDR/v1/tokens" \
  -H "Authorization: Bearer $ADMIN" \
  -H "Content-Type: application/json" \
  -d '{
        "name": "web-app prod reader",
        "scope": {"kind": "config", "id": "cfg_abc123"},
        "access": "read",
        "ttl_seconds": 2592000
      }'
```

### Response

The response returns the raw token **once**, alongside its metadata:

```json
{
  "token": "janus_svc_…",
  "id": "tok_abc123",
  "name": "web-app prod reader",
  "scope": {"kind": "config", "id": "cfg_abc123"},
  "access": "read",
  "expires_at": "2026-08-15T12:00:00Z"
}
```

The `token` field — a `janus_svc_…` string — is the only time you will ever see
the raw credential. Capture it immediately into wherever the consumer reads it
(a CI secret, a secret manager, a `.env` your deploy pipeline injects). The `id`
is what you use later to list or revoke the token; it is **not** the credential
and cannot be used to authenticate.

## Scoping model

Service token authorization is **deny-by-default**: a token can do only what its
scope and access level explicitly permit, and nothing else.

- **Scope** binds a token to exactly one target — a single **config** or a
  single **environment**. A config-scoped token sees only that config's
  secrets; an environment-scoped token covers the configs under that
  environment. The scope target must exist when the token is minted.
- **Access level** is `read` or `readwrite`. A `read` token can list and read
  secret values within its scope; `readwrite` additionally permits writes. There
  is no "admin" access level on a service token — administrative actions
  (minting tokens, managing members, rotating keys) require a user session with
  the appropriate role.
- A config/environment (secrets) token has **no** transit access, and a transit
  token has no secrets access — the two are separate scope kinds.

Because scope and access are enforced on every request, the practical rule is to
mint the **narrowest** token that does the job: a prod app that only reads its
config gets a `read` token scoped to that one config, not a `readwrite` token on
the whole environment.

For how these permissions sit inside the broader role model (viewer /
developer / admin / owner, scoped to project or environment), see the identity &
access section of [../operations.md](../operations.md).

## Using a token

The `janus` CLI accepts a service token two ways, and both send it as an
`Authorization: Bearer` header. Precedence is **`--token` > `JANUS_TOKEN` >
stored login session**:

```sh
# Via environment variable (typical for servers and CI):
export JANUS_TOKEN=janus_svc_…
janus run -- ./my-app

# Or explicitly per-invocation:
janus secrets list --token janus_svc_…
```

When calling the REST API directly, pass the same header:

```sh
curl "$ADDR/v1/configs/cfg_abc123/secrets" \
  -H "Authorization: Bearer janus_svc_…"
```

The flagship use is `janus run`, which injects the scoped config's secrets as
environment variables into a subprocess. See
[injecting-secrets.md](./injecting-secrets.md) for `janus run`, the `secrets`
subcommands, and the download flows in depth.

## Listing tokens

`GET /v1/tokens` returns token **metadata only** — the raw value is
structurally unrecoverable, so listing can never leak a credential. The response
is filtered to tokens whose scope target you can read, and paginates via a
cursor:

```sh
curl "$ADDR/v1/tokens" \
  -H "Authorization: Bearer $ADMIN"
```

```json
{
  "tokens": [
    {
      "id": "tok_abc123",
      "name": "web-app prod reader",
      "scope_kind": "config",
      "scope_id": "cfg_abc123",
      "access": "read",
      "created_by": "usr_…",
      "created_at": "2026-07-16T12:00:00Z",
      "expires_at": "2026-08-15T12:00:00Z"
    }
  ],
  "next_cursor": null
}
```

Follow `next_cursor` (when non-null) to page through the rest. Note the
per-scope visibility filter means a page may contain fewer entries than the
page limit — keep paging until `next_cursor` is `null`.

## Revoking a token

`DELETE /v1/tokens/{id}` revokes a token by its `id` (not its raw value).
Revocation is **immediate**: the next request presenting that token fails
authentication. This is the lever to pull when a token is leaked, a machine is
decommissioned, or a credential is being rotated.

```sh
curl -XDELETE "$ADDR/v1/tokens/tok_abc123" \
  -H "Authorization: Bearer $ADMIN"
# → 204 No Content
```

A successful revoke returns `204 No Content`; an unknown or already-revoked id
returns `404`.

## Security & handling

- **Shown once.** The raw `janus_svc_…` value is returned only in the mint
  response. If you lose it, you cannot recover it — mint a new one and revoke
  the old.
- **HMAC-only storage.** Janus persists an HMAC-SHA256 of the token, never the
  raw value. A database compromise does not expose usable tokens. Verification
  uses a constant-time comparison, so token lookup is not vulnerable to timing
  attacks.
- **Store it as a secret.** Put the token in a CI secret store or a secret
  manager and inject it via `JANUS_TOKEN` at run time. Never commit a token to
  source control, bake it into an image, or write it to a log.
- **Scope narrowly, TTL where you can.** Prefer `read` over `readwrite`, a
  single config over an environment, and set `ttl_seconds` for tokens that only
  need to live for a bounded window.
- **Rotate by mint-then-revoke.** There is no in-place rotation: mint a fresh
  token, roll it out to the consumer, confirm the consumer is using it, then
  revoke the old one. Because revocation is immediate, cut over before you
  revoke.
- **Prefer keyless for GitHub Actions.** For CI on GitHub Actions, OIDC
  federation removes the stored-token problem entirely — the workflow mints a
  short-lived scoped token from its OIDC identity per run. See
  [../ci-federation.md](../ci-federation.md) and
  [github-actions.md](./github-actions.md).

Every mint and revoke is written to the append-only audit log (actor, action,
token id, result), and token values never appear in audit entries. See the
audit section of [../operations.md](../operations.md).
