# Store Layer (Foundation + Core CRUD) — Design

**Milestone 2, slice 1 of the Phase-1 store line.** Date: 2026-07-03.

## Goal

Build `internal/store`: a **crypto-blind** PostgreSQL persistence layer over
`pgxpool`, with a `golang-migrate` runner, the core data-model schema
(project → environment → config → secret) with **two-level versioning** and
**soft-delete**, typed repositories, and a Postgres-backed `SealConfigStore`.

Ends with: `make migrate` applies the schema, the server can persist and load
seal config from Postgres, and the primary hierarchy is fully persisted and
tested against real Postgres.

## Scope

**In scope:**
- DB connection/pool (`Store` over `*pgxpool.Pool`).
- `golang-migrate` runner with migrations embedded via `go:embed`.
- Initial schema migration for the seven core tables.
- Postgres-backed `crypto.SealConfigStore`.
- Repositories: projects, environments, configs, secrets (with versioning).
- Integration tests via testcontainers-go.

**Explicitly deferred to later specs:**
- Config **inheritance** resolution (root/branch configs). A nullable
  `inherits_from` column is added now for forward-compat, but no resolution
  logic is implemented.
- Secret **references** (`${projects.other.prod.KEY}`) and cycle-checking —
  these are read-time *resolution* concerns that belong with the service/API
  layer.
- Encryption orchestration (holding the unsealed `Keyring`, encrypting
  plaintext) — the store only ever handles opaque ciphertext.
- KEK/master-key **rotation** operations.
- Cursor pagination (a later API concern; repos expose simple `List` for now).

## Key decisions

1. **Scope = foundation + core CRUD repositories** (not the whole Phase-1 store
   line). Inheritance and references are separate follow-on specs.
2. **Hand-written SQL + `pgx`/`pgxpool`.** No codegen, no query builder. SQL
   lives as parameterized string constants in each repository. Matches the
   project's stdlib-first, minimal-dependency ethos.
3. **Two-level versioning via a manifest of value pointers** (git-tree style).
   Config versions are immutable manifests; secret values are append-only
   per-key rows. Diff/rollback are set operations; unchanged values are reused,
   not copied.
4. **Crypto-blind store.** Repositories persist and return opaque encrypted
   bytes (`wrapped_dek`, `ciphertext`, `nonce`, `wrapped_kek`). Encryption is
   done by a higher layer that holds the `Keyring`. This mirrors the existing
   `crypto.SealConfigStore`, which stores the *wrapped* master key.

## Architecture & package layout

`internal/store` — a persistence layer, blind to cryptography.

| File | Responsibility |
|------|----------------|
| `store.go` | `Store` wrapping `*pgxpool.Pool`; `Open(ctx, dsn)`, `Close()`, `Ping()`, `withTx()`. |
| `migrate.go` | `golang-migrate` runner; embeds `migrations/*.sql` via `go:embed` (iofs source). `Migrate(ctx)` applies up-migrations. |
| `sealconfig.go` | `PostgresSealConfigStore` implementing `crypto.SealConfigStore`. |
| `models.go` | Domain structs (`Project`, `Environment`, `Config`, `ConfigVersion`, `SecretValue`, `EncryptedValue`, `Change`, ...). |
| `errors.go` | Store sentinels + pgx error mapping. |
| `projects.go` | `ProjectRepo`. |
| `environments.go` | `EnvironmentRepo`. |
| `configs.go` | `ConfigRepo`. |
| `secrets.go` | `SecretRepo` (versioning core). |

**New dependencies** (all anticipated by CLAUDE.md):
- `github.com/jackc/pgx/v5` (+ `pgxpool`) — runtime.
- `github.com/golang-migrate/migrate/v4` (pgx driver + iofs source) — runtime.
- `github.com/testcontainers/testcontainers-go` + `postgres` module — test-only.

**Minor decisions:**
- **IDs:** `uuid` PKs, `DEFAULT gen_random_uuid()` (built into PG16 core; no
  extension, DB-generated, no app-side UUID dependency).
- **Addressing:** human `slug`/`name` columns with partial-unique constraints
  (scoped to `deleted_at IS NULL`), so deferred reference/inheritance features
  have something to resolve against.

