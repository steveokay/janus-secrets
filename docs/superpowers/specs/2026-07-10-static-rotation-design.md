# Static Rotation Framework — Design

> **Phase 3, sub-project 1 of 3.** Phase 3 decomposes into three independent sub-projects, built in order, each with its own spec → plan → cycle:
> 1. **Static rotation framework** ← *this spec*
> 2. Sync integrations (push Janus secrets outward to GitHub Actions / Kubernetes Secrets)
> 3. Dynamic Postgres credentials + lease manager (ephemeral creds, TTL/renewal/revocation, revoke-on-startup sweep)

**Status:** approved 2026-07-10.

## 1. Overview & boundaries

A new engine at `internal/rotation`, structured like the existing `transit` engine: a `Service` struct holding a store repository + keyring, wired via `New(kr, st)`. A **rotation policy** binds a rotator to exactly one target secret `(project, config, secret_key)` and rotates its value on a fixed interval.

Two rotator types ship in this slice:

- **postgres** — single-role reset. Janus holds admin/DDL credentials for the target database and runs `ALTER ROLE <role> WITH PASSWORD <new-random>`, then commits the new password back into Janus as the managed secret's new value.
- **webhook** — generic push. Janus generates a new random value, POSTs it (HMAC-signed) to a configured URL so the operator's endpoint applies it wherever the credential lives, then commits the value on a 2xx.

Any policy (either type) may additionally carry an **optional post-rotation notify webhook** that fires a **value-free** event for alerting. This is distinct from the webhook *rotator*: the notify hook only announces that a rotation happened.

Deliverables this slice: the engine + scheduler, REST API under `/v1/rotation/`, `janus rotation` CLI verbs, RBAC (`rotation:manage`), migration `000010`. **No web UI** — deferred to a small follow-up slice.

**Explicitly out of scope** (later work): web console for rotation, non-Postgres database rotators, sync integrations (sub-project 2), dynamic secrets (sub-project 3).

## 2. Data model

Migration `000010_rotation` (next free version; current head is `000009`).

`rotation_policies`:

| Column | Notes |
| --- | --- |
| `id` | text, `store.NewID` |
| `project_id` | FK → `projects`. Selects the project KEK and scopes RBAC. |
| `config_id` | FK → `configs`. Target config. |
| `secret_key` | text. The key within the config whose value is rotated. |
| `type` | `postgres` \| `webhook` |
| `interval_seconds` | bigint. Rotation cadence. |
| `next_rotation_at` | timestamptz. Next time the scheduler should attempt this policy. |
| `status` | `active` \| `failed` \| `paused` |
| `failure_count` | int. Consecutive failures; reset to 0 on success. |
| `last_error` | text, nullable. Sanitized message — never a secret value or DSN. |
| `last_rotated_at` | timestamptz, nullable. |
| `last_config_version` | int, nullable. Config version produced by the last successful rotation. |
| `config_ct` / `config_nonce` / `config_wrapped_dek` / `config_dek_kek_version` | Envelope-encrypted rotator config blob (mirrors `secret_values`). |
| `pending_ct` / `pending_nonce` / `pending_wrapped_dek` | Envelope-encrypted pending value for crash-safe apply. Nullable. |
| `pending_state` | text, nullable. Non-null (`applying`) means a rotation is in flight / awaiting commit. |
| `created_by` / `created_at` / `updated_at` | standard. |

The **rotator config blob** is JSON, encrypted (see §8):

- postgres: `{ "admin_dsn": "...", "role": "...", "password_len": 32 }`
- webhook: `{ "url": "...", "hmac_key": "..." }`
- optional on either: `{ "notify_url": "...", "notify_hmac_key": "..." }`

Rotation history is captured by `audit_events` (a `rotation.rotate` event per attempt). No separate `rotation_runs` table — YAGNI.

`FOREIGN KEY` on `config_id` uses the same cascade behavior as other config-scoped rows so destroying a config removes its policies. Uniqueness: at most one policy per `(config_id, secret_key)` (a partial/plain unique index) — one key rotates under one policy.

