# Ops Hardening Batch — Design

**Date:** 2026-07-09
**Status:** Approved for planning
**Scope:** Four operational-trust items, delivered as two PRs:

| # | Item | Size | PR |
|---|------|------|----|
| 1 | Readiness/liveness probe endpoints | S | 1 |
| 2 | `janus seal` CLI authentication fix | S | 1 |
| 3 | Session inactivity timeout | S | 1 |
| 4 | Instance backup & restore | M | 2 |

**Why now:** Phases 1–2 are complete. Before Phase 3 (rotation/dynamic) makes the
system more dynamic, close the gaps that break operational trust: a secrets
manager with no DR story ("if Postgres dies, everything is unrecoverable"), a
health endpoint orchestrators can't act on, a CLI seal command that 401s against
every production server, and sessions that live 24h regardless of inactivity.

---

## 1. Readiness / liveness probes

### Problem

`GET /v1/sys/health` (internal/api/sys.go `handleHealth`) always returns 200 —
even sealed, even uninitialized (locked in by `TestHealthAlways200`). Kubernetes
and docker-compose healthchecks cannot distinguish "process up" from "can serve
secrets", so a sealed instance looks healthy and receives traffic it will 503.

### Design

Two new unauthenticated endpoints under `/v1/sys/` (already exempt from
`RequireUnsealed`, internal/api/middleware.go — no wiring changes):

- **`GET /v1/sys/live`** → always `200 {"status":"live"}`. Process liveness
  only; touches nothing (no DB, no keyring).
- **`GET /v1/sys/ready`** → `200 {"status":"ready"}` iff **initialized AND
  unsealed AND database reachable** (a cheap connectivity ping, e.g. pool ping
  with a short timeout). Otherwise `503` with the standard error envelope and a
  specific code, checked in this order:
  - `db_unavailable` — ping failed
  - `uninitialized` — no seal config
  - `sealed` — keyring sealed

### Constraints

- `/v1/sys/health` is **unchanged** (backward compatibility; existing test stays
  green). `docker-compose.yml` healthcheck moves to `/v1/sys/ready`.
- Probes are **not audited** — they fire every few seconds and would flood the
  hash chain. They touch no secrets and mutate nothing.
- Error bodies use the existing `{"error":{"code","message"}}` envelope.

### Testing

Table-driven handler tests: live always 200; ready 503+code for each failure
state (uninitialized / sealed / DB down — DB-down case via a cancelled or
closed pool in the integration suite); ready 200 on an initialized, unsealed
instance with a live DB.

---

## 2. `janus seal` CLI authentication fix

### Problem

Production wires `POST /v1/sys/seal` behind `RequireAuth` + instance-scoped
`sys:seal` (internal/api/server.go). But `newSealCmd`
(cmd/janus/sys_commands.go) calls bare `sysCall` with no credentials, so
`janus seal` returns 401 against every real server. It only works in unit
tests, where the auth service is nil.

### Design

`newSealCmd` switches to the authenticated API client (`newAPIClient`),
following the exact pattern `janus logout` already uses (cmd/janus/login.go):

- `--token` flag: service token overrides the stored session.
- Fallback: the session credential stored by `janus login`.
- No credential at all → clear error: log in with `janus login` or pass
  `--token`.
- A 401/403 from the server is surfaced with the same hint.

`init`, `unseal`, `unseal --reset`, and `seal-status` remain unauthenticated —
correct, since they must work pre-auth and while sealed.

### Testing

Extend `cmd/janus/sys_commands_test.go`: seal sends `Authorization: Bearer`
from `--token`; seal uses the stored session when no flag; no credential →
actionable error without calling the server; 403 response → hint in the error.

---

## 3. Session inactivity timeout

### Problem

UI sessions have a fixed 24h TTL (`sessionTTL`, internal/auth/sessions.go) and
`last_seen_at` already slides on every request — but nothing enforces an idle
limit. A workstation left unattended holds a live session for up to 24h.

### Design

Server-enforced idle timeout in session validation (internal/auth):

- New config **`JANUS_SESSION_IDLE_TIMEOUT`** — Go duration string, **default
  `30m`**, `0` disables. Parsed at boot (internal/api/boot.go) and passed into
  `auth.Service`; invalid values fail boot with a clear message.
- On each authenticated request, after the existing expiry check:
  `now − last_seen_at > idleTimeout` → the session is invalid. The session row
  is **deleted** and the request gets **401** with code `session_expired`
  (message: "session expired due to inactivity").
- The 24h absolute TTL is unchanged and remains the hard cap. Idle enforcement
  applies to **session cookies only** — service tokens have their own
  `expires_at` and are untouched.
- Rows predating `last_seen_at` semantics (null/zero) fall back to
  `created_at` for the idle comparison.
- No new audit event type: an idle-expired session is an authentication
  failure, handled like any other invalid session.

### Frontend

No new code. The API layer's global auth-event handler already maps any 401 to
"clear query cache, drop to login" — an idle-expired session lands on the login
screen exactly like a logged-out one.

### Testing

Unit tests in internal/auth: fresh session passes; session idle past the
threshold → invalid + row deleted; `0` disables enforcement; null
`last_seen_at` falls back to `created_at`; absolute TTL still enforced
independently. One e2e: request after idle window (short configured timeout) →
401 `session_expired`.

---

## 4. Instance backup & restore

### Threat model & guarantees (decided)

- **Key-preserving:** the backup is a logical dump of rows **exactly as stored**
  — wrapped project KEKs, wrapped DEKs, AES-GCM ciphertexts, Argon2id password
  hashes, token HMACs. It contains **zero plaintext secrets by construction**
  and is useless without the original master key. Restore therefore requires
  unsealing with the **same Shamir shares / KMS key** as the source instance.
  A leaked backup file leaks nothing beyond metadata (names, paths, actors).