## Schema (migration `000001_init`)

Encrypted columns are `bytea` opaque blobs. Entity soft-delete via nullable
`deleted_at`; secret-level delete is a tombstone manifest entry.

### `seal_config`
Single row, enforced by `id int PRIMARY KEY CHECK (id = 1)`. Mirrors
`crypto.SealConfig`: `type text`, `threshold int`, `shares int`,
`key_check_value bytea`, `wrapped_master_key bytea`.

### `projects`
`id uuid PK`, `slug text NOT NULL`, `name text`, `wrapped_kek bytea NOT NULL`
(project KEK wrapped by the master key), `kek_version int NOT NULL DEFAULT 1`,
`created_at`, `updated_at`, `deleted_at`.
Partial unique: `slug WHERE deleted_at IS NULL`.

### `environments`
`id uuid PK`, `project_id uuid → projects`, `slug text NOT NULL`, `name text`,
timestamps, `deleted_at`.
Partial unique: `(project_id, slug) WHERE deleted_at IS NULL`.

### `configs`
`id uuid PK`, `environment_id uuid → environments`, `name text NOT NULL`,
`inherits_from uuid NULL → configs(id)` (forward-compat, unresolved),
timestamps, `deleted_at`.
Partial unique: `(environment_id, name) WHERE deleted_at IS NULL`.

### `config_versions`
`id uuid PK`, `config_id uuid → configs`, `version int NOT NULL`,
`message text`, `created_by text NULL` (auth arrives later), `created_at`.
Unique: `(config_id, version)`. Immutable.

### `secret_values`
Append-only per-key history. `id uuid PK`, `config_id uuid → configs`,
`key text NOT NULL`, `value_version int NOT NULL`, `wrapped_dek bytea`,
`ciphertext bytea`, `nonce bytea`, `dek_key_version int`, `created_at`.
Unique: `(config_id, key, value_version)`. Never updated or deleted.

### `config_version_entries`
The manifest. `config_version_id uuid → config_versions`, `key text NOT NULL`,
`secret_value_id uuid NULL → secret_values`, `tombstone bool NOT NULL DEFAULT
false`. PK `(config_version_id, key)`.
CHECK: `tombstone = (secret_value_id IS NULL)` — a tombstone marks a key deleted
at that version.

`down.sql` drops all seven tables in FK order.

### Core queries

**Resolve a config's current state** = latest version's manifest joined to
values, tombstones excluded:
```sql
-- latest = MAX(version) for config_id
SELECT e.key, sv.*
FROM config_version_entries e
JOIN secret_values sv ON sv.id = e.secret_value_id
WHERE e.config_version_id = $latest AND NOT e.tombstone;
```

**Rollback to vN** = new config version whose entries copy vN's manifest rows
(reusing existing `secret_value` ids — no re-encryption).

**Diff(vA, vB)** = set-compare the two manifests by `(key, secret_value_id)`.

## Repositories

Encrypted input/output is carried opaquely:

```go
type EncryptedValue struct {
    WrappedDEK    []byte
    Ciphertext    []byte
    Nonce         []byte
    DEKKeyVersion int
}
```

**`ProjectRepo`** — `Create(ctx, slug, name, wrappedKEK, kekVersion)`, `Get`,
`GetBySlug`, `List`, `SoftDelete`, `Undelete`, `Destroy`.

**`EnvironmentRepo`** — `Create(ctx, projectID, slug, name)`, `Get`,
`GetBySlug(ctx, projectID, slug)`, `ListByProject`, `SoftDelete`, `Undelete`,
`Destroy`.

**`ConfigRepo`** — `Create(ctx, environmentID, name, inheritsFrom *uuid)`,
`Get`, `GetByName(ctx, environmentID, name)`, `ListByEnvironment`, `SoftDelete`,
`Undelete`, `Destroy`.

**`SecretRepo`** — the versioning core. The flagship operation is the batched,
atomic save:

```go
// A save batches per-key edits into ONE new immutable config version.
type Change struct {
    Key   string
    Value *EncryptedValue // non-nil = set; nil = delete (tombstone)
}
SaveConfigVersion(ctx, configID, changes []Change, message, createdBy string) (ConfigVersion, error)
```

