# Phase 3.3 — Dynamic Postgres Credentials + Lease Manager

**Status:** Approved 2026-07-11
**Package:** `internal/dynamic/`
**Slice:** Final Phase 3 slice (rotation → sync → dynamic)

## Summary

On-demand, short-lived Postgres credentials issued by Janus, with a lease
manager that enforces TTL, renewal, and revocation on expiry — plus a
revoke-on-startup sweep for leases orphaned by a crash. Modeled on Vault's
dynamic database secrets, scoped and encrypted the same way as the two Phase-3
packages already shipped (`internal/rotation`, `internal/secretsync`).

Postgres is the only backend (per CLAUDE.md non-goals: "Dynamic secrets backends
beyond Postgres (until explicitly requested)").

## Design decisions (locked)

1. **Role model — Vault-style SQL templates.** An admin stores
   `creation_statements` / `revocation_statements` / `renew_statements` with
   `{{name}}`, `{{password}}`, `{{expiration}}` placeholders. Maximum flexibility
   (schema grants, `ALTER DEFAULT PRIVILEGES`, connection limits, `SET
   search_path`) because real dynamic-Postgres use needs more than a role-grant
   list. Safe because: the admin authoring the template already holds the admin
   DSN (the template grants no new authority — trusted, single-tenant config
   surface, not untrusted API input); and the only interpolated values are
   Janus-generated and sanitized (see §5), so the injection surface on
   caller-influenced input is zero.
2. **Scoping — config-scoped.** Each role attaches to a project→env→config,
   exactly like `rotation_policies` / `sync_targets`. Reuses `projectForConfig`
   for the KEK and the existing config/env RBAC — no new RBAC concepts.
3. **Surface — standalone.** Ship `/v1/dynamic` + `janus dynamic` mirroring the
   rotation/sync surfaces. No `janus run` auto-leasing this slice (deferred fast
   follow-up once the engine is proven).
4. **TTL/renewal — Vault conventions.** A role has `default_ttl` and `max_ttl`.
   A lease is created with `default_ttl`; renewal extends `expires_at` but never
   past `issued_at + max_ttl`.

## Data model

Migration `000012_dynamic.up.sql` / `.down.sql`.

### `dynamic_roles`

```sql
CREATE TABLE dynamic_roles (
  id                     uuid PRIMARY KEY,
  project_id             uuid   NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
  config_id              uuid   NOT NULL REFERENCES configs(id)  ON DELETE CASCADE,
  name                   text   NOT NULL,
  default_ttl_seconds    bigint NOT NULL CHECK (default_ttl_seconds > 0),
  max_ttl_seconds        bigint NOT NULL CHECK (max_ttl_seconds >= default_ttl_seconds),
  config_ct              bytea  NOT NULL,
  config_nonce           bytea  NOT NULL,
  config_wrapped_dek     bytea  NOT NULL,
  config_dek_kek_version int    NOT NULL,
  created_by             text   NOT NULL,
  created_at             timestamptz NOT NULL DEFAULT now(),
  updated_at             timestamptz NOT NULL DEFAULT now(),
  UNIQUE (config_id, name)
);
```

Decrypted `RoleConfig` blob (never logged or persisted in clear):

```go
type RoleConfig struct {
    AdminDSN             string `json:"admin_dsn"`
    CreationStatements   string `json:"creation_statements"`
    RevocationStatements string `json:"revocation_statements"`
    RenewStatements      string `json:"renew_statements,omitempty"`
}
```

`RenewStatements` defaults to `ALTER ROLE "{{name}}" VALID UNTIL '{{expiration}}';`
when empty.

### `dynamic_leases`

```sql
CREATE TABLE dynamic_leases (
  id             uuid PRIMARY KEY,
  role_id        uuid NOT NULL REFERENCES dynamic_roles(id) ON DELETE CASCADE,
  project_id     uuid NOT NULL REFERENCES projects(id)      ON DELETE CASCADE,
  db_username    text NOT NULL,
  status         text NOT NULL DEFAULT 'creating'
                   CHECK (status IN ('creating','active','revoked','expired','revoke_failed')),
  issued_at      timestamptz NOT NULL DEFAULT now(),
  expires_at     timestamptz NOT NULL,
  max_expires_at timestamptz NOT NULL,
  renewed_at     timestamptz,
  revoked_at     timestamptz,
  last_error     text,
  created_by     text NOT NULL,
  created_at     timestamptz NOT NULL DEFAULT now(),
  updated_at     timestamptz NOT NULL DEFAULT now()
);

-- Lease-manager due-scan.
CREATE INDEX dynamic_leases_due ON dynamic_leases (expires_at) WHERE status = 'active';
-- Boot sweep of crash-orphaned in-flight issues.
CREATE INDEX dynamic_leases_creating ON dynamic_leases (id) WHERE status = 'creating';
```

**The generated password is never persisted.** It is returned once at issue time
(like a service token) and discarded. Revocation needs only `db_username`. A
lease row at rest holds zero plaintext secret.

## Engine — `internal/dynamic/`

Mirrors `internal/rotation/` file-for-file:

- `dynamic.go` — `Service`, `New`, envelope helpers (reused verbatim from the
  rotation pattern), `RoleConfig`, view projections, `projectForConfig`.
- `crud.go` — role `Create` / `Get` / `ListByConfig` / `Update` / `Delete`.
  `Delete` revokes outstanding leases first.
- `generate.go` — `IssueCreds`.
- `lease.go` — `Renew`, `Revoke`, lease views.
- `postgres.go` — admin executor (connect, run statements, sanitize).
- `scheduler.go` — `RunScheduler` + `RunDue`.
- `sweep.go` — `SweepOrphanedLeases`.
- `errors.go` — sentinels + `mapStoreErr`.

### IssueCreds — crash-safe persist→apply→commit

Same discipline as the rotation engine's apply path:

1. **Reserve:** generate `db_username`; insert lease `status='creating'`,
   `expires_at = now + default_ttl`, `max_expires_at = now + max_ttl`; commit.
2. **Apply:** connect admin DSN; run `CreationStatements` inside a transaction,
   interpolating the Janus-generated safe values (§5).
3. **Commit:** update lease `status='active'`; return
   `{lease_id, username, password, expires_at}`.

A crash between (1) and (3) leaves a `creating` lease. The caller never received
a response (never got the password), so the boot sweep revokes it idempotently
(`DROP ROLE IF EXISTS`), whether or not the DB role was actually created.

### Renew

Bounded by `max_expires_at`. Computes `new_expires = min(now + default_ttl,
max_expires_at)`, runs `RenewStatements` with `{{name}}=db_username` and
`{{expiration}}=new_expires`, then updates `expires_at` + `renewed_at`.

Running `RenewStatements` is required, not cosmetic: if `CreationStatements` sets
`VALID UNTIL '{{expiration}}'`, Postgres enforces the original expiry regardless
of Janus's `expires_at`, so renewal must push the DB-side `VALID UNTIL` too.

### Revoke / expire

Runs `RevocationStatements` interpolating `{{name}}=db_username`. Default when a
role supplies none: `DROP ROLE IF EXISTS "{{name}}";` (idempotent). On success →
`status='revoked'` (manual) or `'expired'` (scheduler); on failure →
`'revoke_failed'` + `last_error`, retried next tick.

## Lease manager (scheduler + sweep)

- **`RunDue`** every `JANUS_DYNAMIC_TICK`: claim `active` leases with
  `expires_at <= now` (batched), revoke each, mark `expired` / `revoke_failed`.
  No-op while sealed. Per-lease errors never abort the pass.
- **`RunScheduler(ctx, tick)`** — ticker loop tied to the boot ctx; `tick <= 0`
  disables (tests). Wired in `boot.go` behind `if bc.DynamicTick > 0`.
- **`SweepOrphanedLeases(ctx)` at boot** — in `boot.go` alongside the existing
  session/OIDC sweeps: revoke leases stuck in `creating` (crash mid-issue) and
  run one `RunDue` pass for leases that expired while the server was down. This
  is the CLAUDE.md-mandated revoke-on-startup sweep.

### Server wiring

New `JANUS_DYNAMIC_TICK` env (default `60s`, `0` disables) parsed in
`cmd/janus/server.go`; new `BootConfig.DynamicTick`; `boot.go` constructs
`dynamicSvc`, calls `SweepOrphanedLeases`, and launches `RunScheduler` — all
mirroring the rotation/sync blocks already present.

## Crypto

One addition: `crypto.DynamicConfigAAD(roleID)` alongside `RotationConfigAAD`.
No pending-blob AAD (no password at rest). All other envelope helpers reused
from the rotation pattern (`sealBlob` / `openBlob` / `unwrapProjectKEK`).

## Injection safety of interpolation

- `{{name}}` — `db_username` generated to satisfy the rotation engine's `roleRe`
  (`^[A-Za-z_][A-Za-z0-9_]{0,62}$`, ≤63 bytes), rendered via
  `pgx.Identifier{}.Sanitize()`. Format: `janus_<role-prefix>_<random>`.
- `{{password}}` — CSPRNG from the rotation engine's alphanumeric alphabet,
  rendered via `quoteLiteral` (defensive; the alphabet has no quotes).
- `{{expiration}}` — RFC3339 literal.
- Required placeholders validated at role create/update.
- Admin DSN never logged; pgx connect errors sanitized (host/port scrubbed) as
  the rotation engine already does.

## API — `/v1/dynamic/` (mirrors `/v1/rotation`)

| Method | Path | Authz | Notes |
|--------|------|-------|-------|
| `POST`   | `/roles` | `DynamicManage` (admin) | create role |
| `GET`    | `/roles?config_id=` | `DynamicManage` | list (masked) |
| `GET`    | `/roles/{id}` | `DynamicManage` | masked view |
| `PATCH`  | `/roles/{id}` | `DynamicManage` | TTLs and/or config blob |
| `DELETE` | `/roles/{id}` | `DynamicManage` | revokes outstanding leases first |
| `POST`   | `/roles/{id}/creds` | `DynamicIssue` (developer+, config read) | issues lease; returns password **once** |
| `GET`    | `/leases?role_id=` | `DynamicIssue` | masked (no password) |
| `POST`   | `/leases/{id}/renew` | `DynamicIssue` | extends `expires_at` |
| `POST`   | `/leases/{id}/revoke` | `DynamicIssue` | immediate revoke |

Masked role/lease views never carry the admin DSN, statements, or password.

## CLI — `janus dynamic`

- `janus dynamic roles list|create|get|delete`
- `janus dynamic creds <role> [--format env]` — prints `username`, `password`,
  `lease_id`, `expires_at`
- `janus dynamic renew <lease-id>`
- `janus dynamic revoke <lease-id>`
- `janus dynamic leases list`

## Audit

Every mutation writes an event (audit-write-failure fails the mutation, per
policy):

- `dynamic.role.{create,update,delete}`
- `dynamic.creds.issue` — records role name, lease id, `db_username`; **never the
  password**
- `dynamic.lease.{renew,revoke}`
- `dynamic.lease.expire` — system actor (scheduler)

## Testing

- Table-driven unit tests with a fake keyring for CRUD, generate, renew, revoke,
  scheduler, sweep (sealed no-op, batch claim, backoff-on-revoke-failure).
- testcontainers Postgres for the real admin-connect / create / renew / revoke
  path (mirroring `postgres_rotator_test.go`).
- Crash-safety test: reserve → simulated crash (lease left `creating`) → boot
  sweep revokes idempotently.
- Extend the grep-based leak test: the generated password never appears in log
  output or audit entries.
- `internal/crypto` additions keep 100% branch coverage.

## Non-goals for this slice

Consistent with rotation/sync: no web UI, no `janus run` auto-leasing, no
non-Postgres backend.
