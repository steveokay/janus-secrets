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
The last open Phase-1 line item, config inheritance/reference resolution, is now
**done** (milestone 11) — **Phase 1 is fully complete**.

Caveat carried forward: the operator `janus seal` CLI command does not yet send
a credential, so it will receive 401 against the now-gated endpoint until it
grows a token flag (or `janus login`); sealing over HTTP works with a bearer token
or session cookie today.

- [x] Config inheritance resolution + secret references (`${projects...}`)
      (milestone 11)
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

## Milestone 11 — Config inheritance + secret references ✅ complete

Spec: `docs/superpowers/specs/2026-07-05-inheritance-references-design.md`
Plan: `docs/superpowers/plans/2026-07-05-inheritance-references.md` (12 tasks)
Docs: `docs/references.md` · Branch: `milestone-11-resolution` (subagent-driven
development — fresh implementer per task, diff-reviewed by the controller before
proceeding). **This is the final Phase-1 line item; Phase 1 is now complete.**

Scope delivered: two read-time composition features over the project → env →
config → secret hierarchy, in a new pure `internal/resolve` package that composes
over two ports — `RawReader` (config coordinate → raw decrypted values,
implemented by `internal/secrets`) and `Authorizer` (per-target `secret:read`
check, implemented by `internal/api`). **Inheritance:** a config's `inherits_from`
chain is walked to its root and merged **child-wins** per key (same environment
only), with cycle detection (`409 ErrInheritanceCycle`) and missing/deleted-base
detection (`409 ErrBrokenInheritance`); a branch config may have no own secrets
and still read its base's. **References:** secret values embed
`${projects.<project>.<env>.<config>.KEY}` (absolute) or `${KEY}` (local, same
merged set), resolved **transitively** at read time — inheritance applied first,
then references expanded — with `$$` escaping a literal `$`. A value that is
*exactly* one reference passes the target's bytes through unchanged (binary-safe);
an embedded reference splices bytes into the string. Two cycle guards
(`(config,key)` frame revisit → `409 ErrReferenceCycle`) plus a depth cap of 32
(`422 ErrReferenceDepth`); unknown target → `422 ErrUnresolvedReference`; malformed
token → `400 ErrBadReferenceSyntax`. Resolution is **atomic** — any unresolvable
reference fails the whole read and returns no values. Reveals **resolve by
default**; `?raw=true` (CLI `--raw` on `get`/`run`/`download`) returns stored
values verbatim. The masked list carries an `origin` per key
(`own`/`inherited`/`overridden`).

**Authorization asymmetry (by design):** references are **strict** —
every dereferenced config independently requires the caller to hold `secret:read`
(a forbidden reference fails closed `403 ErrForbiddenReference`, atomic; no
transitive privilege escalation); inheritance is **transparent** — reading a
branch needs no separate grant on its base (admin-controlled, same-environment
structural content). **Audit:** a resolved reveal writes one primary
`secret.reveal` on the config read plus one per **distinct** config dereferenced
via a reference (`detail = "via reference from configs/<cid>"`, deduped);
inheritance ancestors are part of the primary reveal, not separately audited.
No secret value ever enters an audit row or error message.

- [x] Design spec (brainstorming) + user review
- [x] Implementation plan (writing-plans) — 12 tasks
- [x] 1. `internal/resolve` skeleton — `Coord`/`RawConfig`/`Provenance` types,
      `RawReader`/`Authorizer` ports, 7 error sentinels
- [x] 2. Reference grammar parser — `${projects.p.e.c.KEY}` + local `${KEY}` +
      `$$` escape (table-driven)
- [x] 3. Inheritance merge — child-wins chain walk, cycle + broken-base
- [x] 4. Reference expansion + `Resolve`/`ResolveKey` — transitive, `(config,key)`
      cycle frames, depth cap, atomic failure, zeroization
- [x] 5. `internal/secrets` `ReadRaw`/`ReadRawByID` port — reuse decrypt path +
      coordinate lookups (version-less configs read as empty own-set)
- [x] 6. `internal/api` `Authorizer` adapter + `writeServiceError` sentinel mapping
      (403/409/422/400)
- [x] 7. Reveal handlers — resolve-by-default + `?raw=true` + single-key + per-deref audit
- [x] 8. Masked-list origin markers (own/inherited/overridden, metadata-only merge)
- [x] 9. Reference-RBAC + inheritance e2e
- [x] 10. Audit-trail + secret-value leak e2e
- [x] 11. CLI `--raw` on `run`/`download`/`get` + list `ORIGIN` column
- [x] 12. Docs (`docs/references.md`), full gate sweep

Verification: `go build`, `go vet`, `go test ./... -count=1` (resolve + secrets +
api + authz + audit + store + crypto + CLI, Docker-backed testcontainers suites
ran) all pass; `internal/crypto`, `internal/authz`, and `internal/audit` coverage
100.0%; `gosec` (shamir excluded) 0 issues (15 recorded `#nosec`, none new for
resolve — the pure package is stdlib-only and allocation-based); `govulncheck` 0
affecting. An e2e proves resolved-vs-raw reveal, transitive references, an
inheritance chain (own/inherited/overridden origins), a forbidden reference
denied `403` atomically, and `janus run` injecting resolved values; a leak test
proves no secret value reaches the logs or any audit row and that each
dereferenced target is audited by path.