In one transaction it: (1) computes the next `version` (serialized, see below);
(2) for each *set*, appends a `secret_values` row at
`value_version = prevMax(key)+1`; (3) builds the new manifest by carrying
forward the previous version's entries, applying sets (new `secret_value_id`)
and deletes (tombstone); (4) inserts the `config_version` and its
`config_version_entries`. An empty first version is allowed.

Reads & history:
- `GetLatest(ctx, configID) (ConfigVersion, map[string]SecretValue)` — default read.
- `GetVersion(ctx, configID, version)` — state at a specific version.
- `ListVersions(ctx, configID)` — version metadata for history UI.
- `GetKeyHistory(ctx, configID, key) []SecretValue` — per-key value trace.
- `Diff(ctx, configID, vA, vB) {added, changed, removed []string}`.
- `Rollback(ctx, configID, targetVersion, message, createdBy)` — new version
  copying the target manifest (reuses value ids; no re-encryption).

## Transactions, concurrency & errors

**Transaction helper:** `Store.withTx(ctx, func(pgx.Tx) error) error` — begins,
`defer`s rollback, commits on nil. `SaveConfigVersion` and `Rollback` run
entirely inside one `withTx`.

**Version-number race:** at the start of each save transaction,
`SELECT ... FOR UPDATE` on the `configs` row. This serializes saves per-config
and simultaneously verifies the config exists and isn't soft-deleted before
writing. Read Committed isolation is then sufficient. The
`UNIQUE (config_id, version)` constraint is a correctness backstop.

**Error sentinels** (`errors.go`), mapped from pgx at the repo boundary so
callers never see driver types:

| Condition | Sentinel |
|-----------|----------|
| `pgx.ErrNoRows` | `ErrNotFound` |
| unique violation (`23505`) | `ErrAlreadyExists` |
| FK violation (`23503`) | `ErrParentNotFound` |
| save against soft-deleted/absent config | `ErrConflict` |

Errors carry no secret material. `migrate.ErrNoChange` is treated as success.

## Testing

Integration tests against **real Postgres via testcontainers-go** (no mock DB).

**Harness (`main_test.go`):** `TestMain` starts one Postgres 16 container per
package run, applies the embedded migrations, and exposes a package-level
`*Store`. If Docker is unavailable, tests `t.Skip` via a `requireStore(t)`
helper (so `go test` stays green locally; CI runs them for real). Between tests,
`resetDB(t)` does `TRUNCATE ... RESTART IDENTITY CASCADE`.

**Coverage (table-driven where it fits):**
- **Migrations:** up → down → up applies cleanly.
- **SealConfig:** round-trip through `PostgresSealConfigStore` (empty →
  `ErrNoSealConfig`, put/get), reusing the crypto `SealConfigStore` contract.
- **Project/Env/Config CRUD:** create/get/get-by-slug/list; soft-delete hides
  from get+list; undelete restores; destroy removes; unique-slug →
  `ErrAlreadyExists`; missing → `ErrNotFound`; orphan FK → `ErrParentNotFound`.
- **Secrets versioning:**
  - first save → v1; `GetLatest` resolves state;
  - batched multi-key save in one version;
  - **dedup:** editing 1 of N keys writes exactly 1 new `secret_values` row,
    manifest reuses the rest (asserted by row count);
  - `value_version` increments per key; `GetKeyHistory` returns ordered trace;
  - delete → tombstone (absent from `GetLatest`), re-add via new save;
  - `GetVersion(old)` returns historical state; `Diff` reports
    added/changed/removed;
  - `Rollback` recreates state, **reuses** `secret_value` ids (asserts no new
    value rows) and bumps version;
  - **concurrency:** N goroutines saving the same config all succeed with
    contiguous versions `1..N`, no duplicates — validating the `FOR UPDATE`
    serialization.
- **Error mapping** at the repo boundary.

No secret-leak test is needed here: the store is crypto-blind, so only
ciphertext ever flows through it.

## Non-goals for this milestone

Inheritance resolution, secret references, encryption orchestration, key
rotation, cursor pagination, and the Postgres schema for auth/audit (later
milestones). See the deferred list under **Scope**.
