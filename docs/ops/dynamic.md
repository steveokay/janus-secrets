# Dynamic Postgres credentials

A **dynamic role** issues short-lived Postgres credentials on demand. An
admin registers a role against one config: an **admin DSN** (a
privileged Postgres connection Janus uses to create/drop roles) plus
SQL **templates** for creation, revocation, and renewal. A developer then
**issues** credentials against that role — Janus generates a unique
username and password, runs the creation template to make a real Postgres
role, and records a **lease**. When the lease's TTL expires (or is
revoked), the lease manager runs the revocation template and the role is
gone.

Unlike static rotation (which rewrites one existing secret's value) or
sync (which replicates a config's secrets outward), dynamic credentials
are **minted per request and never stored**: the password is returned
exactly once, at issue time, and is never persisted, logged, or audited.
There is nothing to reveal later — if you lose it, revoke the lease and
issue a new one.

This is the Vault "database secrets engine" model, scoped to Postgres.

## SQL templates

A role carries three admin-authored SQL templates. Janus interpolates
three placeholders into them before executing against the admin DSN:

| Placeholder | Substituted with |
|---|---|
| `{{name}}` | the generated username (`janus_<role>_<random>`, ≤63 bytes) |
| `{{password}}` | the generated password (32 chars; creation only) |
| `{{expiration}}` | the lease expiry as an RFC3339 timestamp |

- **Creation** (required) — must reference both `{{name}}` and
  `{{password}}`. Example:
  `CREATE ROLE "{{name}}" LOGIN PASSWORD '{{password}}' VALID UNTIL '{{expiration}}' IN ROLE app_readwrite;`
- **Revocation** (optional) — if set, must reference `{{name}}`. Defaults
  to `DROP ROLE IF EXISTS "{{name}}";` when left blank. The default is
  idempotent, which is what makes crash-safety and retries safe.
- **Renewal** (optional) — if set, must reference `{{name}}`. Defaults to
  `ALTER ROLE "{{name}}" VALID UNTIL '{{expiration}}';` when left blank —
  necessary because a `VALID UNTIL` set at creation is enforced by
  Postgres regardless of Janus's own bookkeeping, so renewing the lease
  must also push the DB-side expiry.

**You own the quoting.** Janus substitutes only its own generated,
sanitized, quote-free values (the username is re-validated against
`^[A-Za-z_][A-Za-z0-9_]{0,62}$` at interpolation time; the password draws
from a fixed alphanumeric alphabet), so the template author places the
quotes appropriate to each SQL context (identifier `"…"` vs. literal
`'…'`), exactly as the examples above do. Janus does not add or strip
quoting for you.

The templates may contain **multiple statements** separated by `;`. They
run over pgx's simple protocol as a single implicit transaction, so a
failure partway through rolls the whole batch back — a creation template
never leaves a half-made role behind.

## Admin DSN setup (least privilege)

The admin DSN is a Postgres connection string for a role that can
`CREATE ROLE` / `DROP ROLE` and grant whatever memberships your creation
template hands out — and nothing more. Do **not** use the database
superuser. A dedicated management role is enough:

```sql
CREATE ROLE janus_dyn_admin LOGIN PASSWORD '…' CREATEROLE;
GRANT app_readwrite TO janus_dyn_admin WITH ADMIN OPTION;  -- so it can grant it onward
```

`CREATEROLE` lets it create and drop the ephemeral login roles;
`WITH ADMIN OPTION` on each membership your template grants lets it hand
that membership to the new role. Scope the memberships to exactly the
access the issued credentials should have.

The admin DSN is **envelope-encrypted** under the owning project's KEK
before storage (domain-separated AAD, same hierarchy as every other
secret) and is decrypted only in server memory while sealed — so
role management is unavailable while the server is sealed. It is never
returned by the API, logged, or written to `last_error` (see Masking).

## TTLs

Each role has a **default TTL** (`--default-ttl-seconds`, default `3600`)
and a **max TTL** (`--max-ttl-seconds`, default `86400`). Issuing a lease
sets its expiry to now + default TTL and its hard ceiling
(`max_expires_at`) to now + max TTL. Renewal extends expiry by another
default TTL but never past the ceiling, and never moves expiry backward
(lowering a role's default TTL after issue cannot shorten an existing
lease). Validation requires `0 < default_ttl ≤ max_ttl`.

## Lease lifecycle

A lease moves through these statuses:

```
creating ──▶ active ──▶ expired        (TTL reached, revoked by the manager)
                    └──▶ revoked        (explicit RevokeLease / role delete)
         (apply fails) ──▶ revoked      (creation rolled back, row cleaned up)
   (revocation fails) ──▶ revoke_failed ──▶ (retried every tick) ──▶ expired
```

**Crash-safe issue.** `IssueCreds` persists the lease as `creating`
*before* touching Postgres, then runs `CREATE ROLE`, then flips it to
`active`. A crash between the DB write and the flip leaves a `creating`
row the lease manager reclaims — and the caller, who never received a
password, must simply re-issue. In-flight `creating` rows are protected
by a **5-minute grace window**: the manager only reclaims a `creating`
lease older than the window, so it never revokes a role a running
`IssueCreds` just made.

## The lease manager

An in-process scheduler runs alongside the server — no separate
worker/cron to deploy, same shape as the rotation and sync schedulers.
Each pass (`RunDue`) claims and revokes every lease that is due:

- **active** leases past their expiry → run revocation → `expired`;
- **revoke_failed** leases → retried on **every** tick until they
  succeed (there is no backoff here — a lingering live DB role is a
  standing risk, so the manager keeps trying rather than backing off);
- **creating** leases older than the grace window (crash orphans) →
  reclaimed → `expired`.

| Variable | Default | Meaning |
|---|---|---|
| `JANUS_DYNAMIC_TICK` | `60s` | Go duration between scheduler passes. Set `0` to disable the scheduler on this instance (leases still exist and can be revoked manually via `janus dynamic revoke`, but nothing expires automatically) |

The scheduler stops on graceful shutdown (SIGTERM) with the rest of the
server; there is nothing extra to drain.

## Revoke-on-startup sweep

A crash can leave leases that expired while the server was down, plus
`creating` orphans. On every **sealed → unsealed** transition (KMS
auto-unseal at boot, a KMS retry, or a completed Shamir ceremony) Janus
runs one immediate sweep (`SweepOrphanedLeases`, a single `RunDue` pass)
so those roles are revoked promptly instead of waiting a full tick. The
sweep needs the admin DSN decrypted, which is exactly why it is tied to
the unseal edge rather than to process start.

## Sealed behavior

While the server is sealed, every dynamic operation that needs the admin
DSN is unavailable: `IssueCreds`, `RenewLease`, `RevokeLease`, and
`DeleteRole` return the `sealed` error, and the scheduler's `RunDue` pass
is a complete no-op (it does not even claim due leases). Nothing is
counted as a failure. Once unsealed, the startup sweep and the next tick
catch up any overdue leases.

## RBAC & audit

Two permissions, scoped to the role's config:

- **`dynamic:manage`** — create / get / list / delete roles. Granted to
  the project **admin** and **owner** roles (same tier as
  `rotation:manage` / `sync:manage`).
- **`dynamic:issue`** — issue credentials, renew, revoke, and list
  leases. Granted to **developer** and above.

The split is deliberate: authoring a role means handing Janus a
privileged admin DSN and the SQL that runs under it (an admin act), while
issuing a short-lived credential from an already-vetted role is a routine
developer act.

Every lease action writes a **value-free** audit event under a `system`
actor (`dynamic:<role-id>` — never the triggering user, since scheduled
expiries have none): `dynamic.creds.issue` (records `db_user=<username>`,
never the password), `dynamic.lease.renew`, `dynamic.lease.revoke`, and
`dynamic.lease.expire`. The password never appears in any event, log
line, or error string — a dedicated leak test asserts this over captured
logs and the audit export.

## Masking

`GET`/`list` responses mask role configuration exactly like rotation and
sync mask theirs: the admin DSN and the SQL templates are **never**
echoed back by the API. Only non-secret fields — role name, config path,
TTLs, and lease status/username/expiry — are returned. The issue response
is the **only** place a password is ever surfaced, and only once.

## CLI usage

```sh
# Register a role (admin, dynamic:manage). Creation template required;
# revocation/renewal fall back to the safe defaults if omitted.
janus dynamic roles create --config $CONFIG --name readonly \
  --admin-dsn 'postgres://janus_dyn_admin:pw@db:5432/app?sslmode=require' \
  --creation $'CREATE ROLE "{{name}}" LOGIN PASSWORD \'{{password}}\' VALID UNTIL \'{{expiration}}\' IN ROLE app_readonly;' \
  --default-ttl-seconds 3600 --max-ttl-seconds 86400

# Inspect roles.
janus dynamic roles list --config $CONFIG
janus dynamic roles get <role-id>

# Issue credentials (developer, dynamic:issue). Password shown ONCE.
janus dynamic creds <role-id>
# → lease=<id>  username=janus_readonly_…  password=…  expires=…

# Renew / revoke a lease; list a role's leases.
janus dynamic renew <lease-id>
janus dynamic revoke <lease-id>
janus dynamic leases --role <role-id>

# Deleting a role revokes every still-live lease first, then removes it.
janus dynamic roles delete <role-id>
```

`roles create` accepts `--revocation` and `--renew` to override the
default templates. Deleting a role revokes its active/creating/
revoke_failed leases before removing it, so no live DB role is ever
orphaned; if any revocation fails, the role is left in place.

## Backup & restore

`dynamic_roles` and `dynamic_leases` rows are included in `janus backup` /
`janus restore` like any other table: the envelope-encrypted admin DSN
and SQL templates travel wrapped, alongside the non-secret lease
bookkeeping, as part of the key-preserving instance dump. A restored
instance — once unsealed with the original unseal material — keeps its
roles and resumes revoking due leases on the next tick (and on the
post-unseal sweep). The ephemeral **Postgres roles themselves** live in
your database, not in Janus, so they are covered by your database's own
backups; a Janus restore reconciles leases against whatever roles exist
there. See [backup-restore.md](backup-restore.md) for the general
procedure.