Two defects were found and fixed during the T9 review, not carried forward: a
**version-less inheriting config** (one that sets `inherits_from` but has never
been written to) was unreadable because `GetLatest` returns `ErrNotFound` for a
config with no config version, and both `rawFor` (the `RawReader` port) and
`ListSecretsMerged` (the masked-list merge) called it unconditionally — fixed to
treat a version-less config as an empty own-set that still follows
`inherits_from`. The residual of the same gap, **`LatestVersion`** (used only for
the resolved reveal's response `version` field), also 404'd on a version-less
config — fixed to report version `0`. A regression test
(`versionless_test.go`) locks the fix.

The final adversarial review (whole-milestone diff) surfaced three more, all
fixed before merge: **(1) CRITICAL — cross-environment/cross-project inheritance
authz bypass.** Inheritance is transparent to authorization, which is only safe
because the base is in the *same* environment — but that precondition was never
enforced. `CreateConfig` passed `inherits_from` straight to the store (whose FK
only requires the base to exist *somewhere* in the instance) and `resolveMerged`
walked the chain by raw config-id across any boundary, so a caller with create
rights in one environment could inherit from a config in another environment (or
project) and read its secrets through the branch. Fixed: `CreateConfig` rejects
an `inherits_from` whose base is absent, soft-deleted, or in a different
environment (`ErrValidation` → 400); regression test
(`inherit_scope_test.go`) covers cross-env, cross-project, missing base, and the
same-env positive control. **(2) MEDIUM — un-zeroized decrypted plaintext per
reference dereference:** `resolveRef`'s `ReadRaw` decrypts the target's own
values but uses only its coordinates; the values were never zeroized (and the
config was decrypted twice) — added `defer zeroizeMap(target.Values)`. **(3) LOW
— splice buffer not zeroized** when an embedded reference fails partway through a
string splice — zeroize `buf` on the error path. **(4) LOW — a denied reference
dereference wrote no audit event:** a reveal refused by a forbidden reference
failed closed (403, atomic) but left no trail; now both reveal handlers record a
fail-closed `denied` `secret.reveal` on the config read (mirroring the central
`authorize()` denial pattern; e2e-asserted). The review otherwise cleared the
parser edge cases, both cycle guards, the depth cap, return-value aliasing,
error→HTTP mapping, strict per-target authz, provenance dedup, and the leak
surface.

## Phase 2 · Sub-project A — Transit Engine ✅ complete

Spec: `docs/superpowers/specs/2026-07-05-transit-engine-design.md`
Plan: `docs/superpowers/plans/2026-07-05-transit-engine.md` (13 tasks)
Docs: `docs/transit.md` · Branch: `milestone-10-transit` (subagent-driven
development — fresh implementer per task, diff-reviewed by the controller before
proceeding).

Phase 2 spans four largely independent subsystems, each getting its own
spec → plan → implementation cycle: **A. Transit engine** (this one — no UI
dependency), **B. React SPA**, **C. OIDC login**, **D. usage metrics**. CLAUDE.md's
phase order puts the transit engine first.

Scope delivered: a Vault-style "encryption as a service" engine — `internal/transit`,
a pure HTTP-free/identity-free engine (mirroring `internal/secrets`) holding
instance-scoped named keys and performing encrypt/decrypt/sign/verify/rewrap and
datakey generation on data it never stores, reusing `internal/crypto` (AEAD +
wrap/unwrap + the unsealed master key) and a crypto-blind `store.TransitRepo`. Two
key types: `aes256-gcm` (encrypt/decrypt/rewrap/datakey) and `ed25519` (sign/verify,
via new stdlib `crypto/ed25519` helpers — the private seed is wrapped, the public
key stored in clear). Key versioning with `latest_version` /
`min_decryption_version` / `deletion_allowed`; rotate, trim, rewrap-forward, and
datakey generation (plaintext + wrapped). Ciphertext/signature envelope
`janus:v<N>:<base64>`; each version's material master-key-wrapped under a new
`TransitKeyAAD(name, version)` so a copied version row fails to unwrap. Routes under
`/v1/transit/*` behind `RequireAuth` + `RequireUnsealed` + RBAC. New instance-scoped
actions `transit:read` / `transit:use` / `transit:manage` (viewer reads metadata,
developer uses, admin/owner manages) plus a new **`transit` service-token scope**
(`use`/`manage`, optionally restricted to one key **by name**) so apps can call
transit without reaching secrets or other instance actions. Management ops
(create/rotate/trim/config/delete) are audited fail-closed (recording the key name,
never material) and all denials captured centrally; high-frequency data-plane ops
(encrypt/decrypt/sign/verify/rewrap/datakey) are **not** individually audited — usage
visibility is deferred to sub-project D — matching how M7 treats masked reads.

- [x] Design spec (brainstorming) + user review
- [x] Implementation plan (writing-plans) — 13 tasks
- [x] 1. Ed25519 crypto helpers (`GenerateEd25519Key`/`Sign`/`SignWithSeed`/`Verify`)
      + `TransitKeyAAD` (100% crypto coverage kept, incl. rand-failure + AAD injectivity)
- [x] 2. Migration `000006` — transit tables + `service_tokens` transit-scope extension
- [x] 3. `store.TransitRepo` (create/append/config/trim/delete/get-by-name/get-by-id/list)
- [x] 4. Transit engine skeleton — sentinels, `janus:vN:` envelope parse/format
- [x] 5. Create key + aes encrypt/decrypt with versioned wrapping (+ AAD, tamper, sealed)
- [x] 6. Rotate, config (`min_decryption_version`), rewrap, trim
- [x] 7. Ed25519 sign/verify + datakey (plaintext/wrapped)
- [x] 8. AuthZ — transit actions, `Resource.TransitKey`, transit token capabilities
- [x] 9. Auth — transit token scope in mint/verify (nullable `scope_id` for all-keys)
- [x] 10. API — `writeServiceError` mapping + transit key management routes + Boot wiring
- [x] 11. API — transit data-plane routes (encrypt/decrypt/sign/verify/rewrap/datakey)
- [x] 12. API — mint transit-scoped service tokens end to end
- [x] 13. RBAC matrix + audit + leak e2e, docs (`docs/transit.md`), full gate sweep

Verification: `go build`, `go vet`, `go test ./... -count=1` (transit + api +
auth + authz + audit + store + secrets + crypto + CLI, Docker-backed
testcontainers suites ran) all pass; `internal/crypto`, `internal/authz`, and
`internal/audit` coverage 100.0%; `gosec` (shamir excluded) 0 issues (15 recorded
`#nosec`, no new one needed for transit — the `TransitKeyAAD` `uint64(version)`
G115 reuses the bounded/positive pattern); `govulncheck` 0 affecting. An e2e
lifecycle test drives create → encrypt → rotate → rewrap → decrypt; an RBAC
matrix proves viewer-read-only / developer-use / admin-manage, a key-restricted
transit token allowed its own key and denied another, and a config-scoped token
denied all transit routes; a leak test proves no key material / datakey plaintext
/ sentinel reaches the logs or any audit row, that management ops are audited by
key name, and that data-plane ops emit no audit rows.

Two defects were found and fixed during the RBAC/leak review, not carried
forward: **(1)** a key-restricted transit token was keyed inconsistently — mint
validated/stored the key by UUID (`GetByID`) while enforcement compares
`scope.ID` against the `/{name}` route's key name, so a restricted token matched
no key (denied even its own); compounded by `service_tokens.scope_id` being a
`uuid` column, so storing a key name 500'd on `invalid input syntax for type
uuid`. Fixed by validating/storing the restriction by name (`GetByName`) and
widening `scope_id` to `text` (config/env scopes keep their UUID, now as text).
The all-keys transit token (NULL `scope_id`) was already correct.

## Phase 2 · Sub-project B — React SPA (milestone 1: core editor) ✅ complete

Spec: `docs/superpowers/specs/2026-07-06-spa-core-editor-design.md`
Plan: `docs/superpowers/plans/2026-07-06-spa-core-editor.md` (15 tasks)
Docs: `docs/web.md` · Branch: `milestone-12-spa-core` (subagent-driven
development — fresh implementer per task, diff-reviewed by the controller).

The first slice of the web UI: a Vite **React + TS + Tailwind** SPA embedded in
the `janus` binary via `go:embed` and served **same-origin** by the chi server.
A new `internal/web` package holds `//go:embed dist` + `Handler()` (static assets
+ SPA fallback for deep links + a restrictive CSP); `Server.MountUI` mounts it as
the router `NotFound` fallback after `/v1`, and `RequireUnsealed` was narrowed to
gate only `/v1/*` API paths so the SPA loads while sealed (to present the unseal
screen). **No new `/v1` endpoints** — the SPA consumes the existing
auth/sys/projects/environments/configs/secrets routes.

