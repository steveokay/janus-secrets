# Running Janus: server, seal lifecycle, and the `janus` CLI

How to run the server, initialize and unseal it, and operate it day to day.
The seal lifecycle shipped with the server-bootstrap milestone; **authentication
and RBAC** (below) shipped in the auth and RBAC milestones. The secret-facing
HTTP API and the secrets CLI (`janus run`, etc.) arrive in later milestones, so
the identity endpoints are HTTP-only for now (there is no `janus login` yet).

## The mental model

Janus boots **sealed**: the master key is not in memory, and every route except
`/v1/sys/*` answers `503 {"error":{"code":"sealed"}}`. An operator (or, with a
KMS seal, the server itself) unseals it, which loads the master key into the
in-memory keyring. Restarting — or `janus seal` — wipes the key and returns to
the sealed state. Nothing persisted on disk or in Postgres can decrypt a secret
on its own.

The seal lifecycle is a three-state machine:

```
uninitialized ──init──▶ sealed ──unseal──▶ unsealed
                          ▲                    │
                          └──────seal──────────┘
```

- **uninitialized** — no master key exists yet. `janus init` creates one.
- **sealed** — a master key exists (wrapped/split), but it is not in memory.
- **unsealed** — the master key is live in memory; secret operations work.

## Configuration (environment variables)

| Variable | Required | Meaning |
|---|---|---|
| `JANUS_DATABASE_URL` | yes (`server`, `migrate`) | Postgres DSN, e.g. `postgres://janus:pw@host:5432/janus?sslmode=disable` |
| `JANUS_LISTEN_ADDR` | no | HTTP listen address, default `:8200` |
| `JANUS_SEAL_TYPE` | before first init | `shamir` or `awskms`. After init the stored type is authoritative; a conflicting env value is a **fatal boot error** (misconfiguration is never guessed around) |
| `JANUS_AWS_KMS_KEY_ARN` | for `awskms` | KMS key id/ARN/alias (plus the standard AWS SDK env for credentials/region) |
| `JANUS_ADDR` | no | Default server address for the CLI commands (flag `--address` wins) |

There is no config file. The server auto-applies embedded migrations at boot
(golang-migrate takes a Postgres advisory lock, so concurrent boots are safe);
`janus migrate` remains for explicit/CI use.

## Quickstart (local development)

```sh
make dev-up
```

This builds the binary, starts the compose stack (Postgres + Janus, both with
healthchecks), and runs `scripts/dev-unseal.sh`, which:

1. waits for the server to answer,
2. if uninitialized, runs `janus init --shares 1 --threshold 1` and caches the
   single share in `.dev/janus-share` (gitignored, `0600`),
3. unseals with the cached share (idempotent — safe to re-run after every
   restart).

**The dev share IS the master key.** A 1-of-1 seal plus a share on disk is a
deliberate dev-only convenience; production uses a real k-of-n split and never
writes shares to disk. Prod and dev share every code path — the only "dev mode"
is the share count.

## Production flow (Shamir seal)

```sh
# 1. Run the server (it boots sealed).
JANUS_DATABASE_URL=postgres://... JANUS_SEAL_TYPE=shamir janus server

# 2. Initialize once. Shares are printed EXACTLY ONCE — distribute them to
#    separate custodians immediately; Janus never stores or re-shows them.
janus init --shares 5 --threshold 3

# 3. Unseal: any 3 of the 5 custodians each submit a share.
janus unseal          # prompts on stdin with echo off; repeat per share
```

After a restart the server is sealed again; repeat step 3. `janus seal-status`
shows progress (`2/3 shares`) mid-ceremony.

**Recovering from a bad share:** share reconstruction consumes *all* submitted
shares, so one mistyped share poisons the attempt — the server reports
`key_check_failed`. Run `janus unseal --reset` to discard the submitted set and
resubmit from scratch.

## Production flow (AWS KMS auto-unseal)

