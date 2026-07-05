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
`POST /v1/sys/seal` (auth milestone checklist), secret-facing routes, secrets CLI.

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
stored), and scoped `janus_svc_` service tokens (mint-once, HMAC-verify, list,
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
(tokens store scope now, enforced by the RBAC/API milestones); `janus login` CLI.

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

## Milestone 6 — RBAC (roles, scopes, enforcement) ✅ complete

Spec: `docs/superpowers/specs/2026-07-04-rbac-design.md`
Plan: `docs/superpowers/plans/2026-07-04-rbac.md`
Branch: `milestone-6-rbac` (subagent-driven development; each task compiler- and
diff-reviewed by the controller before proceeding).

Scope delivered: `internal/authz` — a pure, HTTP-free, deny-by-default decision
engine over a fixed role→action matrix (viewer ⊂ developer ⊂ admin ⊂ owner,
built cumulatively) and `role_bindings` in Postgres (migration `000003`).
`Engine.Can(ctx, principal, scope, action, resource)` resolves users by the
most-permissive union of their applicable bindings with top-down scope
inheritance (instance → project → environment), and service tokens by their
scope+`access` mapped to least-privilege secret/config capabilities only —
tokens can never reach management or instance actions. Enforcement is explicit
at the handler boundary via a thin `s.can(...)` helper plus a `requireInstance`
middleware; the engine stays identity/HTTP-free and `internal/secrets` stays
authz-free. Grants honor a delegation constraint (cannot grant above your own
effective role at that scope) and a never-lock-out guard (cannot remove or
disable the last instance owner); Boot reconciles an instance owner on startup
(self-heals an M5→M6 upgrade). Closes the two M5 deferrals: `POST /v1/sys/seal`
now requires `sys:seal`, and token mint/list/revoke are authorized (list is
scope-filtered to what the caller can `token:read`) — the highest-impact M5
follow-up. New endpoints: `/v1/users` (create/list/disable, `user:manage`) and
`/v1/{instance|projects/{pid}|projects/{pid}/environments/{eid}}/members`
(grant/revoke/list). Denied responses expose no policy internals.

- [x] Design spec (brainstorming) + user review
- [x] Implementation plan (writing-plans) — 9 tasks
- [x] 1. Migration `000003` + `RoleBinding` models + `RoleBindingRepo`
      (NULL-safe upsert, list-for-user/scope, revoke, owner count)
- [x] 2. User store additions + `auth.Service` user mgmt; `CreateInitialAdmin`
      returns the new user id
- [x] 3. `internal/authz` — action vocabulary, role→action matrix, `Resource`,
      `ErrForbidden`
- [x] 4. `authz.Engine` — `Can` (user bindings + token capabilities),
      grant/revoke/list, `EffectiveRole` (100% coverage)
- [x] 5. API plumbing — token scope in context, engine wired into `New`/`Boot`,
      bootstrap owner-grant + startup reconciliation (no enforcement yet)
- [x] 6. Enforce `sys:seal`; authorize token mint/list/revoke (scope-filtered)
- [x] 7. `/v1/users` endpoints (create/list/disable) gated on `user:manage`
- [x] 8. Membership endpoints (instance/project/env) with delegation constraint
      + last-owner guard
- [x] 9. Authz leak test, full gate, tracker

Verification: `go build`, `go vet`, `go test ./...` (authz + api + auth + store
+ secrets + crypto + CLI, Docker-backed suites ran) all pass; `internal/authz`
coverage 100.0%; `gosec` (v2.27.1, shamir excluded) 0 issues (no new `#nosec`
needed — the RBAC SQL column-list constants contain no credential-like tokens);
`govulncheck` 0 affecting vulnerabilities. Task ordering kept the build green at
every commit: the owner-grant (task 5) lands before enforcement (task 6), so the
M5 auth/token e2e never regressed mid-sequence.

## Milestone 7 — Hash-chained audit log ✅ complete

Spec: `docs/superpowers/specs/2026-07-05-audit-log-design.md`
Plan: `docs/superpowers/plans/2026-07-05-audit-log.md`
Branch: `milestone-7-audit` (subagent-driven development; each task compiler- and
diff-reviewed by the controller before proceeding).

Scope delivered: `internal/audit` — a pure, HTTP-free engine over a crypto-blind
`store.AuditRepo`. An append-only `audit_events` table (migration `000004`)
whose every append serializes on a Postgres transaction advisory lock, so under
concurrency the chain stays contiguous with no gaps or dupes. Each event carries
the SHA-256 hash of the previous event; the hash is canonical (domain-tagged,
length-prefixed fields, presence byte so NULL and "" never collide, big-endian
seq + nanosecond timestamp — `occurred_at` is µs-truncated before both hashing
and storage so a value read back from Postgres re-hashes identically). `Event`
has **no value field by construction**, so a secret value can never be recorded;
the log records actor / action / resource path / result / IP, never a value.
`Recorder.Record` computes seq/prev_hash/hash from the chain head inside the
store's serialized `Append`; `Verify` walks the chain and reports the first break
(`hash_mismatch` or `chain_break`). Recording is **synchronous and fail-closed**:
a record whose own write fails 500s the request, so no audited mutation is ever
left unrecorded. Services stay audit-blind — only the API layer records, via a
thin `s.record(...)` per handler after each successful mutation, with denials
captured centrally in `s.authorize(...)` / `requireInstance` (every 403 becomes a
`denied` row). Retrofit covers token mint/revoke, user create/disable, member
grant/revoke, `sys.seal`, and auth login (success + anonymous failure) / logout /
password-change. New endpoints: `GET /v1/audit/verify` (chain integrity +
count, `audit:read`, not self-audited) and `GET /v1/audit/export` (JSONL default
or CSV, `from`/`to`/`actor`/`action`/`result` filters, hashes hex-encoded for
offline verification; self-audited **before** streaming so a mid-download abort
is still recorded). `Boot` wires a real `audit.New(store.NewAuditRepo(st))`;
unit-test servers pass `nil` (record no-ops), so the pre-existing e2e/leak suites
stayed byte-for-byte green through the retrofit.

- [x] Design spec (brainstorming) + user review
- [x] Implementation plan (writing-plans) — 7 tasks
- [x] 1. Migration `000004` + `AuditRepo` (advisory-lock append, iterate,
      filtered list; serialization proven under 20-way concurrency)
- [x] 2. `internal/audit` — `Event`, canonical SHA-256 chain hash,
      `Recorder.Record`, `Verify` (tamper + chain-break + genesis; 100% coverage)
- [x] 3. API plumbing — recorder wired into `New`/`Boot`; `record`/`recordActor`/
      `authorize` helpers; central denial capture (nil-recorder no-op seam)
- [x] 4. Retrofit token/user/member/seal handlers (success + centralized denial)
- [x] 5. Retrofit auth handlers (login success+failure, logout, password change)
- [x] 6. `GET /v1/audit/verify` + `GET /v1/audit/export` (jsonl/csv, filters,
      self-audit)
- [x] 7. Real-recorder e2e (login → mint → grant → verify → export → seal),
      leak coverage, full gate, tracker

Verification: `go build`, `go vet`, `go test ./... -count=1` (audit + api + auth
+ authz + store + secrets + crypto + CLI, Docker-backed suites ran) all pass;
`internal/audit` coverage 100.0%; `gosec` (v2.27.1, shamir excluded) 0 issues
(the two `uint32(len)`/`uint64(int64)` conversions in `internal/audit/hash.go`
carry recorded `#nosec G115` justifications — length is bounded, the int64→uint64
is an intentional bit reinterpretation); `govulncheck` 0 affecting
vulnerabilities. A real-recorder e2e (`TestAuditE2E`) proves the chain verifies,
the expected success actions export, masked reads (token list / me) are absent, a
`denied` row is present, and `sys.seal` is recorded; a leak test
(`TestNoSecretValueInAuditRowsOrExport`) pushes a known password + mint-once raw
token through audited handlers and asserts neither ever reaches any
`audit_events` column nor the export body.

Documented caveat (crash window): recording runs after the mutation commits, in
its own advisory-locked transaction. A crash in the window between the
mutation-commit and the audit insert leaves that one action unaudited (the
mutation stands). This is the accepted single-node trade-off — the alternative
(one transaction spanning service mutation + audit) would couple every service
to the audit layer and break the crypto-blind/audit-blind boundaries. Verify
still passes over whatever was recorded; the chain is never left inconsistent.

## Milestone 8 — Secret-facing REST API ✅ complete

Spec: `docs/superpowers/specs/2026-07-05-rest-api-design.md`
Plan: `docs/superpowers/plans/2026-07-05-rest-api.md`
Branch: `milestone-8-rest-api` (subagent-driven development; each task compiler-
and diff-reviewed by the controller before proceeding).

Scope delivered: `internal/api` now exposes the full project → environment →
config → secret hierarchy and its two-level versioning over `/v1/`, RBAC-enforced
and audited, so Janus is usable as a secrets manager without the CLI. Thin
handlers reuse the existing layers — `internal/secrets.Service` for crypto ops
(project create, secret set/reveal/rollback, diff) and the crypto-blind store
repos for hierarchy reads/deletes — over the M6/M7 seam (`s.authorize`/`s.can`/
`s.record`/`resolveScopeResource`); no new package. Route surface: project
CRUD + soft-delete/restore/hard-destroy; environment and config CRUD +
lifecycle; secret masked-list (metadata only, no audit) vs. reveal
(one / all / historical value, each audited `secret.reveal`); batch + per-key
secret write and delete (each a new immutable config version, all-or-nothing via
`SetSecrets`); key value-version history; config version list / diff / rollback.
`writeServiceError` maps every `internal/secrets`/`internal/store` sentinel to
the HTTP envelope (sealed → 503, not-found → 404, conflict → 409, validation →
400, integrity/unexpected → generic 500) with no internals, key material, or
values leaked. Migration `000005` makes hard-destroy of a project/environment
cascade the whole subtree, while `configs.inherits_from` deliberately stays
`NO ACTION` so a branch config blocks destruction of its inheritance base
(→ 409). Reveal, write, and delete emit audit events; masked list and history
read metadata only and do not. All routes sit behind `RequireAuth` +
`RequireUnsealed` (401 unauthenticated, 503 while sealed) and deny-by-default
RBAC.
Deferred (per spec non-goals): config inheritance resolution, secret references
(`${projects...}`), cursor pagination, `Idempotency-Key`, binary value encoding,
and download formats — none implemented this milestone.

- [x] Design spec (brainstorming) + user review
- [x] Implementation plan (writing-plans) — 9 tasks
- [x] 1. Migration `000005` — `ON DELETE CASCADE` for ownership FKs
      (`inherits_from` stays `NO ACTION`; store cascade + inheritance-block tests)
- [x] 2. `writeServiceError` — map secrets/store sentinels to the HTTP envelope
- [x] 3. Project CRUD + lifecycle routes (create/list/get/soft-delete/restore/
      destroy) + e2e
- [x] 4. Environment CRUD + lifecycle routes + e2e
- [x] 5. Config CRUD + lifecycle routes (+ `ConfigRepo.GetIncludingDeleted` for
      restore auth) + e2e
- [x] 6. Secret masked list + reveal (one/all/historical) + key history + e2e
- [x] 7. Secret batch write + per-key put + delete (each → new config version) + e2e
- [x] 8. Config version list + diff + rollback + e2e
- [x] 9. RBAC enforcement matrix e2e + secret-value leak coverage + gates + docs

Verification: `go build`, `go vet`, `go test ./... -count=1` (api + store +
secrets + auth + authz + audit + crypto + CLI, Docker-backed testcontainers
suites ran) all pass; `gosec` (v2.27.1, shamir excluded) 0 issues (no new
`#nosec` needed — the new code is parameterized SQL via repos and stdlib HTTP);
`govulncheck` 0 affecting vulnerabilities. An RBAC matrix e2e
(`TestSecretsRBACMatrix`) proves a viewer can masked-list but is denied
secret-write (403) and project-create (403), a developer can write but is denied
project-destroy (403), and the instance owner can destroy. A leak test
(`TestNoSecretValueInLogsOrErrorResponse`) writes a known sentinel value through
the HTTP write route, reveals it, and asserts the sentinel never appears in the
captured request-logger output nor in an error response body (a `?version=99999`
→ 404).

## Milestone 9 — Secrets CLI (`janus login`/`setup`/`secrets`/`run`) ✅ complete

Spec: `docs/superpowers/specs/2026-07-05-cli-design.md`
Plan: `docs/superpowers/plans/2026-07-05-cli.md`
Docs: `docs/cli.md` · Branch: `milestone-9-cli` (subagent-driven development;
each task TDD'd — failing test first — and diff-reviewed before proceeding).

Scope delivered: the operator/developer secrets CLI on the existing `janus`
binary, consuming the M8 `/v1/` REST API — the Phase-1 finish line. New cobra
subcommands over small, focused, unit-tested files: a credential/config store
(`~/.config/janus/auth.json`, `0600`), an authenticated `apiClient` (mirroring
the unauthenticated `sysCall`) that attaches credentials, decodes the error
envelope, and rewrites auth/seal failures into actionable messages, a
`.janus.yaml` directory-binding resolver, and pure helpers for the env overlay
and `env`/`json`/`yaml` formatting. Commands: `login`/`logout` (email+password
→ stored session; best-effort server logout), `setup` (validate slugs → write
`.janus.yaml`), `secrets list` (masked, unaudited) / `get` (raw value to
stdout, audited) / `set` + `delete` (batched into one config version) /
`download` (env/json/yaml with the `--plain` disk guard), and the flagship
`run` (one audited bulk reveal → env overlay → exec with signal + exit-code
forwarding).

**Two-tier credential model:** `--token`/`JANUS_TOKEN` service tokens (bearer)
for CI, stored session cookie for interactive humans; precedence
`--token > JANUS_TOKEN > session`. Address precedence
`--address > JANUS_ADDR > auth.json > http://127.0.0.1:8200`. Per-field binding
precedence `flags > JANUS_PROJECT/ENV/CONFIG > .janus.yaml` (cwd only). Projects
and environments match by slug, configs by name. `JANUS_CONFIG_DIR` overrides
the whole config-dir path (relocation + portable test isolation, since
`os.UserConfigDir` ignores `XDG_CONFIG_HOME` on Windows). stdout is data only;
all diagnostics/prompts go to stderr. `--output` on `download` requires
`--plain` and writes `0600`; stdout streaming is unguarded.

Deferred (documented non-goals in `docs/cli.md` §11): OIDC / browser login and
CI JWT exchange (password + `JANUS_TOKEN` only), OS keychain storage (the
`0600` file is the store), parent-directory walk for `.janus.yaml` (cwd only),
a global path-map directory binding (dropped for the committed `.janus.yaml` +
flags/env as the single source of truth), and shell completions.

- [x] Design spec (brainstorming) + user review
- [x] Implementation plan (writing-plans) — 13 tasks
- [x] 1. Credential + config store (auth.json, address/credential precedence,
      `JANUS_CONFIG_DIR`)
- [x] 2. Authenticated API client (envelope decode + auth-error rewrite)
- [x] 3. Slug/name → config-id resolution (per-level errors)
- [x] 4. `.janus.yaml` binding + flag/env/file precedence (`yaml.v3` promoted
      to a direct dep)
- [x] 5. `login` / `logout` (session storage, echo-off prompt)
- [x] 6. `setup` (validate + write `.janus.yaml`)
- [x] 7. `secrets` parent + `list` (masked) + `get` (raw value to stdout)
- [x] 8. `secrets set` + `delete` (batched into one config version)
- [x] 9. `env`/`json`/`yaml` formatters with POSIX shell escaping
- [x] 10. `secrets download` + `--plain` disk guard (0600)
- [x] 11. `run` — env overlay (`--preserve-env`), signal + exit-code forwarding
- [x] 12. E2E round-trip + `run` injection + secret-leak assertions (real
      server via testcontainers)
- [x] 13. Docs (`docs/cli.md`, README quickstart, operations flows), tracker,
      full gate sweep

Verification: `go build`, `go vet`, `go test ./... -count=1` (cmd/janus incl.
the Docker-backed CLI e2e + leak tests, plus api + store + secrets + auth +
authz + audit + crypto) all pass; `gosec` (shamir excluded) 0 issues;
`govulncheck` 0 affecting vulnerabilities. One new `#nosec` was recorded:
`G124` on `cmd/janus/apiclient.go` — the `janus_session` value is set on an
**outgoing client request** cookie, where `Secure`/`HttpOnly`/`SameSite` have no
meaning (those attributes apply only to server `Set-Cookie` responses and are
ignored on a request), so the finding is a false positive. The e2e
(`TestCLIRoundTrip` / `TestCLIRunInjectsExit`) drives `setup → set → get →
list → download` against a real unsealed server and confirms `run` injects a
secret into a real child and propagates its exit code; the leak test
(`TestCLINoSecretLeakInDiagnostics`) asserts a known value reaches only stdout
on `get`, never stderr or an error string on `list`/error paths.

Final adversarial review: **SHIP after one fix.** No secret-leak-to-stderr/error
and no `--plain` bypass were found. One **High** (confirmed, fixed): `download
--output` used `os.WriteFile`, which applies its mode only on file *creation*, so
writing over a pre-existing `0644` file silently persisted revealed secrets at the
looser mode. Fixed with an atomic temp + `O_EXCL 0600` + rename (guarantees `0600`
regardless of a pre-existing file, refuses to follow a planted symlink, no partial
write), plus a regression test asserting `0600` over a pre-existing `0644` target
(skipped on Windows where POSIX modes are cosmetic). Non-blocking Low follow-ups
carried forward: POSIX file modes are cosmetic on the Windows host (`auth.json` /
download perms fall back to the parent ACL there); `run` forwards *all* signals
(could restrict to Interrupt/SIGTERM); `buildChildEnv` is case-sensitive while
Windows env vars are not; `resolveConfigID` assumes un-paginated list endpoints
(true today — revisit if cursor pagination lands per CLAUDE.md).

**Phase 1 (Core) is complete.** The CLAUDE.md finish line —
"docker-compose up, create project, set secrets, `janus run` works" — is met.
Config inheritance resolution and secret references (`${projects...}`) remain
the one open Phase-1 line item and roll forward as a follow-up.

## Phase-1 milestones — remaining

**Phase 1 is complete: a runnable server with identities + RBAC + the
secret-facing REST API + the secrets CLI.** `make dev-up` (or
`docker compose up` + `scripts/dev-unseal.sh`)
yields a running, unsealed server; `janus init`/`unseal`/`seal-status` work over
HTTP; non-sys routes return 503 while sealed. Auth and RBAC now exist:
`/v1/auth/*`, `/v1/tokens`, `/v1/users`, and the `.../members` endpoints are live
and enforced deny-by-default, and `POST /v1/sys/seal` requires `sys:seal`. The
hash-chained audit log is now live too: sensitive handlers record fail-closed
events and `GET /v1/audit/verify` + `/export` are served. The secret-facing REST
API is now live as well — project/env/config CRUD + lifecycle, secret
masked-list/reveal/write/delete, and config version list/diff/rollback are served
over `/v1/`, RBAC-enforced and audited (milestone 8). The secrets CLI is now
live too (milestone 9): `janus login`/`setup`/`secrets`/`run` authenticate, bind
a directory via `.janus.yaml`, read/write secrets, and inject a config's secrets
as env vars into a subprocess. Phase-1 finish line (per CLAUDE.md) —
"docker-compose up, create project, set secrets, `janus run` works" — is **met**.
The one open Phase-1 line item, config inheritance/reference resolution, rolls
forward as a follow-up.

Caveat carried forward: the operator `janus seal` CLI command does not yet send
a credential, so it will receive 401 against the now-gated endpoint until it
grows a token flag (or `janus login`); sealing over HTTP works with a bearer token
or session cookie today.

- [ ] Config inheritance resolution + secret references (`${projects...}`)
- [x] Auth (passwords, service tokens) — `POST /v1/sys/seal` auth-gated
      (OIDC / federation deferred to Phase 2)
- [x] RBAC engine — roles/scopes, deny-by-default enforcement, membership +
      user endpoints, token/seal authorization (milestone 6)
- [x] Hash-chained audit log — append-only `audit_events`, SHA-256 chain,
      `/v1/audit/verify` + `/export`, fail-closed per-handler recording (milestone 7)
- [x] REST API (`/v1/`) — project/env/config CRUD + lifecycle, secret masked-list/
      reveal/write/delete, versions/diff/rollback, cascade destroy (milestone 8)
- [x] Secrets CLI with `janus run` — login/setup/secrets/run, `.janus.yaml`
      binding, two-tier credentials, `JANUS_CONFIG_DIR` override (milestone 9)

## Phase-2 items already on the radar

- [ ] **Federation**: OIDC login for humans (generic provider; GitHub + Google
      tested) and OIDC-federated machine identity for CI (GitHub Actions JWT
      exchange → scoped short-lived credential). Deliberately deferred from the
      auth milestone; the Phase-1 identity model must leave room for
      non-password principals and federated token types so this lands without
      rework.
- [ ] Transit/KMS engine, React SPA, usage metrics (per CLAUDE.md Phase 2)