Delivered (project-centric nav, per the approved design): bootstrap guards
(seal-status → uninitialized notice / unseal screen / login / app); **in-UI
Shamir unseal** (per-share progress + reset; KMS auto-unseal), with shares held
only in ephemeral state; password/session **login** + **change-password**;
project switcher + environment/config tree with **lightweight create** forms
(config inherits only from a same-environment base — server-enforced); and the
flagship **secret editor** — masked list with `own`/`inherited`/`overridden`
origin badges (not audited), per-key **audited reveal**, editing of the config's
**own raw values** (inherited rows not editable in place), a pure client-side
**dirty buffer** (add/edit/delete) with a live pending summary, and batched
**Save as vN** (one `PUT …/secrets` = one config version) with an unsaved-changes
guard. Revealed plaintext and Shamir shares never enter the Query cache or
storage. Deferred areas (audit viewer, version diff/rollback, token/member
management, dashboard, transit UI) render "Coming soon" placeholders; their specs
are later B-slices. Tooling: React Router v6 + TanStack Query + a thin typed
`fetch` client; Vitest + React Testing Library + MSW; multi-stage Dockerfile
(web build → embed → go build) + Makefile `web-build`/`web-test`/`dev` wiring.

- [x] Design spec (brainstorming) + user review
- [x] Implementation plan (writing-plans) — 15 tasks
- [x] 1. `internal/web` embed + SPA-fallback handler + CSP
- [x] 2. Serve SPA — narrow seal gate to `/v1/*` + `MountUI` + boot wiring
- [x] 3. Scaffold Vite + React-TS + Tailwind + Vitest
- [x] 4. Typed `fetch` client + `ApiError` + MSW harness
- [x] 5. Typed endpoint fns + query client (global 401/503 routing)
- [x] 6. Auth context + login + change-password
- [x] 7. Unseal screen (shamir shares/progress/reset; kms auto)
- [x] 8. App shell + router + bootstrap guards
- [x] 9. Project switcher + env/config tree sidebar
- [x] 10. Lightweight create forms (project/env/config)
- [x] 11. Pure dirty-buffer logic
- [x] 12. Secret editor — masked list + origin badges + audited reveal
- [x] 13. Secret editor — edit/add/delete + batched Save as vN + unsaved guard
- [x] 14. Coming-soon placeholders for deferred areas + empty states
- [x] 15. Dockerfile web stage + Makefile wiring + docs + gate sweep