```sh
JANUS_DATABASE_URL=postgres://... \
JANUS_SEAL_TYPE=awskms \
JANUS_AWS_KMS_KEY_ARN=arn:aws:kms:...:key/... \
janus server

janus init      # no shares; the server unseals itself immediately
```

At every subsequent boot the server auto-unseals via one KMS `Decrypt` call.
If KMS is unreachable at boot (outage, IAM), the server **stays up but
sealed** and logs a warning; `janus unseal` (no share) retries. The IAM
identity needs `kms:Encrypt` and `kms:Decrypt` on the key.

A **key check value** (a known constant encrypted under the master key at
init) guards both seal types: a wrong-but-well-formed master key — a bad
Shamir reconstruction, a wrong KMS key — is rejected before it is ever used.

## `janus` CLI reference

All sys commands take `--address` (default `JANUS_ADDR`, then
`http://127.0.0.1:8200`).

| Command | What it does |
|---|---|
| `janus server` | Run the server: open Postgres, auto-migrate, resolve the seal config, serve. Graceful shutdown on SIGINT/SIGTERM (10s drain) |
| `janus init [--shares N] [--threshold K] [--admin-email <e>] [--json]` | Initialize the seal. Shamir defaults to 3-of-5; `--shares 1 --threshold 1` is the dev special case. Prints the shares **and the one-time initial-admin credential** once (`--json` for scripting). That admin is granted the instance-owner role. `409 already_initialized` on repeat |
| `janus unseal [--share <hex>]` | Submit one unseal share. With no flag, reads from stdin — echo-off prompt on a TTY, plain read when piped. Under a KMS seal, takes no share and just retries the auto-unseal. Prefer stdin over `--share`: a flag value is visible in process lists and shell history |
| `janus unseal --reset` | Discard all submitted shares (recovery from a bad share) |
| `janus seal-status` | Show `initialized` / `sealed` / seal type / threshold / submission progress |
| `janus seal` | Re-seal a running server — wipes the master key from memory (incident response). **Note:** `POST /v1/sys/seal` now requires the `sys:seal` permission, and this CLI command does not yet send a credential, so it returns `401` against a live server; seal over HTTP with an owner/admin session cookie or a suitably-scoped bearer token until the CLI grows an auth flag |
| `janus migrate` | Apply migrations explicitly (`JANUS_DATABASE_URL`) |
| `janus version` | Print the version |

Errors render as `message (code, HTTP status)`, e.g.
`seal is already initialized (already_initialized, HTTP 409)`.

## Sys HTTP API

Everything under `/v1/sys/` is JSON and reachable while sealed. Shamir shares
travel as lowercase hex strings. Errors use the project-wide envelope
`{"error":{"code":"...","message":"..."}}`.

| Route | Behavior |
|---|---|
| `GET /v1/sys/health` | Liveness — always `200 {"status":"ok","initialized":b,"sealed":b}` while the process is up (compose healthcheck) |
| `GET /v1/sys/seal-status` | `{"initialized","sealed","type","threshold","shares","progress":{"submitted","required"}}` — Shamir fields only for Shamir seals; progress only while sealed |
| `POST /v1/sys/init` | Shamir: `{"shares":5,"threshold":3,"admin_email":"..."}` → one-time `{"type":"shamir","shares":["<hex>",...],"admin":{"email","password"}}`; server stays sealed. KMS: empty body → `{"type":"awskms","admin":{...}}` and immediate auto-unseal. The returned admin holds instance-owner. Init is serialized server-side, so racing inits produce exactly one success and `409` for the rest |
| `POST /v1/sys/unseal` | Shamir: `{"share":"<hex>"}`, one per call; reaching the threshold reconstructs, verifies the KCV, and unseals. KMS: empty body retries. Idempotent when already unsealed |
| `POST /v1/sys/unseal/reset` | Discard submitted shares |
| `POST /v1/sys/seal` | Wipe the master key; back to sealed. Requires authentication and the `sys:seal` permission (owner/admin) |

