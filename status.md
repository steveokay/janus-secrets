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

## Milestone 2 — Store Layer (foundation + core CRUD) 🚧 planning

Spec: `docs/superpowers/specs/2026-07-03-store-layer-design.md`
Docs: `docs/data-model.md` · Plan: _pending (writing-plans)_.

Scope: crypto-blind `internal/store` over `pgxpool`; `golang-migrate` runner;
core schema (project → env → config → secret) with two-level versioning +
soft-delete; typed repositories; Postgres-backed `SealConfigStore`.
Deferred to later specs: config inheritance, secret references, encryption
orchestration, key rotation.

- [x] Design spec (brainstorming) — written + committed
- [ ] Spec review by user
- [ ] Implementation plan (writing-plans)
- [ ] Implementation (tasks TBD from plan)

## Later Phase-1 milestones (not started)

- [ ] CRUD service + encryption orchestration (config inheritance, references)
- [ ] Auth (passwords, service tokens, OIDC)
- [ ] RBAC engine
- [ ] Hash-chained audit log
- [ ] REST API (`/v1/`)
- [ ] CLI with `kh run`