Verification: `go build`/`go vet`/`go test ./... -count=1` (incl. `internal/web`
and `internal/api` UI-mount/seal-gate tests, Docker-backed) all pass; the web
suite (`npm run test`) and `npm run typecheck` are green; `make build` produces
an embedded binary; `gosec` (shamir excluded) 0 issues; `govulncheck` 0
affecting; crypto/authz/audit coverage unchanged at 100% (no changes there).

Two real defects were caught in the per-task review, not carried forward: **(1)**
the plan's `Sidebar` read the active project via `useParams()`, but the sidebar
renders as a *sibling* of `<Routes>` (never inside a matched `<Route>`), so
`useParams()` returns nothing — it would have failed in production, not just
tests; fixed to derive the id from the URL via `useLocation()` + `matchPath`.
**(2)** the shared test render helper rendered components bare, so `SecretEditor`
(which legitimately uses `useParams()` inside a matched route in the real app)
saw no `configId` under test; fixed by wrapping the rendered element in a
matching `<Route>` in the harness (component left correct).

Final adversarial review (whole-milestone diff): **no critical security
defects** — the seal gate, path-traversal handling, CSP, reveal-into-cache, and
unseal-share handling all verified clean. Six functional/hygiene findings were
fixed before merge (commit `8ab5082`): **(1)** a newly added key lived only in
the dirty buffer and rendered no row — now shown as a visible, editable,
removable row; **(2)** `logout()` did not clear the Query cache — now clears it
so no revealed plaintext survives sign-out; **(3)** a 401 from any query
client-navigated to `/login`, leaving stale cached data — now clears the cache
and re-bootstraps auth; **(4)** the save button's version label read a
value-version — now reads the real config version via a new `endpoints.rawConfig`
(`?reveal=true&raw=true`); **(5)** `refresh()` did not swallow a `/me` 503 — now
treats any `/me` failure (401 or 503) as "not authenticated"; **(6)** `embed.go`
could emit a directory listing — now serves only real files, with directory
paths falling through to the SPA shell. A test-harness bug surfaced while fixing
#4: **MSW v2 matches on path only and ignores the query string**, so the masked
(`…/secrets`) and raw (`…/secrets?reveal=true&raw=true`) handlers collided on one
path; fixed by consolidating each into a single handler that branches on
`new URL(request.url).searchParams` the way the real server does. Merged to main
via **PR #21** (merge commit `716d7d9`).

## Phase 2 · Sub-project C1 — OIDC login (humans) ✅ complete

Spec: `docs/superpowers/specs/2026-07-07-oidc-login-design.md`
Plan: `docs/superpowers/plans/2026-07-07-oidc-login.md` (15 tasks)
Docs: `docs/oidc.md` · Branch: `worktree-oidc-federation` (Go-only, developed in
an isolated git worktree parallel to the UI agent; subagent-driven development —
fresh implementer per task, diff-reviewed by the controller).

Human sign-in to the web UI through an external OpenID Connect provider —
**Authorization Code + PKCE (S256) + state + nonce**. Single provider
(single-tenant), generic OIDC, tested against an in-package mock IdP and designed
for GitHub/Google. An OIDC user is an ordinary `KindUser` principal that receives
the same `janus_session` cookie a password login issues; RBAC is unchanged.

Delivered: the login flow behind `RequireUnsealed` (`GET /v1/auth/oidc/status`
→ `{"enabled":bool}`; `GET …/login` → 302 to the IdP setting single-use
state/nonce/PKCE; `GET …/callback` → verify state, PKCE-exchange, verify ID token
issuer/audience/JWKS-signature/nonce, require `email_verified`, resolve a
**pre-provisioned** user by verified email, link by stable `(issuer, subject)`,
issue session, 302 to `/`). Admin provider config (`GET/PUT/DELETE /v1/sys/oidc`)
gated by a new **`oidc:manage`** instance action (admin/owner), audited on write
(`oidc.config.write`/`oidc.config.delete`, recording issuer + client_id only).
The **client secret** is AES-256-GCM wrapped under the master key (AAD
`janus:auth:oidc-client-secret`, arbitrary-length `Encrypt`, not `WrapKey`),
write-only, surfaced as `secret_set` and never logged/returned/audited. Storage:
migration `000007_oidc` (`oidc_providers`, `oidc_identities` with
`UNIQUE(issuer,subject)`, `oidc_auth_requests` single-use/expiring state, swept
at boot). No auto-provisioning; every callback failure returns one
indistinguishable error (no account enumeration).

