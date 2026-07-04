# CLAUDE.md

## Project: Janus — self-hosted, single-tenant secrets manager

A single-tenant, self-hosted secrets manager combining the best of Doppler (project/env/config model, `run` injection), Vault (transit encryption, dynamic secrets, audit), and AWS KMS (encrypt-as-a-service with key versioning). Deployed as one Go binary + Postgres via docker-compose.

## Tech stack

- **Server:** Go (latest stable). Standard library first; minimal dependencies.
- **Storage:** PostgreSQL 16+ via `pgx`. Migrations with `golang-migrate`, SQL files in `migrations/`.
- **Crypto:** Go stdlib `crypto/*` + `golang.org/x/crypto` ONLY. Never implement crypto primitives. Never add third-party crypto libraries without explicit discussion.
- **HTTP:** `net/http` with `chi` router. REST + JSON, all routes under `/v1/`.
- **CLI:** `cobra`. A single `janus` binary provides both the server (`janus server`) and the secrets CLI (`janus run`, `janus secrets …`); there is no separate `kh` binary.
- **Web UI:** React + TypeScript + Vite + Tailwind + TanStack Query, in `web/`. Built to static assets and embedded in the Go binary via `go:embed`. No Node server in production. No Next.js.
- **Deployment:** Dockerfile (multi-stage: build web → build Go with embedded assets) + docker-compose.yml (app + Postgres).

## Repository layout

```
cmd/janus/        server + CLI entrypoint (single `janus` binary)
internal/crypto/     envelope encryption, key hierarchy, unseal
internal/store/      Postgres repositories, migrations
internal/api/        HTTP handlers, middleware, routes
internal/auth/       tokens, OIDC, sessions
internal/authz/      RBAC engine
internal/audit/      hash-chained audit log
internal/transit/    KMS/transit engine (phase 2)
internal/dynamic/    dynamic secrets + lease manager (phase 3)
internal/rotation/   static rotation + sync integrations (phase 3)
web/                 React SPA
migrations/          SQL migrations
```

## Data model (Doppler-style)

Hierarchy: **Project → Environment (dev/staging/prod, user-definable) → Config → Secrets (key/value)**.

- **Two-level versioning:** each save (which may batch edits to multiple secrets) creates one immutable **config version** (v1, v2, ...) — the unit of diff and rollback. Each secret additionally has its own **value version history** for per-key trace. The UI edits in a dirty-state buffer and commits all changes as a single config version ("Save as vN").
- Reads default to latest config version. Soft delete with undelete; hard destroy is a separate explicit operation.
- Configs can inherit from a base config within the same environment (root config + branch configs, like Doppler).
- Secret values support references: `${projects.other.prod.KEY}` resolved at read time, cycle-checked.

## Cryptography (do not deviate without discussion)

Envelope encryption hierarchy:

1. **Master key (root KEK)** — 256-bit, exists only in server memory after unseal. Never persisted in plaintext.
2. **Project KEKs** — one per project, wrapped by the master key, stored wrapped in Postgres.
3. **DEKs** — one per secret version, AES-256-GCM, wrapped by the project KEK. Nonces random, never reused; store nonce alongside ciphertext.

Rules:

- AES-256-GCM for all symmetric encryption. Ed25519 for signing (transit). Argon2id for user password hashing. HMAC-SHA256 for token hashing (store hashes, never raw tokens).
- **Unseal:** `Unsealer` interface with two implementations from day one: Shamir (3-of-5 default, configurable) and cloud-KMS auto-unseal (AWS KMS first). Server starts sealed; all secret operations return 503 until unsealed.
- Key rotation: rotating a project KEK re-wraps DEKs lazily; rotating the master key re-wraps all project KEKs (online operation).
- Zero plaintext secrets in logs, error messages, or audit entries — audit records key names/paths, never values. Enforce with tests.
- Constant-time comparison for all token/MAC checks.

## AuthN / AuthZ

