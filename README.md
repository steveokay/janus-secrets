# Janus

A single-tenant, self-hosted secrets manager, deployed as one Go binary plus
PostgreSQL. It combines ideas from Doppler (project/environment/config model,
`run` injection), Vault (transit encryption, dynamic secrets, hash-chained
audit), and AWS KMS (encrypt-as-a-service with key versioning).

> **Status: early development.** Phase 1 is in progress. The cryptographic
> core — envelope encryption and unseal — is complete and fully tested. The
> storage, API, CLI, and UI are not built yet. See [Roadmap](#roadmap) for the
> honest current state. This is not yet usable as a secrets manager.

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

### Data model (planned)

Doppler-style hierarchy: **Project → Environment → Config → Secrets**, with
two-level versioning (immutable config versions for diff/rollback, plus
per-secret value history). Not yet implemented.

## Tech stack

- **Server / CLI:** Go (stdlib-first, minimal dependencies).
- **Crypto:** Go stdlib `crypto/*` and `golang.org/x/crypto` only, plus AWS KMS
  (used as a service, not a crypto library) and a vendored copy of HashiCorp
  Vault's Shamir implementation (MPL-2.0). No third-party crypto primitives.
- **Storage:** PostgreSQL 16+ via `pgx`, migrations with `golang-migrate` *(planned)*.
- **HTTP:** `net/http` with `chi`, REST + JSON under `/v1/` *(planned)*.
- **Web UI:** React + TypeScript + Vite, embedded in the binary via `go:embed`
  *(planned)*.
- **Deployment:** multi-stage Dockerfile + docker-compose (app + Postgres).

## Repository layout

```
cmd/janus/        server entrypoint
cmd/kh/              CLI entrypoint (planned)
internal/crypto/     envelope encryption, key hierarchy, unseal   ← implemented
internal/crypto/shamir/  vendored HashiCorp Shamir (MPL-2.0)
internal/store/      Postgres repositories, migrations (planned)
internal/api/        HTTP handlers, middleware, routes (planned)
internal/auth/       tokens, OIDC, sessions (planned)
internal/authz/      RBAC engine (planned)
internal/audit/      hash-chained audit log (planned)
migrations/          SQL migrations (planned)
web/                 React SPA (planned)
docs/                design specs and implementation plans
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
docker compose up              # start Postgres (nothing connects to it yet)
```

The `internal/crypto` package is held to **100% statement coverage**, enforced
in CI, and includes tamper, nonce-reuse, and secret-leak tests. CI also runs
`go vet`, `govulncheck`, and `gosec`.

## Security notes

- AES-256-GCM for all symmetric encryption; random nonces, never reused.
- Constant-time comparison for key-check and (later) token/MAC checks.
- Zero plaintext secrets in logs or error messages — enforced by a leak test.
- The file-based seal-config store is for bootstrap and tests; it is atomic but
  not yet crash-durable. A Postgres-backed store lands with the storage
  milestone.

## Roadmap

**Phase 1 — Core (usable Doppler replacement):**
crypto + unseal ✅ → store + migrations → projects/envs/configs/secrets CRUD
with versioning → auth (passwords, service tokens) → RBAC → audit log → REST
API → CLI with `run`.

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