Third-party crypto-lib exception (approved 2026-07-07, recorded in `CLAUDE.md`):
JWT/JWKS verification uses `github.com/coreos/go-oidc/v3`, `golang.org/x/oauth2`,
and (transitively) `github.com/go-jose/go-jose/v4` rather than hand-rolled JOSE;
the envelope/transit/unseal crypto remains stdlib + `x/crypto` only.

- [x] Design spec (brainstorming) + user review
- [x] Implementation plan (writing-plans) — 15 tasks
- [x] 1. Deps (go-oidc/v3, x/oauth2, go-jose/v4 test-only) — oauth2 pinned to
      keep golang-migrate from downgrading
- [x] 2. Crypto — `OIDCClientSecretAAD` + keyring `Wrap/UnwrapOIDCClientSecret`
- [x] 3–6. Migration `000007` + models + three store repos (provider / identity /
      single-use auth-request)
- [x] 7. `oidc:manage` action (admin/owner union) + RBAC matrix
- [x] 8. Extract `createSession` as the single session-mint path
- [x] 9. Service — provider config (`Set/Get/DeleteOIDCProvider`, verifier cache)
- [x] 10. `StartOIDCLogin` (state/nonce/PKCE) + callback verification vs mock IdP
- [x] 11. Pre-provisioned resolution (by-`(iss,sub)`, else verified-email match)
- [x] 12. API — `/v1/auth/oidc/status|login|callback` (+ success audit via Principal)
- [x] 13. API — `/v1/sys/oidc` config (GET/PUT/DELETE, `oidc:manage`, audited)
- [x] 14. Client-secret leak test + expired-login-state boot sweep
- [x] 15. Docs (`docs/oidc.md`), `CLAUDE.md` crypto-lib carve-out, this tracker

Verification: `go build`/`go vet`/`go test ./... -count=1` (auth + api + authz +
audit + store + crypto + CLI, Docker-backed testcontainers) all pass;
`internal/crypto` coverage 100.0%; `gosec` (shamir excluded) 0 issues;
`govulncheck` 0 affecting. `TestOIDCClientSecretNeverLeaks` drives a full
configure + login against the mock IdP with a canary secret and asserts it
reaches no log line, no response body, and no `audit_events` row. During the
gate sweep `govulncheck` flagged **GO-2026-4945** (a JWE decryption panic in
`go-jose/v4`) as reachable through the ID-token verify path; fixed by bumping
`go-jose/v4` v4.0.5 → **v4.1.4**.

Final adversarial review (whole-branch, 11-point threat model): ten items clean;
**one blocking finding fixed before merge** — **login CSRF / session fixation**:
the single-use server-side `state` was not bound to the initiating browser, so a
captured callback URL could fixate a victim into the attacker's account (RFC 9700
§4.7). Fixed by binding `state` to an `HttpOnly`, `SameSite=Lax` `janus_oidc_state`
cookie set at `/login` and required (constant-time equal to the query `state`)
before the callback consumes the row; a regression test drives the victim-without-
cookie path and asserts 400 + no session. Also fixed a non-blocking status nit
(`crypto.ErrSealed` from `PUT /v1/sys/oidc` while sealed now maps to 503, not 500)
and added the state-binding cookie assertions. Re-verified: full suite green,
gosec 0 (17 `#nosec`), govulncheck 0 affecting.

**Follow-up:** sub-project **C2** — OIDC-federated CI machine identity — is now
**complete** (see the C2 section below).

## Phase 2 · Sub-project C2 — OIDC CI federation (machines) ✅ complete

Spec: `docs/superpowers/specs/2026-07-08-oidc-ci-federation-design.md`
Plan: `docs/superpowers/plans/2026-07-08-oidc-ci-federation.md` (14 tasks)
Docs: `docs/ci-federation.md` · Branch: `worktree-oidc-ci-federation` (Go-only,
isolated worktree parallel to the UI agent; subagent-driven development — fresh
implementer per task, diff-reviewed by the controller).

CI jobs exchange a GitHub Actions OIDC token for a **short-lived, scoped
`janus_svc_` service token** — no stored long-lived CI secret. The exchange
reuses C1's go-oidc/JWKS verification path (pointed at the federation issuer with
a required audience) and the existing service-token issuance. Completes the
CLAUDE.md "Federation" Phase-2 item (human C1 + machine C2).

Delivered: the exchange endpoint `POST /v1/auth/oidc/federate` (public, behind
`RequireUnsealed`, rate-limited) — verify (JWKS sig, iss, exp, **aud exact**) →
match a **single** enabled trust binding on structured claim conditions → mint a
token with the binding's scope/access and TTL. Admin config gated by the existing
`oidc:manage` action: `GET/PUT/DELETE /v1/sys/oidc/federation` (issuer/audience/
enabled) and `GET/POST/DELETE /v1/sys/oidc/federation/bindings[/{id}]`, audited.
Storage: migration `000009_oidc_federation` (`oidc_federation_config`,
`oidc_federation_bindings`), plus `service_tokens.created_by` made nullable + a
`federation_binding` FK column (federated tokens have no human minter) via a new
`MintFederatedToken` path.