- **Auth methods:** (1) user email + password (Argon2id) with session cookies for the UI; (2) service tokens (long-lived, scoped, shown once at creation); (3) OIDC — generic OIDC provider config, tested against GitHub and Google; (4) OIDC-federated machine identity for CI (GitHub Actions JWT exchange).
- **RBAC:** roles (viewer / developer / admin / owner) scoped to project or environment. Service tokens are scoped to a single config or environment with read or read/write. Deny by default.
- Token format: `janus_<type>_<random>`; store HMAC only.

## Audit log

Append-only `audit_events` table. Every authenticated request that touches a secret, key, token, or policy writes an event: actor, action, resource path, result, IP, timestamp. Each event includes the SHA-256 hash of the previous event (hash chain) for tamper evidence. Audit write failure fails the request (no unaudited mutations). Never log secret values.

- Revealing a secret value in the web UI is a read and MUST emit an audit event (masked list views read metadata only and do not).
- `GET /v1/audit/verify` walks the hash chain and reports integrity status + event count; the UI surfaces this as a "chain verified" badge.
- `GET /v1/audit/export` streams events as JSONL or CSV, filterable by time range, actor, action type, and result (including denied-only).

## API conventions

- REST, JSON, `/v1/` prefix. `Authorization: Bearer <token>`.
- Errors: `{"error": {"code": "...", "message": "..."}}` with correct HTTP status; never leak internals.
- List endpoints: cursor pagination. Mutations: idempotency via client-supplied `Idempotency-Key` where destructive.
- Rate limiting on auth endpoints. Strict CORS (UI is same-origin embedded, so effectively none).

## CLI (`janus`)

Core secrets commands (same `janus` binary as the server): `janus login`, `janus setup` (bind directory to project/config), `janus secrets get/set/list/delete`, `janus run -- <cmd>` (inject secrets as env vars into subprocess — flagship feature), `janus secrets download --format env|json|yaml`. Config in `~/.config/janus/`. Never write plaintext secrets to disk unless the user explicitly passes `--plain` to a download command.

## Build phases (work in order; do not start a later phase early)

**Phase 1 — Core (usable Doppler replacement):**
crypto layer + unseal → store + migrations → projects/envs/configs/secrets CRUD with versioning → auth (passwords, service tokens) → RBAC → audit log → REST API → CLI with `run`. Ends with: docker-compose up, create project, set secrets, `janus run` works.

**Phase 2 — Transit + UI:**
transit engine (named keys, encrypt/decrypt/sign/verify/rewrap, key versioning, min_decryption_version) reusing internal/crypto; React SPA (project overview dashboard, secret editor with masked values / batched dirty-state saves / config version diff, audit viewer with chain-verify badge and export, token/member management); OIDC login; **usage metrics** — lightweight daily aggregates (reads per config, per token) derived from audit events for the dashboard ("Reads 24h"), no external metrics stack.

**Phase 3 — Rotation + dynamic:**
static rotation framework (scheduled, webhook-notified; Postgres password + generic webhook rotators first); sync integrations (GitHub Actions secrets, Kubernetes Secrets); dynamic Postgres credentials with lease manager (TTL, renewal, revocation on expiry; revoke-on-startup sweep for leases orphaned by a crash).

## Non-goals (do NOT build these; reject scope creep)

- HA / Raft clustering (single node + Postgres + backups)
- PKI / certificate authority (store certs as KV; pair with step-ca if needed)
- SSH signing engine, HSM / PKCS#11 support
- Multi-tenancy / organizations
- FIPS certification claims
- Dynamic secrets backends beyond Postgres (until explicitly requested)

## Testing & security rules

- Table-driven unit tests; `internal/crypto` requires 100% branch coverage including nonce-reuse and tamper (modified ciphertext) cases.
- Integration tests against real Postgres via testcontainers.
- A dedicated test asserts no secret value ever appears in log output or error strings (grep-based leak test over captured logs).
- Run `govulncheck` and `gosec` in CI; treat findings as build failures.
- All inputs validated at the API boundary. SQL only via parameterized queries.
- When in doubt on a security decision, stop and ask rather than guessing.

## Development commands

```
make dev          # run server with hot reload (air) + vite dev server
make test         # go test ./... + web tests
make migrate      # apply migrations to local db
make build        # build web, embed, build binaries
docker compose up # full local stack
```
