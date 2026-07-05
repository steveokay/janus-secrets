# Running Janus: server, seal lifecycle, and the `janus` CLI

How to run the server, initialize and unseal it, and operate it day to day.
The seal lifecycle shipped with the server-bootstrap milestone; **authentication
and RBAC** (below) shipped in the auth and RBAC milestones, the **hash-chained
audit log** (below) shipped in the audit milestone, and the **secret-facing REST
API** (below) shipped in the REST API milestone. The secrets CLI (`janus run`,
etc.) arrives in a later milestone, so everything is HTTP-only for now (there is
no `janus login` yet).

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

## Secrets API

The project → environment → config → secret hierarchy and its two-level
versioning are served over `/v1/`, JSON. Every route requires an **unsealed**
server (503 while sealed) and the relevant **RBAC permission** (deny-by-default);
service errors map to the standard envelope (`404 not_found`, `409 conflict`,
`400 validation`, `503 sealed`, generic `500`). Values are never echoed in an
error message or a log line.

**Hierarchy CRUD + lifecycle.** Every resource supports create, read/list,
soft-delete, restore, and hard-destroy. A hard-destroy of a project or
environment cascades the whole subtree (migration `000005`); a config whose
`inherits_from` is still referenced by a branch config cannot be destroyed
(`409`).

| Route | Purpose | Requires |
|---|---|---|
| `POST /v1/projects`, `GET /v1/projects[/{pid}]` | Create / list / get projects | `project:create` (instance) / `project:read` |
| `DELETE /v1/projects/{pid}[?destroy=true]`, `POST …/restore` | Soft-delete (or destroy) / restore a project | `project:delete` (**owner**) |
| `POST/GET /v1/projects/{pid}/environments[/{eid}]`, `DELETE …[?destroy=true]`, `POST …/restore` | Environment CRUD + lifecycle | `env:*` / `project:read` |
| `POST/GET /v1/projects/{pid}/environments/{eid}/configs`, `GET/DELETE /v1/configs/{cid}[?destroy=true]`, `POST /v1/configs/{cid}/restore` | Config CRUD + lifecycle | `config:*` / `config:read` |

**Reading secrets — masked vs. reveal.** The distinction is endpoint-encoded and
matches the audit rules: a **masked** read returns key names + value-version
metadata only and is **not audited**; a **reveal** decrypts the value and **is
audited** (`secret.reveal`).

| Route | Purpose | Requires |
|---|---|---|
| `GET /v1/configs/{cid}/secrets` | **Masked** list (`{key → {value_version, created_at}}`), no values, no audit | `secret:read` |
| `GET /v1/configs/{cid}/secrets?reveal=true` | **Reveal** every key's value in one call (audited) | `secret:read` |
| `GET /v1/configs/{cid}/secrets/{key}[?version=N]` | **Reveal** one key's latest (or historical) value (audited) | `secret:read` |
| `GET /v1/configs/{cid}/secrets/{key}/history` | Masked value-version history for one key, no audit | `secret:read` |

**Writing secrets.** Each write commits **one new immutable config version**
(the unit of diff/rollback); a batch is all-or-nothing. Writes and deletes are
audited (`secret.write` / `secret.delete`).

| Route | Purpose | Requires |
|---|---|---|
| `PUT /v1/configs/{cid}/secrets` | Batch write `{"message":…,"changes":[{"key","value","delete"}]}` — one version for all edits; duplicate key in a batch → `400` | `secret:write` |
| `PUT /v1/configs/{cid}/secrets/{key}` | Write one key `{"value":…}` | `secret:write` |
| `DELETE /v1/configs/{cid}/secrets/{key}` | Remove one key (new version) | `secret:write` |

**Versions, diff, rollback.**

| Route | Purpose | Requires |
|---|---|---|
| `GET /v1/configs/{cid}/versions` | List config versions (v1, v2, …) | `config:read` |
| `GET /v1/configs/{cid}/versions/diff?a=N&b=M` | Added / changed / removed keys between two versions (no values) | `config:read` |
| `POST /v1/configs/{cid}/rollback` | Roll the config back to a target version — repoints at existing ciphertext, no re-encryption, as a new version | `secret:write` |

## Audit log

Every authenticated request that performs a sensitive action appends an
immutable event to a hash chain: **actor, action, resource path, result, IP,
timestamp**, plus the SHA-256 of the previous event. Recording is **fail-closed**
— if the audit write fails, the request returns `500` and the caller never sees
success for an unrecorded action. Events never contain a secret value (the event
type has no value field); the log records key names and paths only.

**What gets audited.** Mutations: token mint/revoke, user create/disable, member
grant/revoke, `sys.seal`, and `auth.login` (success, plus failed attempts as a
`denied`/anonymous event with the attempted email — brute-force visibility),
`auth.logout`, `auth.password_change`. Every denied (`403`) authorization
decision on these endpoints is recorded as a `denied` event. **Not** audited:
masked/metadata reads (token/user/member `LIST`, `/v1/auth/me`, and
`/v1/audit/verify` itself); `sys.init`/`sys.unseal` (pre-auth bootstrap
operations).

| Route | Behavior | Requires |
|---|---|---|
| `GET /v1/audit/verify` | Walks the chain, recomputing every hash and checking linkage. `{"valid":true,"count":N,"head_seq":N,"head_hash":"<hex>"}` when intact; `{"valid":false,"broken_at_seq":K,"reason":"hash_mismatch"\|"chain_break"}` on the first break. Not self-audited. | `audit:read` (owner/admin) |
| `GET /v1/audit/export` | Streams matching events, chunked. `?format=jsonl` (default, `application/x-ndjson`) or `?format=csv`. Filters (AND-combined): `?from=`/`?to=` (RFC3339), `?actor=` (matches actor id **or** name), `?action=`, `?result=` (`success`/`denied`/`error`). Each row includes `prev_hash`+`hash` (hex) for offline verification. Self-audited (`audit.export`) **before** streaming. | `audit:read` (owner/admin) |

Both are gated by `RequireAuth` + `audit:read`, so they answer `503` while the
server is sealed and `403` for a caller without the permission. Invalid
`format`/`from`/`to`/`result` → `400 validation`.

**Tamper evidence & its limits.** `verify` detects any field mutation, deletion,
or reordering of past events, and the chain stays contiguous under concurrency
(each append serializes on a Postgres advisory lock). The chain is *unanchored*:
an attacker with direct database write access could append a well-formed
continuation or rewrite the whole chain from a point forward and recompute
hashes — external anchoring / signatures are an explicit non-goal. Protect the
database. The log is **append-only forever** (no pruning/retention — pruning
would break the chain), and there is a documented crash-window caveat: a crash
between a mutation's commit and its audit insert leaves that one action
unaudited (the mutation stands; the chain remains consistent).

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