Safety rules (enforced + tested): every binding **must** include a `repository`
claim; **exactly one** binding must match (0 or >1 → denied, ambiguous denied as
such); audience required + exact-matched; TTL per-binding **capped at 1h**
(default 15m). Every exchange failure returns one indistinguishable
`federation_denied` (401); the raw JWT is never logged/audited (a leak test
drives a full exchange and asserts the JWT + minted token appear in no log line
or `audit_events` row). Federation uses the same crypto-lib exception recorded in
`CLAUDE.md` for C1 (go-oidc / x-oauth2 / go-jose); the config holds no secret
(JWKS trust, nothing to wrap).

- [x] 1. Migration `000009` + models
- [x] 2. Federation store repos (config + bindings)
- [x] 3. `service_tokens` nullable minter + `CreateFederated`
- [x] 4. `MintFederatedToken`
- [x] 5. Service wiring (federation repos + verifier cache)
- [x] 6. Federation config + binding CRUD with validation
- [x] 7. Claim matcher (exactly-one, deny-ambiguous)
- [x] 8. `federationVerifierFor` + `FederateCILogin` exchange
- [x] 9. API — `POST /v1/auth/oidc/federate`
- [x] 10. API — `/v1/sys/oidc/federation` config (oidc:manage, audited)
- [x] 11. API — `/v1/sys/oidc/federation/bindings` CRUD (oidc:manage, audited)
- [x] 12. Client-JWT leak test
- [x] 13. Full gate sweep
- [x] 14. Docs (`docs/ci-federation.md`), cross-links, this tracker

Verification: `go build`/`go vet`/`go test ./... -count=1` (auth + api + store +
crypto + CLI, Docker-backed testcontainers) all pass; `internal/crypto` coverage
100.0%; `gosec` (shamir excluded) 0 issues (17 `#nosec`); `govulncheck` 0
affecting. Exchange tests cover happy path, wrong audience, expired token,
no-match, ambiguous match, TTL-cap + `repository`-required config validation,
RBAC on all admin routes, and the JWT leak test.

## Phase 2 · Sub-project D — Usage metrics ("Reads 24h") ✅ complete

Spec: `docs/superpowers/specs/2026-07-07-usage-metrics-design.md`
Plan: `docs/superpowers/plans/2026-07-07-usage-metrics.md` (8 tasks)
Branch: `usage-metrics` · Merged via **PR #35** (main `2584da2`).

