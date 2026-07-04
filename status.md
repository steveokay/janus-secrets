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

## Milestone 4 — Server Bootstrap (unseal-at-startup + sys API + CLI) ✅ complete

Spec: `docs/superpowers/specs/2026-07-03-server-bootstrap-design.md`
Plan: `docs/superpowers/plans/2026-07-03-server-bootstrap.md`
Branch: `milestone-4-bootstrap` (subagent-driven development; every task spec-
and quality-reviewed, incl. an empirical race probe of the unseal handlers).

Scope delivered: `internal/api` (chi router, `/v1/sys/*` seal lifecycle,
`RequireUnsealed` 503 middleware, body-free request logger, project-wide
`{"error":{code,message}}` envelope, `Boot` composition with auto-migrate and
KMS boot auto-unseal); `cmd/janus` rebuilt onto cobra (`server`, `init`,
`unseal` with echo-off stdin prompt, `seal-status`, `seal`, `migrate`,
`version`); Shamir + AWS KMS seal backends; two small crypto additions
(`SubmittedShares()`, deterministic 1-of-1 seal for dev); Dockerfile + compose
app service + `scripts/dev-unseal.sh` + `make dev-up`.
Deferred (documented in spec): TLS, sys rate limiting, auth-gating
`POST /v1/sys/seal` (auth milestone checklist), secret-facing routes, `kh`.

- [x] Design spec (brainstorming) + user review
- [x] Implementation plan (writing-plans) — 10 tasks
- [x] 1. deps (chi/cobra/x-term/aws-config) + JSON error envelope
- [x] 2. crypto `SubmittedShares()` + 1-of-1 seal (100% coverage kept)
- [x] 3. `RequireUnsealed` middleware + body-free request logger
- [x] 4. Server, router, health/seal-status, graceful shutdown
- [x] 5. init/unseal/reset/seal handlers (+ race-leak fix, precise error
      taxonomy, concurrency regression test)
- [x] 6. `Boot` (auto-migrate, seal-type resolution, KMS boot auto-unseal)
- [x] 7. API leak test (no share material in logs/error responses)
- [x] 8. cobra CLI (+ argv-exposure warning, non-envelope error fallback,
      wire assertions, stdout routing fix)
- [x] 9. Dockerfile, compose app service, 1-of-1 dev-unseal workflow —
      verified end-to-end against real Docker (init → unseal → status)
- [x] 10. CI/security gate green, full-suite verification

Verification: `go build`, `go vet`, `go test ./...` (api + store + secrets +
CLI, Docker-backed suites ran) all pass; `gosec` (v2.27.1, shamir excluded)
0 issues; `govulncheck` 0; `internal/crypto` coverage 100.0%.

## Milestone 5 — Auth (passwords, sessions, service tokens) ✅ complete

Spec: `docs/superpowers/specs/2026-07-04-auth-design.md`
Plan: `docs/superpowers/plans/2026-07-04-auth.md`
Branch: `milestone-5-auth` (subagent-driven development; every task spec- and
quality-reviewed).

Scope delivered: `internal/auth` identity layer — Argon2id PHC passwords
(needs-rehash on login, strict bounds-checked param parsing to defuse a
crafted-hash DoS), Postgres-backed opaque sessions (32-byte cookie, HMAC
stored), and scoped `kh_svc_` service tokens (mint-once, HMAC-verify, list,
revoke). A single `Principal{Kind,ID,Name}` type is the seam RBAC, audit, and
Phase-2 federation build on. The token-HMAC key is a random 256-bit key wrapped
by the master key under a fixed `janus:auth:token-hmac` AAD, materialized at the
first-unseal transition — so a DB dump is not verifiable offline and credential
verification requires an unsealed server. Two-phase bootstrap: the initial admin
is created during the init ceremony (one-time password shown once), the HMAC key
at first unseal. `internal/api` gains `/v1/auth/{login,logout,me,password}` and
`/v1/tokens` (mint/list/revoke) behind `RequireAuth`, per-IP rate limiting on
credential endpoints, and auth-gates `POST /v1/sys/seal`. `janus init` prints the
one-time admin credential (`--admin-email`).
Deferred (per spec): OIDC / federation (Phase 2); RBAC scope *enforcement*
(tokens store scope now, enforced by the RBAC/API milestones); `kh login` CLI.