Error codes: `sealed`, `not_initialized`, `already_initialized`,
`invalid_share`, `duplicate_share`, `key_check_failed`, `validation`,
`internal`. Status mapping: 400 for share/validation problems, 409 for repeat
init, 503 for sealed (middleware), 500 with a generic message for
infrastructure failures — internals never leak.

## Identity & access (auth + RBAC)

Once unsealed, the server enforces authentication and role-based access control.
These endpoints are HTTP + JSON under `/v1/` and require an unsealed server.

**Authenticating.** Humans log in at `POST /v1/auth/login` (email + password →
an HTTP-only session cookie); `/v1/auth/{logout,me,password}` manage the session.
Machines use **service tokens**: `POST /v1/tokens` mints a `janus_svc_…` token
(returned exactly once — only its HMAC is stored) scoped to a config or
environment with `read` or `readwrite` access. Present it as
`Authorization: Bearer janus_svc_…`. The bootstrap admin credential from `init`
is the first login.

**Roles and scopes.** Four roles — viewer ⊂ developer ⊂ admin ⊂ owner — are
bound to a user at **instance**, **project**, or **environment** scope:

| Endpoint | Purpose | Requires |
|---|---|---|
| `GET/PUT/DELETE /v1/instance/members[/{uid}]` | Instance-wide role bindings | `member:read` / `member:manage` at instance |
| `GET/PUT/DELETE /v1/projects/{pid}/members[/{uid}]` | Project role bindings | `member:*` at that project |
| `GET/PUT/DELETE /v1/projects/{pid}/environments/{eid}/members[/{uid}]` | Environment role bindings | `member:*` at that environment |
| `POST/GET /v1/users`, `POST /v1/users/{id}/disable` | Provision / list / deactivate users | `user:manage` (instance) |
| `POST/GET/DELETE /v1/tokens[/{id}]` | Mint / list / revoke service tokens | `token:*` at the token's scope |

Bindings inherit top-down (an instance binding applies everywhere; a project
binding covers that project's environments and configs) and combine
most-permissively. `PUT …/members/{uid}` with `{"role":"developer"}` grants;
`DELETE` revokes. Two rails the operator will hit: you cannot grant a role above
your own (delegation), and the **last instance owner cannot be removed,
demoted, or disabled** (`409`) — so you can never lock yourself out. If every
owner binding is somehow lost, the next server start re-grants instance-owner to
the oldest user. Denied requests return a generic `403 forbidden` that reveals
nothing about the policy.

## Security posture and current caveats

- **No key material in logs.** The request logger records method, path,
  status, and duration only — request/response bodies (which carry shares)
  are never logged. Enforced by leak tests at the HTTP layer.
- **One-time share exposure.** Init shares exist in server memory only long
  enough to hex-encode into the response, then are zeroized. The response is
  the only copy — there is no recovery path for lost shares short of
  threshold reconstruction.
- **Sealed-by-default for future routes.** The 503 middleware is installed
  ahead of all routing, so any route added later is gated without touching
  middleware; unknown paths are 503 while sealed too.
- **`POST /v1/sys/seal` requires the `sys:seal` permission** (owner/admin) as
  of the RBAC milestone. `init` and `unseal` remain unauthenticated by
  bootstrap necessity, matching the Vault model: init races are guarded, and
  unsealing requires valid shares. Caveat: the `janus seal` *CLI* does not yet
  attach a credential, so it returns `401` — seal over HTTP with a session
  cookie or bearer token until the CLI grows an auth flag.
- **Rate limiting keys on `RemoteAddr`.** The login limiter buckets by direct
  peer address; behind a TLS-terminating proxy that collapses to one bucket.
  Add trusted-proxy `X-Forwarded-For` handling when a proxy is introduced (the
  same caveat affects the conditional cookie `Secure` flag).
- **No TLS yet** — terminate TLS at a reverse proxy for now; native TLS is a
  later milestone. Shares transit the network in the clear otherwise.
