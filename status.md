# Janus — build status

Phase 1 (Core) is being built strictly in order. Each task counts as done only
after implementation + spec review + quality review.

## Milestone 1 — Scaffold + Crypto Layer ✅ complete (merged)

Plan: `docs/superpowers/plans/2026-07-02-scaffold-crypto-layer.md`
Docs: `docs/crypto.md` · Merged via PRs #1, #2, #3.

- [x] 1. Repo scaffold (go.mod, Makefile, compose, CI)
- [x] 2. AEAD primitives + error sentinels
- [x] 3. Key generation + wrap/unwrap with AAD
- [x] 4. Keyring (sealed/unsealed state machine)
- [x] 5. Vendor HashiCorp shamir
- [x] 6. Unsealer contract + KCV + seal-config store
- [x] 7. Shamir unsealer
- [x] 8. KMS unsealer + AWS adapter
- [x] 9. Leak test + 100% coverage gate
- [x] Final review + merge decision

## Milestone 2 — Store Layer (foundation + core CRUD) ✅ merged (PR #4)

Spec: `docs/superpowers/specs/2026-07-03-store-layer-design.md`
Docs: `docs/data-model.md` · Plan: `docs/superpowers/plans/2026-07-03-store-layer.md`
Branch: `milestone-2-store` (built via subagent-driven development; every task
spec- and quality-reviewed).

Scope delivered: crypto-blind `internal/store` over `pgxpool`; embedded
`golang-migrate` runner; core schema (project → env → config → secret) with
two-level versioning + soft-delete; typed repositories; Postgres-backed
`SealConfigStore`; `janus migrate` CLI + `make migrate`.
Deferred to later specs: config inheritance, secret references, encryption
orchestration, key rotation.

- [x] Design spec (brainstorming) + user review
- [x] Implementation plan (writing-plans) — 12 tasks
- [x] 1. Connection pool + testcontainers harness
- [x] 2. Migrations + embedded golang-migrate runner
- [x] 3. Errors, models, pgx error mapping
- [x] 4. Postgres SealConfigStore
- [x] 5. ProjectRepo (CRUD + soft-delete/undelete/destroy)
- [x] 6. EnvironmentRepo
- [x] 7. ConfigRepo (inherits_from column, unresolved)
- [x] 8. SecretRepo — batched atomic save + versioned reads
- [x] 9. SecretRepo — history, diff, rollback
- [x] 10. Concurrency test (contiguous versions under FOR UPDATE)
- [x] 11. `janus migrate` subcommand + `make migrate`
- [x] 12. CI/security gate green, full-suite verification
- [x] Final review (holistic, clean bill) + merged to main via PR #4

Verification: `go build`, `go vet`, `go test ./...` (crypto + store via
testcontainers), `gosec` (0 issues), `govulncheck` (0) all pass. Toolchain
pinned to `go1.26.4` (`toolchain` directive) to clear two stdlib `crypto/x509`
advisories flagged by govulncheck; CI stays on `go-version: stable` above that
floor.

## Later Phase-1 milestones (not started)

**Not yet usable end-to-end.** Only the foundation exists: `docker compose up`
+ `make migrate` applies the schema, and the store persists/reads versioned
(pre-encrypted) data. There is no way yet to create a project, set a plaintext
secret, or run `kh` — that arrives with the CRUD/encryption service, then the
API and CLI. Phase-1 finish line (per CLAUDE.md): "docker-compose up, create
project, set secrets, `kh run` works."

- [ ] CRUD service + encryption orchestration (config inheritance, references)
- [ ] Auth (passwords, service tokens, OIDC)
- [ ] RBAC engine
- [ ] Hash-chained audit log
- [ ] REST API (`/v1/`)
- [ ] CLI with `kh run`