## 3. Rotation execution (crash-safe ordering)

The correctness hazard: for a single-role reset, if `ALTER ROLE` succeeds but the server crashes before the new password is stored, the old password no longer works and the new one is lost — the credential is orphaned. Solved with **persist-pending → idempotent-apply → commit**:

1. **Unseal-gated.** Decrypt the config blob using the project KEK. If the keyring is sealed the tick skips the policy entirely (no attempt, no failure increment).
2. **Reuse or generate.** If `pending_state` is non-null (resuming after a crash or a prior failed attempt), reuse the existing pending value. Otherwise generate a new random password with `crypto/rand` over a safe charset, length `password_len`.
3. **Persist pending.** Store the pending value (encrypted) durably and set `pending_state='applying'`. This happens *before* any external apply.
4. **Apply.**
   - postgres: connect with `admin_dsn`, run `ALTER ROLE <role> WITH PASSWORD <pending>`. Idempotent — admin credentials authenticate the DDL, so re-running sets the same password regardless of the role's current password.
   - webhook: HMAC-signed `POST` of `{ "policy_id", "secret_key", "new_value", "ts" }` to `url`. Success is a 2xx response.
5. **Commit** (single DB transaction): write the pending value as a new **config version** for the target key (`created_by = "rotation:<id>"`), clear `pending_*`/`pending_state`, set `failure_count=0`, `status='active'`, `last_rotated_at=now`, `last_config_version`, and `next_rotation_at = now + interval`.
6. **Notify** (best-effort, non-blocking). If a notify webhook is configured, POST a value-free event `{ "policy_id", "project", "config", "secret_key", "new_version", "rotated_at" }`, HMAC-signed. A notify failure is logged but does **not** fail the rotation — the rotation already committed.

**Recovery.** On startup and on every tick, any policy with a non-null `pending_state` is resumed from step 2: it reuses the persisted pending value, re-applies (idempotent), and commits. Because retries and crash-recovery both reuse the same pending value, the external apply is always idempotent from the target's perspective; webhook receivers must therefore be idempotent (documented in the runbook).

## 4. Scheduler

A single goroutine started at server boot, tied to the server shutdown `context`. It ticks every `JANUS_ROTATION_TICK` (default `60s`; `0` disables the scheduler for that instance).

Each tick:

1. If `keyring.Sealed()`, skip the tick.
2. Otherwise, in a transaction:
   `SELECT ... FROM rotation_policies WHERE status='active' AND (next_rotation_at <= now() OR pending_state IS NOT NULL) FOR UPDATE SKIP LOCKED LIMIT <batch>`
3. Rotate each selected policy (§3).

The instance is single-node today; `FOR UPDATE SKIP LOCKED` keeps the query correct and future-proof without adding coordination machinery.

## 5. Failure handling

Every rotation attempt — success or failure — writes a `rotation.rotate` audit event recording result (`success` / `failure`), policy id, and target path. **Never values or DSNs.**

On failure:

- `failure_count++`, store a sanitized `last_error`.
- `next_rotation_at = now + backoff(failure_count)` — exponential backoff with a cap (e.g. base 1m, doubling, cap ~1h). This is a short retry, **not** the full interval, so a due window is retried rather than silently skipped for a whole cycle.
- After `failure_count >= 5` consecutive failures, set `status='failed'`. The scheduler ignores `failed` policies until an operator clears them via rotate-now or a `PATCH` to `status='active'`.

`next_rotation_at` re-anchors from the success time on success (`now + interval`); on failure it only advances by the backoff.

## 6. REST API

All routes require `rotation:manage` on the policy's project. Reads are **masked**: they return status/target/schedule/URLs but never the rotator secrets (admin DSN, HMAC keys) or the rotated secret value.

