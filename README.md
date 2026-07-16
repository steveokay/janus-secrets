# Janus

A single-tenant, self-hosted secrets manager, deployed as one Go binary plus
PostgreSQL. It combines ideas from Doppler (project/environment/config model,
`run` injection), Vault (transit encryption, dynamic secrets, hash-chained
audit), and AWS KMS (encrypt-as-a-service with key versioning).

> **Status: Phases 1–3 complete.** All three build phases have shipped and are
> tested against real Postgres:
>
> - **Phase 1 — Core:** the cryptographic core (envelope encryption + unseal),
>   the storage layer (Postgres persistence, migrations, two-level versioning),
>   the encryption-orchestration service, the runnable server (init/unseal over
>   HTTP, docker-compose stack), authentication (email/password sessions + scoped
>   service tokens), deny-by-default RBAC, the hash-chained audit log
>   (tamper-evident, fail-closed, `verify`/`export`), the secret-facing REST API
>   (project/env/config CRUD + lifecycle, secret masked-list/reveal/write/delete,
>   config version list/diff/rollback), config inheritance + secret references,
>   and the secrets CLI (`janus login`/`setup`/`secrets`/`run`).
> - **Phase 2 — Transit + UI:** the Vault-style transit (encryption-as-a-service)
>   engine; the **React SPA** (embedded via `go:embed`, served same-origin) —
>   unseal/login, project/env/config nav, the flagship secret editor, version
>   diff, audit viewer, token/member management, transit console, and the
>   operations console; **OIDC** human login **and** OIDC-federated CI machine
>   identity; and the usage-metrics ("Reads 24h") dashboard.
> - **Phase 3 — Rotation + dynamic:** scheduled static rotation (Postgres +
>   webhook), sync integrations (GitHub Actions + Kubernetes), and dynamic
>   Postgres credentials with a lease manager.
>
> Janus is usable end-to-end: `docker compose up`, create a project in the UI or
> CLI, set secrets, and `janus run` injects them into your process. What remains
> is polish and release hygiene, tracked in [gaps.md](gaps.md).
>
> **Docs:** full documentation lives under [`docs/`](docs/) (see the
> [docs index](docs/README.md)). New here? Start with the task-oriented
> **how-to guides**: [getting started](docs/guides/getting-started.md),
> [injecting secrets](docs/guides/injecting-secrets.md),
> [managing secrets](docs/guides/managing-secrets.md),
> [service tokens](docs/guides/service-tokens.md),
> [GitHub Actions](docs/guides/github-actions.md),
> [Docker](docs/guides/docker.md),
> [Kubernetes](docs/guides/kubernetes.md), and
> [production deployment](docs/guides/production-deployment.md) (TLS
> termination, configuration, unseal, sizing, backups, and upgrades).
> The subsystem **references** cover
> [architecture](docs/architecture.md), [cryptography](docs/crypto.md), the
> [data model & versioning](docs/data-model.md),
> [references & inheritance](docs/references.md), [operations](docs/operations.md),
> the [CLI reference](docs/cli.md), the [transit engine](docs/transit.md),
> [OIDC login](docs/oidc.md), [CI federation](docs/ci-federation.md), the
> [web UI](docs/web.md), and operations for
> [rotation](docs/ops/rotation.md), [sync](docs/ops/sync.md),
> [dynamic secrets](docs/ops/dynamic.md), and
> [backup & restore](docs/ops/backup-restore.md).

## Why

Self-hosted secrets management that you fully own: no SaaS, no multi-tenancy, no
per-seat pricing. One binary, one Postgres, your keys never leave your server in
plaintext.

## Design

### Envelope encryption

A three-level key hierarchy, so no single stored value can decrypt secrets on
its own:

1. **Master key (root KEK)** — 256-bit, exists only in server memory after
   unseal, never persisted in plaintext.
2. **Project KEKs** — one per project, wrapped by the master key, stored wrapped.
3. **DEKs** — one per secret version (AES-256-GCM), wrapped by the project KEK.
   Nonces are random and stored alongside the ciphertext.

Every wrapped key is bound to its storage location with authenticated
additional data (AAD), so a ciphertext copied to a different row fails to
decrypt — this defeats wrapped-key-swap attacks.

### Unseal

The server starts **sealed**: the master key is not in memory and all secret
operations fail until an operator unseals it. Two mechanisms ship from day one,
behind a common `Unsealer` interface:

- **Shamir** — the master key is split k-of-n (default 3-of-5, configurable).
  Operators submit shares interactively until the threshold is met.
- **Cloud KMS auto-unseal** — the master key is wrapped by a cloud KMS key
  (AWS KMS first) and recovered automatically at startup with a single decrypt.

A **key check value** (a known constant encrypted under the master key) lets
unseal reject a wrong-but-well-formed master key before it is ever used.

The server exposes the seal lifecycle under `/v1/sys/` (`health`,
`seal-status`, `init`, `unseal`, `unseal/reset`, `seal`); every other route
returns `503 {"error":{"code":"sealed"}}` until unsealed. `init` and `unseal`
are unauthenticated by bootstrap necessity (the Vault model); `POST /v1/sys/seal`
re-seals a running server and now **requires the `sys:seal` permission**.

### Identity & access

Two authentication methods ship today: **email + password** (Argon2id, opaque
Postgres-backed sessions via an HTTP-only cookie) for humans, and **scoped
service tokens** (`janus_svc_…`, shown once at creation, only their HMAC is
stored) for machines. A single `Principal{Kind,ID,Name}` is the seam that
authorization, audit, and Phase-2 federation build on. The initial admin is
created during the init ceremony (one-time password shown once).

Authorization is **deny-by-default RBAC**. Four roles — viewer ⊂ developer ⊂
admin ⊂ owner — bind to a user at **instance**, **project**, or **environment**
scope, with top-down inheritance (an instance binding applies everywhere; a
project binding applies to that project's environments and configs) and a
most-permissive union across a user's bindings. Service tokens get only
least-privilege secret/config capabilities at their exact scope and can never
perform management or instance actions. Two safety rails: a **delegation
constraint** (you cannot grant a role above your own) and a **never-lock-out**
guard (the last instance owner cannot be removed, demoted, or disabled). The
engine is a pure decision function (`internal/authz`); handlers enforce it
explicitly, so the storage and secrets layers stay identity-free.

### Audit log

Every authenticated request that performs a sensitive action writes an
**append-only, tamper-evident** event: actor, action, resource path, result, IP,
and timestamp, plus the SHA-256 of the previous event (a hash chain). The hash is
canonical — domain-tagged, length-prefixed fields, a presence byte so `NULL` and
`""` never collide — and an event has **no value field by construction**, so a
secret value can never be recorded. Each append serializes on a Postgres
transaction advisory lock, so the chain stays contiguous under concurrency.
Recording is **synchronous and fail-closed**: if the audit write fails, the
request fails — no mutation is left unaudited. Services stay audit-blind; only the
API layer records (a one-line `s.record(...)` per handler), with denied requests
captured centrally. `GET /v1/audit/verify` walks the chain and reports integrity
(the future UI's "chain verified" badge); `GET /v1/audit/export` streams the log
as JSONL or CSV, filterable by time/actor/action/result, with each row carrying
`prev_hash`+`hash` for offline verification. The engine (`internal/audit`) is
pure and HTTP-free.

### Transit (encryption as a service)

The first Phase-2 subsystem is live: a Vault-style **transit engine**
(`internal/transit`) that encrypts, decrypts, signs, verifies, rewraps, and mints
data keys against **instance-scoped named keys whose material never leaves the
server in plaintext** — Janus holds the keys, your app holds the ciphertext, and
Janus never persists the data you pass through it. Two key types (`aes256-gcm` for
encrypt/decrypt/rewrap/datakey, `ed25519` for sign/verify), key **versioning**
(`latest_version`, `min_decryption_version`, `deletion_allowed`) with rotate,
trim, and rewrap-forward, and a `janus:v<N>:<base64>` envelope. Each version's
material is master-key-wrapped under a name+version AAD, so a copied version row
fails to unwrap. Routes under `/v1/transit/*` are RBAC-enforced by three
instance-scoped actions (`transit:read`/`use`/`manage`) and a new **transit
service-token scope** so an app can call transit without reaching secrets;
management ops are audited (recording the key name, never material) while
high-frequency data-plane ops are not. See [docs/transit.md](docs/transit.md).

### OIDC & CI federation

Beyond passwords and service tokens, Janus supports **OIDC** for humans
(Authorization Code + PKCE + state + nonce against a generic provider, tested
against GitHub and Google; the client secret is master-key-wrapped and login is
CSRF-hardened with a browser-bound state cookie) and **OIDC-federated machine
identity** for CI: a GitHub Actions workflow exchanges its OIDC JWT for a
short-lived, scoped `janus_svc_` token via `POST /v1/auth/oidc/federate`, gated
by admin-authored structured-claim trust bindings (repository required,
exactly-one match, TTL ≤ 1h) — no long-lived secret in the CI system. Both are
admin-configured under `/v1/sys/oidc*`. See [docs/oidc.md](docs/oidc.md) and
[docs/ci-federation.md](docs/ci-federation.md).

### Web UI

A **React + TypeScript + Vite + Tailwind** SPA is built to static assets and
**embedded in the `janus` binary** via `go:embed`, served same-origin by the Go
server (no Node in production). It covers in-browser Shamir unseal and login, the
project → environment → config tree, the flagship **secret editor** (masked list
with origin badges, audited per-key/bulk reveal, a client-side dirty buffer, and
batched "Save as vN"), config version diff, the audit viewer with chain-verify
badge and export, token/member management, the transit key console, a usage
dashboard ("Reads 24h"), and an **operations console** over the three Phase-3
engines (rotation, sync, dynamic leases — manage and act, not create). The visual
system is dual-theme (dark-first + light) via CSS-variable tokens. Revealed
plaintext and unseal shares never enter the Query cache or storage. See
[docs/web.md](docs/web.md).

### Rotation, sync & dynamic secrets (Phase 3)

Three engines extend Janus past static storage:

- **Static rotation** (`internal/rotation`) — scheduled, webhook-notified secret
  rotation with a crash-safe persist → apply → commit sequence; Postgres
  (`ALTER ROLE`) and generic-webhook (HMAC) rotators ship first.
- **Sync integrations** (`internal/secretsync`) — outbound one-way replication of
  a config's resolved secrets to **GitHub Actions secrets** (NaCl sealed-box) and
  **Kubernetes Secrets** (server-side apply, verified TLS), with keyed-HMAC change
  detection and a project-scoped resolver that blocks cross-project exfiltration.
- **Dynamic Postgres credentials** (`internal/dynamic`) — Vault-style
  config-scoped dynamic roles from admin-authored creation/revocation SQL
  templates, with a lease manager (TTL, monotonic renewal capped at max-TTL,
  revoke-on-expiry, and a revoke-on-startup sweep for leases orphaned by a crash).
  The issued password is returned exactly once and never persisted or audited.

Each engine has a `/v1/{rotation,sync,dynamic}` API, a `janus` CLI surface, and
runs its scheduler in-process on a `JANUS_*_TICK` interval.

## Quickstart (dev)

```sh
make dev-up     # build, docker compose up, init a 1-of-1 dev seal, unseal
bin/janus seal-status
```

The dev seal stores its single share in `.dev/janus-share` (gitignored) — that
share IS the master key; this flow is for local development only. Production
uses a real k-of-n split: `janus init` prints the shares exactly once, and
operators unseal with `janus unseal` (share read from stdin with echo off).

Server configuration is env-only:

| Variable | Meaning |
|---|---|
| `JANUS_DATABASE_URL` | Postgres DSN (required) |
| `JANUS_LISTEN_ADDR` | listen address, default `:8200` |
| `JANUS_SEAL_TYPE` | `shamir` or `awskms`; required before first init, stored type is authoritative after |
| `JANUS_AWS_KMS_KEY_ARN` | KMS key for `awskms` (plus standard AWS SDK env) |
| `JANUS_ADDR` | CLI default server address |

### Data model

Doppler-style hierarchy: **Project → Environment → Config → Secrets**, with
two-level versioning (immutable config versions for diff/rollback, plus
per-secret value history). The schema, migrations, and repositories are built
and tested — see [docs/data-model.md](docs/data-model.md). The store is
**crypto-blind**: it persists opaque ciphertext and never holds a key or
plaintext. Configs can **inherit** from a base config in the same environment
(child wins per key) and secret values can embed **references** —
`${projects.<project>.<env>.<config>.KEY}` or local `${KEY}` — resolved at read
time, transitively, with cycle detection and strict per-target authorization.
See [docs/references.md](docs/references.md).

## CLI

The same `janus` binary is the client. A typical developer flow:

```sh
janus login --address http://localhost:8200      # email + password → session
cd my-service && janus setup                       # writes .janus.yaml (project/env/config)
janus secrets set DATABASE_URL=postgres://…        # one new config version
janus run -- ./my-service                          # inject secrets as env vars
janus secrets download --format env --output .env --plain
```

Machine/CI: set `JANUS_TOKEN=janus_svc_…` (a scoped service token minted with
`POST /v1/tokens`) instead of logging in — it is sent as a bearer token and
takes precedence over any stored session. Full command reference, the
credential/address/binding precedence rules, the `.janus.yaml` format, and the
`run` / `--plain` semantics are in [docs/cli.md](docs/cli.md).

## Tech stack

- **Server / CLI:** Go (stdlib-first, minimal dependencies).
- **Crypto:** Go stdlib `crypto/*` and `golang.org/x/crypto` only, plus AWS KMS
  (used as a service, not a crypto library) and a vendored copy of HashiCorp
  Vault's Shamir implementation (MPL-2.0). No third-party crypto primitives.
- **Storage:** PostgreSQL 16+ via `pgx`, migrations with `golang-migrate`.
- **HTTP:** `net/http` with `chi`, REST + JSON under `/v1/` (sys, auth, OIDC,
  token, user, membership, audit, metrics, transit, and the secret routes —
  projects/environments/configs, `configs/{cid}/secrets`
  masked-list/reveal/write/delete, and `versions` list/diff/rollback — plus the
  Phase-3 `rotation`/`sync`/`dynamic` engines — all live).
- **AuthN/Z:** Argon2id passwords, HMAC-SHA256 token hashing, opaque sessions,
  OIDC (Auth Code + PKCE) human login and OIDC-federated CI identity, and a pure
  deny-by-default RBAC engine.
- **CLI:** `cobra` (server/ops: `janus server/init/unseal/seal-status/seal/
  migrate`; secrets: `janus login/logout/setup/secrets/run`; Phase-3:
  `janus rotation/sync/dynamic`).
- **Web UI:** React + TypeScript + Vite + Tailwind + TanStack Query, built to
  static assets and embedded in the binary via `go:embed` (no Node in
  production).
- **Deployment:** multi-stage Dockerfile (build web → embed → build Go) +
  docker-compose (app + Postgres).

## Repository layout

```
cmd/janus/           single binary: server + operator CLI + secrets CLI      ← implemented
                     (login/setup/secrets/run) + rotation/sync/dynamic, all cobra
internal/crypto/     envelope encryption, key hierarchy, unseal              ← implemented
internal/crypto/shamir/  vendored HashiCorp Shamir (MPL-2.0)
internal/store/      Postgres repositories, migrations, versioning           ← implemented
internal/secrets/    encryption orchestration + secrets CRUD                 ← implemented
internal/resolve/    config inheritance + secret-reference resolution        ← implemented
internal/api/        HTTP server + all /v1 routes (sys/auth/oidc/token/user/  ← implemented
                     member/audit/metrics/secret/version/transit/rotation/
                     sync/dynamic)
internal/auth/       passwords, sessions, service tokens, OIDC + federation   ← implemented
internal/authz/      RBAC engine (roles, scopes, enforcement)                ← implemented
internal/audit/      hash-chained audit log                                  ← implemented
internal/transit/    transit engine (encrypt/decrypt/sign/verify, versioning) ← implemented
internal/rotation/   scheduled static rotation (Postgres + webhook)          ← implemented
internal/secretsync/ sync to GitHub Actions + Kubernetes Secrets             ← implemented
internal/dynamic/    dynamic Postgres credentials + lease manager            ← implemented
internal/web/        //go:embed dist + SPA handler (CSP, deep-link fallback) ← implemented
migrations/          SQL migrations (000001–000012)
web/                 React SPA (Vite + TS + Tailwind + TanStack Query)       ← implemented
docs/                subsystem docs, design specs, implementation plans
```

## Building and testing

Requires a recent Go toolchain. Postgres (for later milestones) is provided via
docker-compose.

```sh
go build ./...                 # build
go test ./...                  # run all tests
go test -race ./internal/crypto/   # crypto tests with the race detector

make test                      # go test -race ./...
make build                     # build the server binary
make dev-up                    # full stack: compose up + dev init/unseal
docker compose up -d           # start Postgres + the janus server (sealed)
make migrate                   # apply the schema explicitly (server also auto-migrates)
```

The `internal/store`, `internal/secrets`, and `internal/api` integration tests
run against a real PostgreSQL via
[testcontainers](https://testcontainers.com/) and require Docker; without it
they skip (they do not fail). The pure-logic packages `internal/crypto`,
`internal/authz`, and `internal/audit` are held to **100% statement coverage**,
enforced in CI; crypto includes tamper, nonce-reuse, and secret-leak tests,
authz an exhaustive role→action matrix test, and audit hash-determinism,
tamper, chain-break, and genesis tests. CI also runs `go vet`, `govulncheck`,
and `gosec`. The Go
toolchain is pinned to `go1.26.5` (via a `toolchain` directive) as a security
floor.

## Security notes

- AES-256-GCM for all symmetric encryption; random nonces, never reused.
- Constant-time comparison for key-check, token, and MAC checks; only token
  HMACs are stored (never raw tokens), and session/token verification requires
  an unsealed server.
- Zero plaintext secrets or key material in logs or error messages — enforced
  by leak tests at the crypto, secrets, HTTP, and audit layers (the request
  logger is structurally body-free; an audit `Event` has no value field, so a
  secret can never enter the audit log).
- Every ciphertext's AAD binds it to its exact storage slot (project,
  config/key path, value version), so relocated or swapped ciphertext fails
  closed.
- Seal config lives in Postgres (the file-based store remains for tests). The
  server runs TLS-less behind your own network for now — terminate TLS at a
  reverse proxy; native TLS is a later milestone.
- `POST /v1/sys/seal` requires the `sys:seal` permission; `init` and `unseal`
  are unauthenticated by bootstrap necessity, matching the Vault model.
- RBAC is deny-by-default; denied requests return a generic `403 forbidden` that
  never leaks role names, bindings, or query internals (enforced by a leak test).
- The audit log is append-only and hash-chained; recording is fail-closed (an
  audit-write failure fails the request), and `GET /v1/audit/verify` detects any
  field mutation, deletion, or reorder of past events.

## Roadmap

**Phase 1 — Core (usable Doppler replacement):**
crypto + unseal ✅ → store + migrations + versioning ✅ → CRUD service +
encryption orchestration ✅ → server bootstrap (sys API + `janus` CLI) ✅ →
auth (passwords, service tokens) ✅ → RBAC (roles, scopes, enforcement) ✅ →
audit log (hash-chained, tamper-evident) ✅ → REST API ✅ → CLI with `run` ✅ →
config inheritance + secret references ✅. **Phase 1 complete.** Live tracker:
[status.md](status.md).

**Phase 2 — Transit + UI:** transit/KMS engine ✅ (sub-project A — see
[docs/transit.md](docs/transit.md)); React SPA ✅ (sub-project B — embedded
same-origin SPA: unseal/login, project/env/config nav, the flagship secret editor
with audited reveal + batched Save-as-vN, config version diff, audit viewer,
token/member management, transit console, dashboard, and the operations console —
see [docs/web.md](docs/web.md)); OIDC login + CI federation ✅ (sub-project C —
[docs/oidc.md](docs/oidc.md), [docs/ci-federation.md](docs/ci-federation.md));
usage metrics ✅ (sub-project D — "Reads 24h"). **Phase 2 complete.**

**Phase 3 — Rotation + dynamic:** scheduled static rotation (Postgres +
webhook) ✅; sync integrations (GitHub Actions, Kubernetes) ✅; dynamic Postgres
credentials with a lease manager ✅. **Phase 3 complete.**

All three build phases have shipped. Remaining work is polish, spec debt, and
release hygiene — see the [gap analysis](gaps.md) and the live
[status tracker](status.md).

### Non-goals

HA/Raft clustering, PKI/certificate authority, SSH signing, HSM/PKCS#11,
multi-tenancy/organizations, and FIPS certification claims are explicitly out of
scope.

## License

Janus is licensed under the **Apache License, Version 2.0** — see
[`LICENSE`](LICENSE) and [`NOTICE`](NOTICE).

The vendored `internal/crypto/shamir/` package is licensed under
**MPL-2.0** (see its `LICENSE`); its per-file headers are retained. MPL-2.0
is file-level copyleft and compatible with Apache-2.0 distribution.
