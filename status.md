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

## Milestone 3 — Secrets Service (encryption orchestration + core CRUD) ✅ complete

Spec: `docs/superpowers/specs/2026-07-03-secrets-service-design.md`
Plan: `docs/superpowers/plans/2026-07-03-secrets-service.md`
Branch: `milestone-3-secrets` (subagent-driven development; every task spec- and
quality-reviewed).

Scope delivered: new `internal/secrets` service wiring `internal/crypto` to
`internal/store`. Project KEK lifecycle (generate + wrap at project create,
AAD-bound to a service-generated project id); batched envelope-encrypted writes
(`SetSecrets`) via a store `Change` encrypt-closure so each DEK's AAD binds the
store-assigned `value_version`; masked list vs. auditable reveal
(`ListSecrets`/`KeyHistory` carry no value; `GetSecret`/`RevealConfig`/
`GetSecretVersion` decrypt); crypto-free version ops (`ListVersions`,
`DiffVersions`, `Rollback` — reuses ciphertext, no re-encryption); sealed-state
handling (`ErrSealed`) and best-effort zeroization of every KEK/DEK/plaintext.
Two supporting store changes: `Store.NewID` + `ProjectRepo.Create(id)`, and
`Change.Encrypt func(valueVersion int)`.
Deferred to later specs: config inheritance resolution, secret references,
server bootstrap/unseal wiring, key rotation.

- [x] Design spec (brainstorming) + user review
- [x] Implementation plan (writing-plans) — 8 tasks
- [x] 1. store `NewID` + `ProjectRepo.Create(id)` (+ closure contract tests)
- [x] 2. store `Change` encrypt closure bound to `value_version`
- [x] 3. secrets package skeleton (Service, errors, validation, zeroize)
- [x] 4. project KEK lifecycle + env/config passthrough + test harness
- [x] 5. batched encrypted set + reveal round-trip
- [x] 6. masked reads + version ops + historical reveal
- [x] 7. security tests (tamper→ErrDecrypt, DEKAAD relocation, no-plaintext-leak,
      sealed reads, absent version, soft-deleted rejection)
- [x] 8. CI/security gate green, full-suite verification

Verification: `go build`, `go vet`, `go test ./...` (crypto + store + secrets
via testcontainers) all pass; `gosec` (v2.27.1, shamir excluded) 0 issues;
`govulncheck` 0. A `value_version→uint64` conversion is guarded (fail-closed) to
clear gosec G115.

## Later Phase-1 milestones (not started)

**Usable in-process, not yet over the wire.** The secrets service can now
create a project, encrypt/store secrets, and read them back decrypted (with full
versioning, diff, rollback) — but only via Go APIs. There is still no auth, no
HTTP surface, and no `kh` CLI. `docker compose up` + `make migrate` applies the
schema; wiring the service to a server (unseal-at-startup) and the API/CLI is
what remains. Phase-1 finish line (per CLAUDE.md): "docker-compose up, create
project, set secrets, `kh run` works."

- [ ] Server bootstrap: unseal-at-startup + `janus init`/`unseal` CLI
- [ ] Config inheritance resolution + secret references (`${projects...}`)
- [ ] Auth (passwords, service tokens, OIDC)
- [ ] RBAC engine
- [ ] Hash-chained audit log
- [ ] REST API (`/v1/`)
- [ ] CLI with `kh run`