- **Full instance:** everything needed for a byte-faithful replacement
  instance, including users, memberships, service-token records, OIDC config,
  transit keys, and the complete audit chain.
- **Empty-instance restore only:** restore refuses to run unless the target
  database is empty (uninitialized). No merge/upsert semantics, no
  wipe-and-replace flag.

### Backup format

A versioned **JSONL stream**:

```
{"janus_backup":1,"migration_version":9,"janus_version":"…","created_at":"…"}
{"table":"seal_config","row":{…}}
{"table":"users","row":{…}}
…
```

- **Line 1 header:** format version (`janus_backup: 1`), the applied
  golang-migrate schema version, the server version, and a timestamp.
- **Typed records** in FK-safe insertion order:
  seal_config → users → projects → wrapped project KEKs → environments →
  configs → config_versions → secrets → secret_versions → memberships →
  service_tokens → OIDC config → federation bindings → transit keys →
  transit key versions → **audit_events last** (the large table; streamed via
  cursor pagination, never buffered whole in memory).
- **Excluded tables:** sessions (ephemeral — everyone re-logs-in after
  restore), rate-limiter state, idempotency records.
- Binary columns (ciphertexts, nonces, HMACs, wrapped keys) are base64 in
  JSON. Row shapes mirror the store layer's canonical columns; the exact
  per-table field list is fixed in the implementation plan and covered by
  round-trip tests.

### API

**`GET /v1/sys/backup`**
- Auth: `RequireAuth` + new instance-scoped authz action **`sys:backup`**
  (internal/authz/actions.go), granted to owner/admin like `sys:seal`.
- Requires an unsealed instance implicitly (login/session auth needs it).
- Streams the JSONL with `Content-Type: application/x-ndjson` and
  `Content-Disposition: attachment; filename="janus-backup-<timestamp>.jsonl"`.
- **Audited:** one `sys.backup` event (actor, result; no values, no row
  counts needed). Audit-write failure fails the request, per the global rule.

**`POST /v1/sys/restore`**
- A **pre-init bootstrap operation**, like `/v1/sys/init`: unauthenticated,
  but valid **only when the instance is empty** — seal config absent AND zero
  users AND zero projects. Anything else → `409` code `not_empty`.
- Validates the header: unknown `janus_backup` version → `422`;
  `migration_version` must **exactly match** the running binary's applied
  migrations — mismatch → `422` with a message naming both versions ("run the
  janus version that wrote this backup, or migrate a fresh DB to version N").
- Applies all records in a **single transaction**, in FK order. Any failure
  rolls back everything; the instance remains empty and restorable.
- After commit, appends a **`sys.restore` audit event to the restored hash
  chain** (its `prev_hash` = the chain head from the backup) — chain
  continuity is preserved and `GET /v1/audit/verify` passes across the
  restore boundary.
- Post-restore state: initialized + **sealed**. The operator unseals with the
  original shares/KMS key. (Restore itself never touches the keyring or any
  plaintext.)
- Serialized against `/v1/sys/init` with the existing `initMu` so a
  concurrent init/restore race cannot interleave.

### CLI

- **`janus backup [--out FILE]`** — default stdout (pipe-friendly). Auth:
  `--token` flag or stored session, same pattern as `janus seal` (§2).
- **`janus restore [FILE]`** — default stdin. Unauthenticated (empty-instance
  only, enforced server-side). Prints a completion summary and reminds the
  operator to unseal with the original shares.
- Writing the backup to disk is acceptable — it contains only ciphertext; the
  "never write plaintext secrets to disk" CLI rule is not violated.

### DR runbook (documented with the feature)

1. Fresh Postgres + `janus migrate` (or server auto-migrate) to the matching
   version. 2. `janus restore backup.jsonl`. 3. `janus unseal` with the
   original shares (or KMS auto-unseal with the same key). 4. Verify:
   `janus seal-status`, `GET /v1/audit/verify`, spot-read a secret.

### Testing

- **Round-trip e2e (testcontainers):** populate an instance (projects, envs,
  configs, multi-version secrets, tokens, transit keys, audit events) →
  backup → fresh DB → restore → unseal with the same shares → all secrets
  read back plaintext-equal; transit encrypt/decrypt still works; audit chain
  verifies across the boundary.
- Restore refused on a non-empty instance (`409 not_empty`).
- Header validation: bad format version, migration-version mismatch → `422`.
- Mid-stream failure (truncated input) → transaction rolled back, instance
  still empty and restorable.
- Authz: non-admin gets 403 on backup; `sys.backup` audit event written.
- **Leak test:** extend the grep-based no-plaintext test over a real backup
  fixture produced from known plaintext values — none may appear.

---

## Delivery

- **PR 1** — branch `ops-probes-seal-idle`: items 1–3. Small, independent,
  quick review.
- **PR 2** — branch `ops-backup-restore`: item 4, on its own review track.
- Usual gates on both: `go build ./...`, `go vet ./...`, `go test ./...`
  (testcontainers), `gosec`, `govulncheck`; no new migrations expected for
  items 1–3; item 4 adds no schema changes either (it reads/writes existing
  tables) unless the plan surfaces a need.

## Non-goals (this batch)

- Prometheus metrics endpoint (proposed separately; not in this batch).
- Scheduled/automatic backups (operators cron `janus backup` themselves).
- Portable passphrase-encrypted backups (`--portable`) — explicitly rejected
  for now; key-preserving only.
- Merge/upsert restore, cross-version restore, partial (per-project) restore.
- UI for backup/restore (CLI + API only; a Settings-page affordance can come
  with the Settings screen later).
