# Running Janus: server, seal lifecycle, and the `janus` CLI

How to run the server, initialize and unseal it, and operate it day to day.
The seal lifecycle shipped with the server-bootstrap milestone; **authentication
and RBAC** (below) shipped in the auth and RBAC milestones, the **hash-chained
audit log** (below) shipped in the audit milestone, and the **secret-facing REST
API** (below) shipped in the REST API milestone. The **secrets CLI**
(`janus login`/`setup`/`secrets`/`run`) shipped in the CLI milestone — its
developer/CI flows are summarized under [Secrets CLI](#secrets-cli-developer--ci-flows)
below and documented in full in [cli.md](cli.md).

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
| `JANUS_SEAL_TYPE` | before first init | `shamir`, `awskms`, `gcpkms`, or `azurekv`. After init the stored type is authoritative; a conflicting env value is a **fatal boot error** (misconfiguration is never guessed around) |
| `JANUS_AWS_KMS_KEY_ARN` | for `awskms` | KMS key id/ARN/alias (plus the standard AWS SDK env for credentials/region) |
| `JANUS_GCP_KMS_KEY` | for `gcpkms` | GCP KMS key resource name `projects/P/locations/L/keyRings/R/cryptoKeys/K` (uses ambient GCP application-default credentials) |
| `JANUS_AZURE_KEYVAULT_URL` | for `azurekv` | Key Vault URL `https://<vault>.vault.azure.net/` (uses ambient `DefaultAzureCredential`) |
| `JANUS_AZURE_KEY_NAME` | for `azurekv` | Key Vault key name (RSA/RSA-HSM; wrapping via RSA-OAEP-256) |
| `JANUS_AZURE_KEY_VERSION` | no | optional Key Vault key version; empty = current version |
| `JANUS_SESSION_IDLE_TIMEOUT` | no | Session inactivity window (Go duration, default `30m`; `0` disables). Applies to session cookies — web UI and CLI `janus login` sessions; service tokens unaffected |
| `JANUS_ROTATION_TICK` | no | rotation scheduler tick interval; 0 disables (Go duration, default `60s`) |
| `JANUS_SYNC_TICK` | no | sync scheduler tick interval; 0 disables (Go duration, default `60s`) |
| `JANUS_DYNAMIC_TICK` | no | dynamic-lease scheduler tick interval; 0 disables (Go duration, default `60s`) |
| `JANUS_NOTIFY_TICK` | no | notification dispatcher tick interval; 0 disables (Go duration, default `30s`) |
| `JANUS_BACKUP_TICK` | no | scheduled encrypted **S3 backup** interval; unset/`0` disables the engine (Go duration, e.g. `6h`). When set, the S3 bucket/region/credentials below become required or boot fails. See [backup & restore](guides/backup-and-restore.md#scheduled-encrypted-backups-to-s3-built-in) |
| `JANUS_BACKUP_RETENTION` | no | keep the N most recent backup objects under the prefix; prune the rest after each upload (non-negative int; `0`/unset = keep all) |
| `JANUS_BACKUP_S3_BUCKET` | when `JANUS_BACKUP_TICK` set | destination S3 bucket for scheduled backups |
| `JANUS_BACKUP_S3_PREFIX` | no | key prefix under the bucket (e.g. `prod/`); objects are `<prefix>/janus-backup-<timestamp>.jsonl` |
| `JANUS_BACKUP_S3_REGION` | when `JANUS_BACKUP_TICK` set | S3 region |
| `JANUS_BACKUP_S3_ENDPOINT` | no | custom endpoint for **S3-compatible** stores (MinIO, Cloudflare R2, Backblaze B2, …); empty = real AWS S3. Uses path-style addressing |
| `JANUS_BACKUP_S3_ACCESS_KEY_ID` | when `JANUS_BACKUP_TICK` set | **static** S3 access key id (never the host's ambient AWS identity) |
| `JANUS_BACKUP_S3_SECRET_ACCESS_KEY` | when `JANUS_BACKUP_TICK` set | static S3 secret access key (write-only; never logged) |
| `JANUS_BREAKGLASS_MAX_TTL` | no | Ceiling a break-glass grant's requested TTL is clamped to (Go duration, positive, default `1h`) |
| `JANUS_HTTP_READ_TIMEOUT` | no | HTTP server read timeout (Go duration, default `30s`; `0` disables) |
| `JANUS_HTTP_IDLE_TIMEOUT` | no | HTTP server idle (keep-alive) timeout (Go duration, default `120s`; `0` disables) |
| `JANUS_HTTP_WRITE_TIMEOUT` | no | HTTP server write timeout (Go duration, default `0` = disabled — deliberate, so `/v1/audit/export` can stream long-running responses) |
| `JANUS_HTTP_MAX_BODY_BYTES` | no | Max request body size in bytes (default `10485760` = 10 MiB; `0` disables the cap). Not applied to the restore endpoint |
| `JANUS_SHUTDOWN_GRACE` | no | Graceful-drain window on `SIGTERM`/`SIGINT` (Go duration, positive, default `10s`); bounds how long in-flight requests get before the server force-closes |
| `JANUS_DB_MAX_CONNS` | no | pgx pool max size (positive int; unset ⇒ pgx default `max(4, NumCPU)`) |
| `JANUS_DB_MIN_CONNS` | no | pgx pool min idle connections (non-negative int; unset ⇒ pgx default `0`) |
| `JANUS_DB_MAX_CONN_LIFETIME` | no | pgx max connection lifetime (Go duration, positive; unset ⇒ pgx default `1h`) |
| `JANUS_DB_MAX_CONN_IDLE_TIME` | no | pgx max idle time before a connection is closed (Go duration, positive; unset ⇒ pgx default `30m`) |
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

## Production flow (cloud KMS auto-unseal)

Auto-unseal wraps the master key with a cloud KMS key and unwraps it at boot
with a single decrypt call — no shares, no ceremony. Pick one provider; each
relies on that cloud's ambient credentials:

```sh
# AWS KMS
JANUS_DATABASE_URL=postgres://... \
JANUS_SEAL_TYPE=awskms \
JANUS_AWS_KMS_KEY_ARN=arn:aws:kms:...:key/... \
janus server

# GCP KMS (ambient application-default credentials)
JANUS_DATABASE_URL=postgres://... \
JANUS_SEAL_TYPE=gcpkms \
JANUS_GCP_KMS_KEY=projects/P/locations/L/keyRings/R/cryptoKeys/K \
janus server

# Azure Key Vault (ambient DefaultAzureCredential; RSA key wrapped via RSA-OAEP-256)
JANUS_DATABASE_URL=postgres://... \
JANUS_SEAL_TYPE=azurekv \
JANUS_AZURE_KEYVAULT_URL=https://<vault>.vault.azure.net/ \
JANUS_AZURE_KEY_NAME=janus-unseal \
janus server

janus init      # no shares; the server unseals itself immediately
```

At every subsequent boot the server auto-unseals via one KMS `Decrypt` call.
If the KMS is unreachable at boot (outage, permissions), the server **stays up
but sealed** and logs a warning; `janus unseal` (no share) retries. The
identity Janus runs as needs encrypt+decrypt on the key: `kms:Encrypt` /
`kms:Decrypt` (AWS), `cloudkms.cryptoKeyVersions.useToEncrypt` /
`useToDecrypt` (GCP), or `keys/encrypt` + `keys/decrypt` (Azure).

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
| `janus seal` | Re-seal a running server — wipes the master key from memory (incident response). Requires an admin (`sys:seal`): authenticates like the secrets commands — `--token` > `JANUS_TOKEN` > the stored session from `janus login` — and its `--address` falls back to the stored login address |
| `janus backup [--out FILE]` | Stream a key-preserving full-instance backup (JSONL) to stdout or `--out` (written atomically, mode 0600). The dump contains only wrapped keys and ciphertext — useless without the original unseal material. Requires an admin (`sys:backup`): authenticates like `janus seal` — `--token` > `JANUS_TOKEN` > stored session. See the backup & restore runbook (`docs/ops/backup-restore.md`) |
| `janus restore [file]` | Restore a backup into an **empty** instance (fresh database, before init); reads stdin when no file is given. Unauthenticated by design — it is a pre-init bootstrap operation like `janus init`. Afterwards the instance is sealed: unseal with the ORIGINAL shares/KMS key. See the backup & restore runbook |
| `janus session list [--json]` | List your own active login sessions (IP, user-agent, last-seen; the current session is marked `*`). Metadata only — no credential material |
| `janus session revoke <id> \| --others` | Revoke one of your sessions by id, or `--others` to sign out every session except the current one |
| `janus migrate` | Apply migrations explicitly (`JANUS_DATABASE_URL`) |
| `janus version` | Print the version |
| `janus rotation create --config <id> --key <k> --type postgres\|webhook --interval-seconds <n> [...]` | Create a rotation policy on a config's secret key. `postgres` type: `--admin-dsn`, `--role`, `--password-len` (default 32). `webhook` type: `--url`, `--hmac-key`. Either type: optional `--notify-url`/`--notify-hmac-key`. Requires `rotation:manage`. See the rotation runbook (`docs/ops/rotation.md`) |
| `janus rotation list --project <id>` | List rotation policies for a project. Requires `rotation:manage` |
| `janus rotation get <id>` | Show one rotation policy (masked config). Requires `rotation:manage` |
| `janus rotation update <id> [--interval-seconds <n>] [--status active\|paused]` | Update a policy's interval or status. Requires `rotation:manage` |
| `janus rotation rotate <id>` | Rotate a policy immediately; also clears a `failed` status and retries. Requires `rotation:manage` |
| `janus rotation delete <id>` | Delete a rotation policy. Requires `rotation:manage` |
| `janus sync create --config <id> --provider github\|k8s\|gitlab\|aws_ssm --interval-seconds <n> [...]` | Create a sync target on a config. `github`: `--owner`, `--repo`, `--environment` (optional), `--pat`. `k8s`: `--api-url`, `--k8s-token`, `--ca-cert`, `--namespace`, `--secret-name`. `gitlab`: `--project`, `--gitlab-token`, `--gitlab-url`/`--environment-scope` (optional). `aws_ssm`: `--aws-region`, `--path-prefix`, `--aws-access-key-id`, `--aws-secret-access-key`, `--aws-session-token` (optional). Any type: `--prune` (default `true`). Requires `sync:manage`. See the sync runbook (`docs/ops/sync.md`) |
| `janus sync list --project <id>` | List sync targets for a project. Requires `sync:manage` |
| `janus sync get <id>` | Show one sync target (masked config). Requires `sync:manage` |
| `janus sync update <id> [--interval-seconds <n>] [--prune] [--status active\|paused] [...]` | Update a target's interval, prune toggle, status, destination address, or credentials. Requires `sync:manage` |
| `janus sync sync <id>` | Sync a target immediately, bypassing change detection; also clears a `failed` status and retries. Requires `sync:manage` |
| `janus sync delete <id>` | Delete a sync target. Requires `sync:manage` |
| `janus dynamic roles create --config <id> --name <n> --admin-dsn <dsn> --creation <sql> [--revocation <sql>] [--renew <sql>] [--default-ttl-seconds <n>] [--max-ttl-seconds <n>]` | Register a dynamic Postgres role on a config. `--creation` SQL must reference `{{name}}`/`{{password}}`; `--revocation`/`--renew` default to `DROP ROLE`/`ALTER ROLE … VALID UNTIL`. Requires `dynamic:manage`. See the dynamic-credentials runbook (`docs/ops/dynamic.md`) |
| `janus dynamic roles list --config <id>` | List dynamic roles for a config. Requires `dynamic:manage` |
| `janus dynamic roles get <id>` | Show one dynamic role (masked config). Requires `dynamic:manage` |
| `janus dynamic roles delete <id>` | Delete a dynamic role, revoking its still-live leases first. Requires `dynamic:manage` |
| `janus dynamic creds <role-id>` | Issue short-lived credentials from a role; prints the password **once** (never stored). Requires `dynamic:issue` |
| `janus dynamic renew <lease-id>` | Extend a lease by the role's default TTL, capped at its max. Requires `dynamic:issue` |
| `janus dynamic revoke <lease-id>` | Revoke a lease now (drops the DB role). Idempotent. Requires `dynamic:issue` |
| `janus dynamic leases --role <id>` | List a role's leases (status, username, expiry). Requires `dynamic:issue` |
| `janus notifications create --name <n> --type webhook\|slack --url <url> --events <csv> [--hmac-key <k>]` | Create an alerting channel. `--events` is a comma-separated subset of `rotation.failed,sync.failed,promotion.pending,access.denied,breakglass.activated`. `--hmac-key` (webhook only) signs deliveries. Requires `notification:manage` |
| `janus notifications list [--json]` | List channels (masked — no URL/HMAC). Requires `notification:manage` |
| `janus notifications update <id> [--enable\|--disable] [--events <csv>] [--url <url> [--hmac-key <k>]]` | Update a channel. Requires `notification:manage` |
| `janus notifications delete <id>` | Delete a channel and its queued deliveries. Requires `notification:manage` |
| `janus notifications test <id>` | Send a synchronous test notification. Requires `notification:manage` |
| `janus notifications deliveries <id>` | Show recent delivery history (value-free). Requires `notification:manage` |
| `janus break-glass activate --scope instance\|project\|environment [--project <id>] [--environment <id>] --role <r> --reason <text> [--ttl 30m]` | Activate emergency elevation on a scope you already hold a role on; `--role` must exceed your held role. `--ttl` is clamped to `JANUS_BREAKGLASS_MAX_TTL`. Loud + audited |
| `janus break-glass list [--json]` | List active grants (your own, or all if you are an instance admin) |
| `janus break-glass revoke <id>` | End a grant early (self or instance admin) |

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
| `GET /v1/sys/version` | `{"version","commit","date"}` build metadata (matches `janus version`). Requires authentication (no instance permission beyond being logged in) |

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
A user can review and revoke their own sessions self-service:
`GET /v1/auth/sessions` lists active sessions with non-secret client metadata
(IP, user-agent, last-seen) and a current-session marker (no cookie or
credential material is returned); `DELETE /v1/auth/sessions/{id}` revokes one and
`DELETE /v1/auth/sessions` signs out every *other* session. This is surfaced in
the web UI (**Settings → Active sessions**) and the CLI (`janus session`).
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

**Break-glass (emergency elevation).** For incidents, a user can self-service
*raise* their own role on a scope for a bounded time instead of holding standing
admin. It is **guarded**, not approval-gated: you may activate break-glass only
on a scope where you **already hold a role** (no existing binding ⇒ `403`), and
only to a **strictly higher** role (up to `owner`). The requested `ttl` is
clamped to `JANUS_BREAKGLASS_MAX_TTL` (default `1h`) and the grant auto-expires.
A non-empty `reason` is **mandatory**. Every activation is the **loudest** event
in the system — stamped into the audit chain (`breakglass.activate`, with the
scope, elevated role, `expires_at`, and reason) and forwarded to notification
channels (`breakglass.activated`); the activate audit is fail-closed (no
unaudited elevation). See the [break-glass how-to](guides/break-glass.md).

| Endpoint | Purpose | Requires |
|---|---|---|
| `POST /v1/break-glass` | Activate break-glass (scope + role + reason + ttl) | already hold a role on the scope; user account |
| `GET /v1/break-glass` | List active grants (admins see all; users see their own) | authenticated |
| `DELETE /v1/break-glass/{id}` | End a grant early | the grant's owner, or instance `member:manage` |

The audit trail uses three actions: `breakglass.activate` (on activation),
`breakglass.revoke` (early end), and `breakglass.expire` (emitted by a boot-time
sweep for grants that lapsed while the server was down). None carry secret
material — the reason is operator-entered text.

## Secrets API

The project → environment → config → secret hierarchy and its two-level
versioning are served over `/v1/`, JSON. Every route requires an **unsealed**
server (503 while sealed) and the relevant **RBAC permission** (deny-by-default);
service errors map to the standard envelope (`404 not_found`, `409 conflict`,
`400 validation`, `503 sealed`, generic `500`). Values are never echoed in an
error message or a log line.

**Pagination and idempotency (cross-cutting).** List endpoints — projects,
environments, configs, members, tokens, transit keys, users — support opt-in
cursor pagination: `?limit=1-200` returns a page plus an opaque `next_cursor`
(base64url, keyset on `created_at, id`) to pass back as `?cursor=`; omitting
`?limit` returns everything unbounded, so existing callers are unaffected.
`GET /v1/audit/events` paginates separately with its own raw-integer `?cursor=`
keyed on sequence number. Destructive/mutating requests (`POST`/`PUT`/`DELETE`/
`PATCH`) may carry an `Idempotency-Key` header; a repeated key on the same route
replays the first response's status without re-executing the mutation. Keys are
tracked by outcome status only (no request/response bodies are stored) and do
not expire.

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

## Transit (encryption as a service)

The transit engine performs encrypt/decrypt/sign/verify/rewrap and mints data
keys against **instance-scoped named keys** whose material never leaves the
server in plaintext — Janus holds the keys, your app holds the ciphertext. It is
separate from the secret store: no project/env/config hierarchy, and Janus never
persists the data you pass through it. Routes are under `/v1/transit/`, JSON, and
require an **unsealed** server (503 while sealed) plus the relevant RBAC
permission. Full reference — key types, the `janus:v<N>:` envelope, versioning,
and every route — is in [transit.md](transit.md).

Two key types: `aes256-gcm` (encrypt/decrypt/rewrap/datakey) and `ed25519`
(sign/verify). Keys have versions with `latest_version` (target for new
encrypt/sign), `min_decryption_version` (decrypt floor), and `deletion_allowed`
(a delete guard). **Rotate** appends a new version; old data still decrypts.
**Rewrap** rolls old ciphertext forward to the latest version without exposing
plaintext. **Trim** drops archived versions below a floor.

| Action | Grants | Role |
|---|---|---|
| `transit:read` | read metadata, list keys | viewer and up |
| `transit:use` | encrypt/decrypt/sign/verify/rewrap/datakey | developer and up |
| `transit:manage` | create/rotate/trim/config/delete | admin / owner |

**Management** ops (create/rotate/trim/config/delete) are audited fail-closed
(recording the key **name**, never material); **data-plane** ops are not
individually audited (usage metrics are a later Phase-2 sub-project).

**Operator flow.** Mint a transit-scoped service token so an app can call transit
without reaching secrets:

```sh
# 1. Create a key (admin / transit:manage).
curl -XPOST $ADDR/v1/transit/keys -H "Authorization: Bearer $ADMIN" \
  -d '{"name":"app","type":"aes256-gcm"}'

# 2. Mint an all-keys transit-use token (scope.id "" = all keys; a key NAME
#    restricts the token to one key). access is use|manage, not read/readwrite.
curl -XPOST $ADDR/v1/tokens -H "Authorization: Bearer $ADMIN" \
  -d '{"name":"app","scope":{"kind":"transit","id":""},"access":"use"}'
# → {"token":"janus_svc_…", …}  (shown once)

# 3. The app encrypts/decrypts with that token (base64 in/out).
curl -XPOST $ADDR/v1/transit/encrypt/app -H "Authorization: Bearer $TOKEN" \
  -d '{"plaintext":"'"$(printf 'hello' | base64)"'"}'
# → {"ciphertext":"janus:v1:…"}

# 4. After rotating the key, roll old ciphertext forward without exposing it.
curl -XPOST $ADDR/v1/transit/keys/app/rotate -H "Authorization: Bearer $ADMIN"
curl -XPOST $ADDR/v1/transit/rewrap/app -H "Authorization: Bearer $TOKEN" \
  -d '{"ciphertext":"janus:v1:…"}'
# → {"ciphertext":"janus:v2:…"}
```

A transit token can perform only transit actions; a config/environment (secrets)
token has no transit access, and vice versa. Decrypt/verify failures return a
single generic `400` that never distinguishes a bad key, wrong version, or
tampered ciphertext.

## Inheritance & references

Reveal reads **resolve** config inheritance and secret references by default;
`?raw=true` returns stored values verbatim. Full reference:
[references.md](references.md).

- **Inheritance:** a config's `inherits_from` (another config in the **same
  environment**, set at create time) merges the base's values under the config's
  own — **child wins**. A branch may have no own secrets and still be read (it
  returns the base's values). The masked list marks each key `own`, `inherited`,
  or `overridden`. Reading a branch needs no separate grant on its base.
- **References:** a value may embed `${projects.<project>.<env>.<config>.KEY}`
  (absolute) or `${KEY}` (same config), resolved at read time, transitively;
  `$$` escapes a literal `$`. Each dereferenced target requires the caller's own
  `secret:read` (strict — a forbidden reference returns `403` and the whole read
  fails atomically); each dereference is audited as its own `secret.reveal`.
- **Failure codes:** cycle (inheritance or reference) → `409`; unknown target /
  depth-cap → `422`; forbidden reference → `403`; bad `${...}` syntax → `400`.
  Error bodies carry paths, never values.

```sh
# billing/prod/api holds the shared HOST; app/prod/web references it.
curl -XPUT $ADDR/v1/configs/$WEB/secrets -H "$AUTH" \
  -d '{"changes":[{"key":"DB_URL","value":"postgres://${projects.billing.prod.api.HOST}/app"}]}'
curl "$ADDR/v1/configs/$WEB/secrets?reveal=true" -H "$AUTH"   # DB_URL resolved
curl "$ADDR/v1/configs/$WEB/secrets?reveal=true&raw=true" -H "$AUTH"   # DB_URL verbatim ${...}
```

## Static rotation

Rotation policies rotate one config's secret key on an interval — a
`postgres` rotator resets a Postgres role's password via an admin DSN, a
`webhook` rotator POSTs a new HMAC-signed value to an operator endpoint —
managed via `janus rotation …` above (`rotation:manage`, project admin/owner)
or `/v1/rotation/policies`. Rotation pauses while the server is sealed (not
counted as a failure) and retries failures with exponential backoff before
marking a policy `failed` after 5 consecutive failures. Full reference —
crash-safety, the webhook receiver contract, least-privilege Postgres setup,
and backoff/failure semantics — is in the rotation runbook
(`docs/ops/rotation.md`).

## Sync integrations

Sync targets replicate one config's **resolved** secrets (references
expanded) one-way to an external store — `github` (GitHub Actions secrets,
repo- or environment-scoped), `k8s` (a Kubernetes `Secret`, via
server-side apply), `gitlab` (GitLab CI/CD variables), `aws_ssm` (AWS
SSM Parameter Store `SecureString` parameters), `cloudflare` (secret
bindings on a deployed Cloudflare Worker script), or `aws_secrets` (AWS
Secrets Manager named secrets — **billed per secret**) — on an interval
or on demand, managed via
`janus sync …` above (`sync:manage`, project admin/owner) or
`/v1/sync/targets`. A per-target `prune` toggle keeps the destination a
full mirror by deleting keys Janus previously wrote but no longer sees in
the config. Sync pauses while the server is sealed (not counted as a
failure) and retries failures with exponential backoff before marking a
target `failed` after 5 consecutive failures. A config is resolved with a
project-scoped authorizer, so a reference to another project's secrets
fails the sync rather than exfiltrating it. Full reference — credential
least-privilege setup for each provider, prune semantics, the GitHub
key-name constraint, change detection, and backoff/failure semantics — is
in the sync runbook (`docs/ops/sync.md`).

## Dynamic Postgres credentials

Dynamic roles mint **short-lived Postgres credentials on demand** instead
of storing a long-lived password. An admin registers a role on a config —
a privileged **admin DSN** plus `creation`/`revocation`/`renew` SQL
templates (`{{name}}`/`{{password}}`/`{{expiration}}` placeholders) —
managed via `janus dynamic roles …` above (`dynamic:manage`, project
admin/owner) or `/v1/dynamic/roles`. A developer then issues credentials
(`janus dynamic creds`, `dynamic:issue`, project developer+): Janus
generates a unique username/password, runs the creation template to make a
real Postgres role, and records a **lease**. The password is returned
**exactly once** and is never persisted, logged, or audited — lose it and
you re-issue. An in-process lease manager revokes each lease when its TTL
expires (running the revocation template), retries failed revocations,
and reclaims crash-orphaned in-flight leases; it also runs a
revoke-on-startup sweep on the sealed→unsealed edge so leases orphaned by
a crash are cleaned up promptly. Dynamic operations are unavailable while
the server is sealed (the admin DSN cannot be decrypted). Full reference —
template authoring and quoting rules, least-privilege admin-DSN setup, the
lease lifecycle and crash-safety, TTL/renewal semantics, and RBAC/audit —
is in the dynamic-credentials runbook (`docs/ops/dynamic.md`).

## Notifications (outbound alerting)

Notification **channels** route operational events to a generic **webhook**
or a **Slack** incoming webhook so failures find humans instead of waiting to
be noticed. A channel subscribes to one or more event kinds —
`rotation.failed`, `sync.failed`, `promotion.pending` (a request awaiting
approval), and `access.denied` (any 403). Manage channels via
`janus notifications …` (`notification:manage`, instance admin/owner) or
`/v1/notifications/channels`; the destination **URL** and optional webhook
**HMAC signing key** are write-only (envelope-encrypted under the master key,
never returned or logged).

Delivery is **decoupled and crash-safe**: a dispatcher (interval
`JANUS_NOTIFY_TICK`, default `30s`) tails the **audit log** from a persisted
cursor and, for each matching event, enqueues a delivery per subscribing
channel into an outbox, then sends it — retrying with exponential backoff
(1m→1h, giving up after six attempts). Because notifications are rendered
from the audit log, which has **no value field by construction**, a
notification can never carry a secret value — it reports the event kind,
resource **path/name**, actor, and a sanitized category detail only. Webhook
payloads are signed `X-Janus-Signature: sha256=<hmac>` when a key is set;
Slack channels receive a compact `{"text": …}` message. `POST
…/channels/{id}/test` sends a synchronous test; `GET …/channels/{id}/deliveries`
shows recent (value-free) delivery history. The dispatcher is a clean no-op
while the server is sealed. Notification channels are **not** included in a
backup — reconfigure them after a restore.

## Secrets CLI (developer & CI flows)

The same `janus` binary is the client for the secrets routes above. Full
reference — every command, the credential/address/binding precedence tables, the
`.janus.yaml` format, and the `run` / `--plain` semantics — is in
[cli.md](cli.md). The operator-facing essentials:

**Credentials.** Two tiers. Humans run `janus login` (email + password →
a `janus_session` cookie stored in `<config-dir>/auth.json`, mode `0600`);
sessions have a 24h server TTL, so re-login daily. Machines/CI set
`JANUS_TOKEN=janus_svc_…` (a scoped service token minted with `POST /v1/tokens`,
shown once), sent as a bearer token. Precedence per request:
`--token > JANUS_TOKEN > stored session`. Address precedence:
`--address > JANUS_ADDR > auth.json > http://127.0.0.1:8200`. The config
directory is `os.UserConfigDir()/janus` (`~/.config/janus`, `%AppData%\janus`,
or `~/Library/Application Support/janus`); **`JANUS_CONFIG_DIR` overrides it**
wholesale — use it to relocate CLI state or to isolate a CI run portably.

**Binding a directory.** `janus setup --project P --env E --config C` validates
each against the server, then writes `.janus.yaml` (human slugs only, safe to
commit) into the cwd. Projects/environments match by slug, configs by name.
Precedence per field: `--project/--env/--config flags > JANUS_PROJECT/ENV/CONFIG
> .janus.yaml` (cwd only — no parent-directory walk). Teammates who clone inherit
the binding.

**Human developer flow:**

```sh
janus login --address http://localhost:8200   # → session in auth.json
cd my-service && janus setup                    # → .janus.yaml (validated)
janus secrets set DATABASE_URL=postgres://…     # one new config version
janus secrets list                              # masked KEY/VERSION/UPDATED (not audited)
janus run -- ./my-service                       # secrets injected as env vars
```

`janus run` does one **audited** bulk reveal, overlays the secrets onto the
parent environment (the **secret wins** on a name clash; `--preserve-env` flips
that so an existing env var wins), then execs the command after the required
`--`. The child inherits stdin/stdout/stderr; signals are forwarded (best-effort
on Windows) and the child's exit code is propagated verbatim. Secret values only
ever reach the child's environment — never disk, never the CLI's own logs.

**CI / machine flow** (no interactive login):

```sh
export JANUS_TOKEN=janus_svc_…                  # scoped, read or read/write
export JANUS_ADDR=https://janus.internal
janus run --project acme --env prod --config prod -- ./deploy.sh
```

**Exporting to a file.** `janus secrets download --format env|json|yaml` streams
to stdout by default (a `> file` redirect is your own act — no flag needed). To
have the CLI write the file itself you must pass `--plain` (`--output PATH`
without it refuses and writes nothing); with `--plain --output` the file is
created mode `0600`. Prefer streaming or ephemeral files — a plaintext `.env` on
disk is the CLI's least-safe output.

**Operator note (`janus seal`).** Sealing is gated (`sys:seal`, owner/admin),
and `janus seal` authenticates with the same credential resolution as the
secrets commands: `--token` > `JANUS_TOKEN` > the stored session from
`janus login`. Without a valid admin credential it fails with an actionable
hint (`not authenticated — run \`janus login\``). Unlike `init`/`unseal`/
`seal-status`, it is not a pre-auth command.

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
`auth.logout`, `auth.password_change`, session revocation
(`auth.session.revoke`, `auth.session.revoke_others`), and notification channel
management (`notification.channel.create/update/delete`, `notification.channel.test`).
Every denied (`403`)
authorization decision on these endpoints is recorded as a `denied` event.
**Not** audited: masked/metadata reads (token/user/member `LIST`,
`/v1/auth/me`, the session **list** `/v1/auth/sessions`, and
`/v1/audit/verify`/`/v1/audit/events`/`/v1/audit/histogram` themselves —
`/v1/audit/export` is the one audit-log read that IS self-audited, since it's a
bulk export); `sys.init`/`sys.unseal` (pre-auth bootstrap operations).

**Engine action endpoints — a deliberate exception (decided 2026-07-23).** The
three Phase-3 engines expose action endpoints that produce an *external* side
effect: rotation `POST /v1/rotation/{id}/rotate`, sync `POST /v1/sync/{id}/sync`,
and dynamic `issue`/`renew`/`revoke`. For these, the **authorization/denial
path is fail-closed** (a `403` is recorded before anything runs), but the
**success audit event is the engine's best-effort write** — the engine rotates
the real Postgres password (or writes GitHub/k8s secrets, or issues a DB role),
records the run, then writes the audit event; if that final audit write fails it
is logged as a warning and the request still reports success. This is
intentional and applies **uniformly across all three engines**: the side effect
has already happened in an external system and cannot be undone by a late audit
failure, so failing the request after the fact would falsely imply a rollback
that did not occur. The engines' own `*_runs` tables provide a second durable
record of every attempt independent of the hash chain. Endpoints whose mutation
is *internal* to Janus (secrets, tokens, keys, policy, members) remain strictly
fail-closed as above — the mutation and its audit event commit together.

| Route | Behavior | Requires |
|---|---|---|
| `GET /v1/audit/verify` | Walks the chain, recomputing every hash and checking linkage. `{"valid":true,"count":N,"head_seq":N,"head_hash":"<hex>"}` when intact; `{"valid":false,"broken_at_seq":K,"reason":"hash_mismatch"\|"chain_break"}` on the first break. Not self-audited. | `audit:read` (owner/admin) |
| `GET /v1/audit/export` | Streams matching events, chunked. `?format=jsonl` (default, `application/x-ndjson`) or `?format=csv`. Filters (AND-combined): `?from=`/`?to=` (RFC3339), `?actor=` (matches actor id **or** name), `?action=`, `?result=` (`success`/`denied`/`error`). Each row includes `prev_hash`+`hash` (hex) for offline verification. Self-audited (`audit.export`) **before** streaming. | `audit:read` (owner/admin) |
| `GET /v1/audit/events` | Paginated viewer page: same filters as export, plus `?limit=` (1-200, default 50) and a sequence-number `?cursor=`. `{"events":[...],"next_cursor":N\|null}`. Not self-audited. | `audit:read` (owner/admin) |
| `GET /v1/audit/histogram` | Bucketed event counts for a chart: requires `?from=`/`?to=`, optional `?bucket=hour\|day` (default `day`, range capped at 1000 buckets). `{"buckets":[{"start","success","denied","error"}]}` — counts only, value-free. Not self-audited. | `audit:read` (owner/admin) |

All four are gated by `RequireAuth` + `audit:read`, so they answer `503` while the
server is sealed and `403` for a caller without the permission. Invalid
`format`/`from`/`to`/`result`/`limit`/`cursor`/`bucket` → `400 validation`.

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
  unsealing requires valid shares. The `janus seal` CLI authenticates
  accordingly (`--token` > `JANUS_TOKEN` > stored session from `janus login`).
- **Rate limiting keys on `RemoteAddr`.** The login limiter buckets by direct
  peer address; behind a TLS-terminating proxy that collapses to one bucket.
  Add trusted-proxy `X-Forwarded-For` handling when a proxy is introduced (the
  same caveat affects the conditional cookie `Secure` flag).
- **No TLS yet** — terminate TLS at a reverse proxy for now; native TLS is a
  later milestone. Shares transit the network in the clear otherwise.