- `POST /v1/rotation/policies` — create. Body: `project_id`, `config_id`, `secret_key`, `type`, `interval_seconds`, type-specific config (admin DSN / role / password_len, or webhook url + hmac_key), optional notify webhook.
- `GET /v1/rotation/policies?project_id=…` — list (masked), cursor-paginated per API conventions.
- `GET /v1/rotation/policies/{id}` — get (masked).
- `PATCH /v1/rotation/policies/{id}` — update interval, rotator config, or `status` (e.g. un-fail a policy).
- `DELETE /v1/rotation/policies/{id}` — remove.
- `POST /v1/rotation/policies/{id}/rotate` — rotate now (manual trigger; clears `failed` status and runs a rotation immediately).

Errors use the project `{"error":{"code","message"}}` envelope. New code `rotation_not_found`; reuse `validation`, `forbidden`, `sealed` (rotate-now while sealed returns `503 sealed`).

## 7. CLI

`janus rotation create | list | get | update | delete | rotate <id>`, built on the existing `apiclient` (`c.call` / JSON) pattern used by other CLI verbs. `rotate` triggers a manual rotation and reports the resulting config version.

## 8. Crypto & security invariants

- The rotator config blob and the pending value are encrypted exactly like secret values: `keyring.NewDEK(projectKEK, aad)` to mint + wrap a per-blob DEK, then AES-256-GCM over the plaintext, storing ciphertext + nonce + wrapped DEK. A new AAD helper `crypto.RotationConfigAAD(policyID)` binds the blob to its policy, mirroring `ProjectKEKAAD` / `DEKAAD` / the OIDC and transit AAD helpers. **No new crypto primitives; stdlib + `x/crypto` only.**
- **Zero plaintext leakage.** Admin DSNs, HMAC keys, and rotated values must never appear in logs, error strings, `last_error`, audit entries, or masked API responses. A grep-based leak test asserts this over captured logs (consistent with the existing project-wide leak test).
- Outbound webhook bodies are signed with HMAC-SHA256; the runbook documents that receivers must verify with a constant-time compare and must treat requests idempotently.
- Rotation pauses automatically while the server is sealed (§3 step 1, §4 step 1); all secret operations already require unseal.
- All policy inputs validated at the API boundary; SQL via parameterized queries only.

## 9. Testing

- **Unit (table-driven):** backoff schedule, failure→`failed` transition at the threshold, state-machine transitions, config-blob encrypt/decrypt round-trip, AAD binding rejects a mismatched policy id.
- **Integration (testcontainers Postgres):**
  - postgres rotator: real `ALTER ROLE` → new value reconnects, old password rejected; new config version committed.
  - crash recovery: a policy left in `pending_state='applying'` resumes idempotently and commits.
  - webhook rotator against an `httptest` server: 2xx commits; 5xx retries with backoff then marks `failed` at the threshold; HMAC signature verified by the test receiver.
  - notify webhook fires a value-free body after a successful rotation.
  - sealed instance ⇒ scheduler performs no rotation.
- **Leak test:** no DSN or secret value appears in captured logs across a rotation run.
- Gates unchanged: `go build`, `go vet`, `go test`, `gosec`, `govulncheck` all green.

## 10. Files (anticipated)

- `internal/crypto/` — add `RotationConfigAAD` helper (small addition to an existing file).
- `internal/rotation/` — `rotation.go` (Service + New), `policy.go` (CRUD/model logic), `postgres_rotator.go`, `webhook_rotator.go`, `notify.go`, `scheduler.go`, `errors.go`, plus tests.
- `internal/store/` — `rotation.go` (repository), migration `migrations/000010_rotation.{up,down}.sql`.
- `internal/api/` — `rotation_handlers.go`, route wiring in the router, `rotation:manage` in the RBAC permission set, error code.
- `cmd/janus/` — `rotation_commands.go` (CLI verbs), scheduler startup wiring in `server.go`, `JANUS_ROTATION_TICK` parsing.
- `docs/ops/rotation.md` — operator runbook (webhook receiver contract, admin-role least-privilege, sealed behavior).
- `docs/operations.md` — CLI + env-var rows.
