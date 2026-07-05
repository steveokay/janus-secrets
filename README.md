# Janus

A single-tenant, self-hosted secrets manager, deployed as one Go binary plus
PostgreSQL. It combines ideas from Doppler (project/environment/config model,
`run` injection), Vault (transit encryption, dynamic secrets, hash-chained
audit), and AWS KMS (encrypt-as-a-service with key versioning).

> **Status: early development.** Phase 1 is in progress. The cryptographic
> core (envelope encryption + unseal), the storage layer (Postgres persistence,
> migrations, two-level versioning), the encryption-orchestration service, the
> **runnable server** (init/unseal over HTTP, `janus` CLI, docker-compose
> stack), **authentication** (email/password sessions + scoped service tokens),
> and **RBAC** (roles/scopes, deny-by-default enforcement) are complete and
> tested against real Postgres. Still to come before Janus is usable as a
> secrets manager: the hash-chained audit log, the secret-facing REST API, and
> the secrets CLI (`janus run`). See [Roadmap](#roadmap) for the honest current
> state.
>
> **Docs:** how each subsystem works is documented under [`docs/`](docs/) —
> [architecture](docs/architecture.md), [cryptography](docs/crypto.md), the
> [data model & versioning](docs/data-model.md), and
> [operations](docs/operations.md).

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
plaintext. Config inheritance and secret references are deferred to a later
milestone.

## Tech stack

- **Server / CLI:** Go (stdlib-first, minimal dependencies).
- **Crypto:** Go stdlib `crypto/*` and `golang.org/x/crypto` only, plus AWS KMS
  (used as a service, not a crypto library) and a vendored copy of HashiCorp
  Vault's Shamir implementation (MPL-2.0). No third-party crypto primitives.
- **Storage:** PostgreSQL 16+ via `pgx`, migrations with `golang-migrate`.
- **HTTP:** `net/http` with `chi`, REST + JSON under `/v1/` (sys, auth, token,
  user, and membership routes live; secret routes arrive with the API milestone).
- **AuthN/Z:** Argon2id passwords, HMAC-SHA256 token hashing, opaque sessions,
  and a pure deny-by-default RBAC engine.
- **CLI:** `cobra` (`janus server/init/unseal/seal-status/seal/migrate`).
- **Web UI:** React + TypeScript + Vite, embedded in the binary via `go:embed`
  *(planned)*.
- **Deployment:** multi-stage Dockerfile + docker-compose (app + Postgres).

## Repository layout

```
cmd/janus/           single binary: server + operator CLI (cobra); ← implemented
                     secrets CLI (`janus run`) planned
internal/crypto/     envelope encryption, key hierarchy, unseal    ← implemented
internal/crypto/shamir/  vendored HashiCorp Shamir (MPL-2.0)
internal/store/      Postgres repositories, migrations, versioning ← implemented
internal/secrets/    encryption orchestration + secrets CRUD       ← implemented
internal/api/        HTTP server, sys/auth/token/user/member routes ← implemented
internal/auth/       passwords, sessions, service tokens           ← implemented
                     (OIDC/federation planned for Phase 2)
internal/authz/      RBAC engine (roles, scopes, enforcement)      ← implemented
internal/audit/      hash-chained audit log (planned)
migrations/          SQL migrations
web/                 React SPA (planned)
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
they skip (they do not fail). The pure-logic packages `internal/crypto` and
`internal/authz` are held to **100% statement coverage**, enforced in CI;
crypto includes tamper, nonce-reuse, and secret-leak tests, and authz an
exhaustive role→action matrix test. CI also runs `go vet`, `govulncheck`, and
`gosec`. The Go
toolchain is pinned to `go1.26.4` (via a `toolchain` directive) as a security
floor.

## Security notes

- AES-256-GCM for all symmetric encryption; random nonces, never reused.
- Constant-time comparison for key-check, token, and MAC checks; only token
  HMACs are stored (never raw tokens), and session/token verification requires
  an unsealed server.
- Zero plaintext secrets or key material in logs or error messages — enforced
  by leak tests at the crypto, secrets, and HTTP layers (the request logger is
  structurally body-free).
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

## Roadmap

**Phase 1 — Core (usable Doppler replacement):**
crypto + unseal ✅ → store + migrations + versioning ✅ → CRUD service +
encryption orchestration ✅ → server bootstrap (sys API + `janus` CLI) ✅ →
auth (passwords, service tokens) ✅ → RBAC (roles, scopes, enforcement) ✅ →
audit log → REST API → CLI with `run`. Live tracker: [status.md](status.md).

**Phase 2 — Transit + UI:** transit/KMS engine (named keys, encrypt/decrypt/
sign/verify, key versioning); React SPA; OIDC login; usage metrics.

**Phase 3 — Rotation + dynamic:** scheduled static rotation; sync integrations
(GitHub Actions, Kubernetes); dynamic Postgres credentials with a lease manager.

### Non-goals

HA/Raft clustering, PKI/certificate authority, SSH signing, HSM/PKCS#11,
multi-tenancy/organizations, and FIPS certification claims are explicitly out of
scope.

## License

Not yet chosen. The vendored `internal/crypto/shamir/` package is under MPL-2.0
(see its `LICENSE`); its headers are retained.