- [x] Design spec (brainstorming) + user review
- [x] Implementation plan (writing-plans) — 10 tasks
- [x] 1. Migration `000002` + store auth models + `UserRepo`
- [x] 2. Store repos — sessions, service tokens, auth config
- [x] 3. Crypto `WrapAuthKey`/`UnwrapAuthKey` + `AuthKeyAAD`
- [x] 4. `internal/auth` — Principal, errors, Argon2id passwords (+ crafted-hash
      DoS fix: strict param parse, tight bounds, salt/hash length checks)
- [x] 5. Service — HMAC keying, bootstrap admin, sessions, ChangePassword, sweep
- [x] 6. Scoped service tokens (mint/verify/list/revoke)
- [x] 7. `RequireAuth` + `PrincipalFrom` + per-IP rate limiter + error codes
- [x] 8. Init-ceremony admin bootstrap, first-unseal HMAC key, Boot wiring,
      seal gating, CLI credential output
- [x] 9. Auth + token endpoints + e2e lifecycle/enumeration/rate-limit tests
- [x] 10. Credential leak test, full gate, tracker

Verification: `go build`, `go vet`, `go test ./...` (auth + api + store +
secrets + CLI, Docker-backed suites ran) all pass; `gosec` (v2.27.1, shamir
excluded) 0 issues (three findings resolved with recorded `#nosec`
justifications: G115 bounded key length, G101 SQL column list, G124 intentional
conditional-`Secure` cookie); `govulncheck` 0; `internal/crypto` coverage 100.0%.
Final holistic review: SHIP, no blocking issues.

Non-blocking follow-ups from final review (carry into RBAC / a hardening pass):
- `GET /v1/tokens` and `DELETE /v1/tokens/{id}` are authn-gated only — any
  principal (incl. a read-only service token) can list/revoke. Spec'd as "any
  principal" for M5; add an admin gate when RBAC lands (highest-impact gap).
- Per-IP login rate limiter keys on `r.RemoteAddr`; behind a TLS-terminating
  proxy that collapses to one bucket — add trusted-proxy `X-Forwarded-For`
  handling when the proxy is introduced (same caveat nullifies the conditional
  cookie `Secure` flag).
- `ChangePassword` leaves other sessions valid and has no `new != old` check.
- Login returns 404 (not 503) if the HMAC key is missing after a partial unseal.
- `janus seal` CLI sends no credential → 401 against the gated endpoint.

## Later Phase-1 milestones (not started)

**Runnable server with identities, no secret routes yet.** `make dev-up` (or
`docker compose up` + `scripts/dev-unseal.sh`) yields a running, unsealed
server; `janus init`/`unseal`/`seal-status` work over HTTP; non-sys routes
return 503 while sealed. Auth now exists: `/v1/auth/*` and `/v1/tokens*` are
live, and `POST /v1/sys/seal` is auth-gated. The secrets service is live
in-process but still not exposed over HTTP — RBAC + the secret-facing REST API
come next. Phase-1 finish line (per CLAUDE.md): "docker-compose up, create
project, set secrets, `kh run` works."

Caveat carried forward: the operator `janus seal` CLI command does not yet send
a credential, so it will receive 401 against the now-gated endpoint until it
grows a token flag (or `kh login`); sealing over HTTP works with a bearer token
or session cookie today.

- [ ] Config inheritance resolution + secret references (`${projects...}`)
- [x] Auth (passwords, service tokens) — `POST /v1/sys/seal` auth-gated
      (OIDC / federation deferred to Phase 2)
- [ ] RBAC engine
- [ ] Hash-chained audit log
- [ ] REST API (`/v1/`)
- [ ] CLI with `kh run`

## Phase-2 items already on the radar

- [ ] **Federation**: OIDC login for humans (generic provider; GitHub + Google
      tested) and OIDC-federated machine identity for CI (GitHub Actions JWT
      exchange → scoped short-lived credential). Deliberately deferred from the
      auth milestone; the Phase-1 identity model must leave room for
      non-password principals and federated token types so this lands without
      rework.
- [ ] Transit/KMS engine, React SPA, usage metrics (per CLAUDE.md Phase 2)