Lightweight usage metrics derived **on-demand** from `audit_events` — no external
metrics stack, no rollup table, no background job. A new `store.MetricsRepo.Reads24h(
ctx, projectID *string)` counts successful `secret.reveal` events in the trailing 24h
(`projectID == nil` → instance-wide; non-nil → that project's configs), grouped into a
total + top-5 configs (config id parsed from the audit `resource` path, joined for
names) + top-5 service tokens (grouped by `actor_id` where `actor_kind='service_token'`).
Two routes, identical JSON shape, differing only in authz scope:
`GET /v1/metrics/reads-24h` (instance `AuditRead`) and
`GET /v1/projects/{pid}/metrics/reads-24h` (project-scoped `AuditRead`); neither
self-audits (a metadata read, like `/v1/audit/events`), denials still recorded.
Migration `000008_metrics_index` adds a composite `audit_events(action, occurred_at)`
index. Frontend (**B6 dashboard**): `web/src/metrics/ReadsStrip.tsx` — an instance-wide
strip on the Projects list + a project-scoped row on the board, composed from the kit,
**names + counts only** (never a secret value or raw token), and it **hides itself on
error/403** so a viewer without audit permission never sees a broken page.

- [x] 1. Frontend endpoints + `useReads24h`/`useProjectReads24h` hooks
- [x] 2. `ReadsStrip` component (data / loading / empty / error-hides)
- [x] 3. Mount instance strip on Projects list
- [x] 4. Mount project-scoped row on the board
- [x] 5. Migration `000008` — `(action, occurred_at)` index
- [x] 6. `MetricsRepo.Reads24h` — on-demand aggregation (testcontainer tests)
- [x] 7. Instance endpoint `/v1/metrics/reads-24h`
- [x] 8. Project endpoint + isolation e2e

Verification: `go build`, `go test ./internal/store/... ./internal/api/...`
(testcontainers) pass; web `typecheck` + full `vitest` (194→197 through later
slices) + `build` + dual-theme `smoke` green. Final security review: APPROVE, no
Critical/Important — SQL parameterized, project scoping enforced twice (authz +
`e.project_id=$1`), and the `errorMessage` ordering (below) keeps guardrail messages
intact. UI-first order let the dashboard build against msw mocks mirroring the fixed
Go wire shape before the backend landed.

## Phase 2 · Sub-project B (cont.) — FE punch-list §3–§7 ✅ complete

The `fe-improvements.md` polish punch-list, each its own brainstorm → spec → plan →
subagent-driven slice on top of the R1–R4 dark redesign:

- [x] **§3 — Secret editor redesign** (**PR #33**, `85bc8f3`): rebuilt to mockup §06 —
      grid table with origin pills, per-row reveal/copy on hover + rails/change-chips,
      per-row actions (inherited→edit=override), bottom dirty-bar (Review diff / Discard /
      Save as vN), key filter, Import .env modal, value-free Review-diff. New `rowState.ts`
      + split components; security review clean (plaintext ephemeral on the reveal path).
- [x] **§3-P2 editor polish** (**PR #44**, `07fd4ba`): new `ui/Modal.tsx` (Radix Dialog —
      focus-trap/`Esc`/`aria-modal`/sr-only Title) with `ReviewDiffDialog`/`ImportEnvDialog`
      refactored onto it; **reveal-all/hide-all** = one audited **bulk** reveal into
      ephemeral state + **auto-re-mask** on window blur + 60s idle; **⌘/Ctrl+S** saves when
      dirty. 203 web tests; final security review APPROVE.
      ⚠️ **Correction:** the §3 (PR #33) "no reveal on mount" claim was **inaccurate** —
      the editor's mount-time `raw` query (`?reveal=true&raw=true`) revealed **all** stored
      plaintext into the TanStack Query cache and emitted one audited `secret.reveal` per
      open. PR #44's final review caught it.
- [x] **On-demand editor reveal — security fix** (**PR #46**, `a662790`): the editor no
      longer reveals on mount. Mount loads only masked metadata + the config version
      (`listVersions`, value-free); values load **on demand** into two ephemeral maps
      (`revealed` = viewing, auto-re-masks on blur/60s-idle; `original` = edit-originals,
      persists while dirty), both RAW; per-key (`revealKeyRaw`) / bulk (`rawConfig`)
      reveals are explicit + audited. `remove()` seeds a value-free existence marker so
      `dirty.ts` registers deletes. Removed orphaned `revealAll`/`revealKey`. 207 web
      tests (mount-reveals-nothing / per-key / edit-fetches-original / reveal-all-once +
      pending-edit-survives-blur); adversarial security review APPROVE (5 invariants,
      file:line). Frontend-only. Spec/plan: `…/2026-07-08-editor-ondemand*`.
- [x] **§4 component kit + §5 feedback** (**PR #37**, `7b566be`): `Button`/`Input`/
      `Textarea`/`Select`/`Card`/`Skeleton`/`Tooltip` (Radix) primitives; `errorMessage`
      envelope→friendly mapping (**403/409 curated-first** — load-bearing: the backend
      returns last-owner guardrails as 409-coded-`validation`, so friendly-first would
      clobber them); success/error toasts on all mutations (never a secret in a title);
      skeleton loaders; shell icon-button tooltips; kit adopted in the forms. `Tabs`
      dropped (YAGNI); optimistic UI + in-app unsaved-guard deferred.
- [x] **§6 auth/unseal branded** (**PR #40**, `3de3486`): `AuthCard` shell; login +
      unseal re-skinned onto the kit; share-progress **segments** (green-filled per
      mockup — not a "ring"); unseal share cleared before the network await (preserved).
      First-login OTP-prompt deferred (needs a backend first-login signal).
- [x] **§7 a11y & responsive** (**PR #41**, `3ed6549`): secret-table horizontal-scroll
      containment (`overflow-x-auto` + `min-w`) + editor `Esc`-to-cancel. Focus rings
      (global `:focus-visible`), contrast (dark-AA guard), Radix menu/dialog keyboarding,
      and the `min-w-0` shell overflow-guard were already in place.

Verification (whole punch-list): web `typecheck` + full `vitest` (197 passing) +
`build` + dual-theme `smoke` green throughout; each slice got a security-focused final
review (all APPROVE, no Critical/Important). §3-P2 (reveal-all/⌘S/dialog-a11y) shipped in
PR #44; the **on-demand editor reveal** security fix shipped in PR #46. Remaining
`fe-improvements.md` items are all **P2** polish: editor row-nav, §5 optimistic UI +
in-app unsaved-guard [needs a data-router migration], §0 motion, §1 collapsible sidebar,
§2 onboarding.

## Ops hardening (PR 1) — probes, seal CLI auth, session idle timeout ✅ complete

Spec: `docs/superpowers/specs/2026-07-09-ops-hardening-design.md` (§1–§3 of a
four-item batch; §4 **backup/restore lands separately as PR 2**)
Plan: `docs/superpowers/plans/2026-07-09-ops-hardening.md`
Branch: `ops-probes-seal-idle` · Docs: `docs/operations.md`

Three small operational-trust fixes closed before Phase 3 makes the system more
dynamic:

- [x] 1. **Readiness/liveness probes** (`internal/api/sys_probes.go`):
      `GET /v1/sys/live` → always `200 {"status":"live"}` (process liveness,
      touches nothing); `GET /v1/sys/ready` → `200 {"status":"ready"}` iff
      **DB reachable AND initialized AND unsealed**, else `503` with a distinct
      envelope code checked in order — `db_unavailable` / `uninitialized` /
      `sealed`. Unauthenticated, un-audited (probes fire every few seconds and
      touch no secrets); `/v1/sys/health` unchanged for backward compat.
      `docker-compose.yml` healthcheck moved to `/v1/sys/ready` — a sealed
      instance now reports **unhealthy** until an operator unseals (intended:
      `docker compose up --wait` blocks on unseal).
- [x] 2. **Authenticated `janus seal`** (`cmd/janus/sys_commands.go`): the seal
      command now goes through the authenticated API client — `--token` /
      `JANUS_TOKEN` overrides the stored `janus login` session; no credential →
      actionable error without calling the server. Closes the caveat carried
      since M5/M6: bare `sysCall` sent no credential, so `janus seal` was a
      guaranteed 401 against every production server (`sys:seal`-gated).
      `init`/`unseal`/`seal-status` stay unauthenticated by design.
- [x] 3. **Session idle timeout** (`internal/auth/sessions.go`): new
      **`JANUS_SESSION_IDLE_TIMEOUT`** (Go duration, default **30m**, `0`
      disables; invalid value fails boot) enforced server-side on every session
      validation — `now − last_seen_at > idle` deletes the session row and
      returns **401 `session_expired`**. The 24h absolute TTL stays the hard
      cap; service tokens are untouched; null `last_seen_at` falls back to
      `created_at`. No new frontend code — the SPA's global 401 handler already
      drops an idle-expired session to the login screen.

Verification: `go build`, `go vet`, `go test ./...` (api + auth + store +
secrets + transit + resolve + audit + authz + crypto + web + CLI, Docker-backed
testcontainers suites ran fresh) all pass; `gosec` (shamir excluded) 0 issues
(17 recorded `#nosec`, none new); `govulncheck` 0 affecting — the toolchain pin
moved `go1.26.4` → `go1.26.5` during the gate sweep to clear stdlib
GO-2026-5856 (`crypto/tls` ECH), same pattern as the M2 pin. Known cosmetic
oddity deferred: the CLI's generic 503 rewrite makes `janus seal` against an
already-sealed server print "server is sealed — unseal it first".

## Phase-2 items already on the radar

- [x] **Federation** — **complete** (C1 human OIDC login, PR #34; C2 CI machine
      federation, PR #39). Generic OIDC (GitHub/Google-ready) human sign-in +
      GitHub-Actions-JWT → scoped short-lived `janus_svc_` token exchange. See the
      Sub-project C1 + C2 sections above.
- [x] Transit/KMS engine — **complete** (see the Sub-project A section above;
      `internal/transit`, `/v1/transit/*`, transit token scope, `docs/transit.md`).
- [x] React SPA — **complete**: core editor + feature slices B2–B5, the full dark
      redesign (R1–R4), the **B6 metrics dashboard** (sub-project D), and the entire
      `fe-improvements.md` P0/P1 punch-list (§3 editor redesign, §4 kit, §5 feedback,
      §6 auth/unseal, §7 a11y/responsive). Only **P2** polish remains. (See the
      Sub-project B / B(cont.) / D sections; `web/` + `internal/web`, `docs/web.md`.)
- [x] Usage metrics — **complete** (sub-project D, PR #35; see the Sub-project D
      section above). On-demand `secret.reveal` aggregation → `/v1/metrics/reads-24h`
      + the B6 dashboard strips.

### Next up: remaining SPA feature slices (before C/D)

Decision (2026-07-06): build out the remaining **SPA feature slices** — the
"Coming soon" placeholder screens — next, ahead of sub-projects C (OIDC) and D
(usage-metrics backend). Each is its own brainstorm → spec → plan →
subagent-driven slice consuming existing `/v1` APIs:

- [x] **B2** — config version history: list, diff, rollback (Sheet drawer,
      key-name-only diffs, confirm-gated audited rollback) — **PR #24**
- [x] **B3** — audit viewer: event list + filters, chain-verify badge, export;
      added backend `GET /v1/audit/events` keyset pagination — **PR #25**
- [x] **B4** — token + member management (scoped mint w/ show-once value, role
      management with server guardrails, users w/ one-time password) — **PR #26**
- [x] **B5** — transit UI: `/v1/transit/*` key console (create/rotate/configure/
      trim/delete) + plaintext-free crypto playground (encrypt/rewrap/sign/verify;
      no decrypt/datakey) — **PR #31**
- [x] **B6** — usage-metrics dashboard (sub-project D): instance "Reads 24h" strip +
      project-scoped board row from `/v1/metrics/reads-24h` — **PR #35**

**FE visual polish — DONE (2026-07-07).** The Doppler-inspired **dark redesign**
(dark-first + light toggle, minus SaaS chrome) shipped as slices **R1–R4**
(**PRs #27–#30**): dual-theme CSS-var tokens (`web/src/theme.css` + `ThemeProvider`),
app shell + ⌘K command palette, projects list + env-columns board + create-project
modal, and a screen polish pass (dark-AA, board fixes, palette a11y). New canonical
authority: `docs/design/ui-redesign-mockup.html` +
`docs/superpowers/specs/2026-07-07-dark-redesign-design.md`. The front-end punch-list
that followed (secret-editor redesign §3, kit primitives §4, feedback §5, auth/unseal
branded §6, a11y/responsive §7, + §3-P2 polish PR #44, + the on-demand editor reveal
security fix PR #46) is now **complete** — see the "FE punch-list §3–§7" section above.
Only discretionary **P2** polish items remain in [`fe-improvements.md`](fe-improvements.md).

**Phase 2 is essentially complete** — transit (A), the React SPA incl. all feature
slices + dark redesign + FE punch-list incl. §3-P2 and the on-demand editor reveal
security fix (B), OIDC human + CI federation (C1/C2), and usage metrics (D) are all
merged. Remaining work: discretionary **P2** UI polish (`fe-improvements.md`) and
Phase 3 (rotation + dynamic secrets, not started).
