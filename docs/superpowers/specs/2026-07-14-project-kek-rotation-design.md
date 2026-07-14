# Project-KEK Rotation (lazy DEK re-wrap) — Design

**Status:** approved (2026-07-14)
**Scope:** gaps.md §4.1, first of two specs. This spec covers **project-KEK rotation** with lazy DEK re-wrap. **Master-key rotation is a separate, later spec** and is explicitly out of scope here.

## Goal

Let a project owner rotate a project's Key-Encryption-Key (KEK) so that every Data-Encryption-Key (DEK) under it is eventually re-wrapped under a fresh KEK, without downtime and without ever decrypting a secret value. Rotation is **instant**; the re-wrap of existing DEKs is **lazy** (old KEK versions are retained so reads never break) and completed on demand by an explicit, resumable `rewrap` sweep.

## Background (current state, verified)

- **Master key**: 32 bytes in `crypto.Keyring.master` (`internal/crypto/keyring.go:12`), populated at unseal, never persisted in plaintext. Sealed ⇒ every keyring op returns `crypto.ErrSealed`; the API middleware maps that to 503 (`internal/api/middleware.go:26`).
- **Project KEK**: one per project. Stored wrapped in `projects.wrapped_kek` (bytea) with `projects.kek_version integer NOT NULL DEFAULT 1` — the version column exists but is **never incremented today** (`migrations/000001_init.up.sql`; `internal/store/models.go:22`). Wrapped via `Keyring.WrapProjectKEK(kek, projectID)` / unwrapped via `Keyring.UnwrapProjectKEK(ct, projectID)` (`internal/crypto/keyring.go:53,63`), AAD = `crypto.ProjectKEKAAD(projectID)` (`internal/crypto/keys.go:55`) which binds the **project id only** (not the version).
- **DEK**: one per secret value-version. Stored in `secret_values(wrapped_dek, ciphertext, nonce, dek_key_version)` where `dek_key_version` records **which project-KEK version wrapped this DEK** (`migrations/000001_init.up.sql:56`; `internal/store/models.go:69`). DEK wrapped under the project KEK with AAD `crypto.DEKAAD(projectID, configID+"/"+key, valueVersion)` (`internal/crypto/keys.go:100`), which binds project/path/value-version — **not** the KEK version.
- **Write path** (`internal/secrets/secrets.go` `SetSecrets`): for each change, generates a fresh DEK, encrypts the value under it, stores `DEKKeyVersion: proj.KEKVersion` (the latest). So new writes always use the latest KEK automatically.
- **Read path** (`internal/secrets/secrets.go` `decryptValue`, ~line 226): unwraps the project KEK **once per config read** from `proj.WrappedKEK`, then per value unwraps the DEK and decrypts.
- No KEK/master rotation logic exists. Next migration number is **000015** (`migrations/` currently ends at `000014_sync_runs`).
- Store transaction helper `Store.withTx(ctx, func(pgx.Tx) error)` commits on nil / rolls back otherwise (`internal/store/store.go:42`); `execAffectingOne`, `mapError`, `ErrNotFound` available.

Because `DEKAAD` and `ProjectKEKAAD` bind identifiers but **not** the KEK version, re-wrapping a DEK (or storing a new KEK version) needs **no AAD change** — the version is tracked purely by DB columns.

## Core model & storage

The current wrapped KEK stays denormalized on the project row (`projects.wrapped_kek`, `projects.kek_version`) as the **latest** version — the hot read path is unchanged (zero extra queries when a DEK is at the latest version). One new table holds **only superseded** KEK versions that are still referenced by not-yet-rewrapped DEKs:

```sql
-- migrations/000015_project_kek_versions.up.sql
CREATE TABLE project_kek_versions (
  project_id  uuid    NOT NULL REFERENCES projects (id) ON DELETE CASCADE,
  version     integer NOT NULL,
  wrapped_kek bytea   NOT NULL,   -- KEK wrapped under the master, AAD = ProjectKEKAAD(project_id)
  created_at  timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (project_id, version)
);
```

```sql
-- migrations/000015_project_kek_versions.down.sql
DROP TABLE IF EXISTS project_kek_versions;
```

Invariant: `project_kek_versions` holds exactly the versions in `[oldest-still-referenced, kek_version)` that some DEK still points at. **In steady state (no rotation in flight, or after a full rewrap) it is empty.** No backfill is needed at migration time — existing projects have all DEKs at the current `kek_version`, so no superseded versions exist yet.

DEK → KEK-version resolution on read:
- `dek_key_version == projects.kek_version` → unwrap with `projects.wrapped_kek` (common path, already loaded on the project row).
- `dek_key_version < projects.kek_version` → unwrap with the row from `project_kek_versions` for that `(project_id, dek_key_version)`.

## Operations

### rotate (instant, one transaction)

`ProjectKeyService.Rotate(ctx, projectID) (newVersion int, err error)`:

1. `withTx`: `SELECT ... FROM projects WHERE id=$1 FOR UPDATE` → current `V = kek_version`, `W = wrapped_kek`. (404 if not found; the project must be live — reject if `deleted_at` is set.)
2. `INSERT INTO project_kek_versions (project_id, version, wrapped_kek) VALUES ($1, V, W)`. (Preserves the outgoing KEK so its DEKs stay readable.)
3. Generate a fresh 32-byte KEK (`crypto.GenerateKey`), wrap under the master: `Keyring.WrapProjectKEK(newKEK, projectID)` → `Wnew`. Zero `newKEK` after wrapping.
4. `UPDATE projects SET wrapped_kek=Wnew, kek_version=V+1, updated_at=now() WHERE id=$1`.
5. Commit. Return `V+1`.

Requires the server **unsealed** (needs the master to wrap the new KEK) → `ErrSealed` ⇒ 503. Emits audit `project.kek.rotate` (project id, `V → V+1`). Rotating twice simply creates two superseded versions; not idempotent by design (each call is a deliberate new version).

### rewrap (resumable, batched sweep)

`ProjectKeyService.Rewrap(ctx, projectID) (rewrapped int, retired []int, err error)`:

Target rows: **all** `secret_values` for the project (join `secret_values → configs → project_id`) with `dek_key_version < projects.kek_version`, **including value-versions belonging to soft-deleted configs/secrets** (version history and undelete must keep working after the old KEK versions are retired).

1. Unwrap the latest project KEK once (from `projects.wrapped_kek`), held in memory for the sweep; lazily unwrap each needed superseded KEK version from `project_kek_versions` (cache per version). All unwrapped KEKs zeroed at the end.
2. Keyset-paginate the target rows (`WHERE ... AND id > $cursor ORDER BY id LIMIT 200`). For each **batch**, in one `withTx`:
   - For each row: unwrap the DEK with the KEK version equal to its `dek_key_version` (AAD `DEKAAD(projectID, configID+"/"+key, valueVersion)`); re-wrap the DEK under the latest KEK with the **same AAD** and a **fresh random nonce**; `UPDATE secret_values SET wrapped_dek=<new>, dek_key_version=<latest> WHERE id=$id`. **The `ciphertext`/`nonce` value columns are never read or modified — no secret value is ever decrypted.** Zero the DEK immediately after re-wrapping.
   - Commit the batch.
3. After the sweep, delete newly-empty superseded versions: `DELETE FROM project_kek_versions v WHERE v.project_id=$1 AND NOT EXISTS (SELECT 1 FROM secret_values sv JOIN configs c ON c.id=sv.config_id WHERE c.project_id=$1 AND sv.dek_key_version = v.version)`. Return the deleted version numbers as `retired`.

Crash-safety / idempotency: each row's `dek_key_version` advances transactionally per batch, so a crash mid-sweep leaves some rows at old versions and re-running `rewrap` simply continues (it only selects rows still `< latest`). Running `rewrap` when nothing is pending is a no-op returning `rewrapped:0, remaining:0`. Requires unsealed. Emits audit `project.kek.rewrap` (project id, count, retired versions).

### read-path change (secrets service)

`decryptValue` currently unwraps `proj.WrappedKEK` once. Change: resolve the KEK **per value-version** by `dek_key_version`:
- Maintain a small per-read cache `map[int][]byte` of unwrapped KEKs.
- For `dek_key_version == proj.KEKVersion`, unwrap `proj.WrappedKEK` (as today).
- For a lower version, load `project_kek_versions.wrapped_kek` for that version (a single `SELECT`, only hit during a rotation's lazy window) and unwrap it.
- Zero all cached KEKs after the config read.

This keeps the steady-state read path identical (one KEK unwrap, no extra query); the extra query happens only while un-rewrapped DEKs exist.

## API / CLI (owner-only)

New routes under the existing project routes (`internal/api`), all gated on the **owner** role for `{id}` (deny by default; non-owner → 403; unauthenticated → 401), sealed → 503, unknown/soft-deleted project → 404:

- `POST /v1/projects/{id}/kek/rotate` → `200 {"kek_version": N}`
- `POST /v1/projects/{id}/kek/rewrap` → `200 {"rewrapped": n, "retired_versions": [..], "remaining": 0}` (synchronous, internally batched; `remaining` is always 0 on success — it exists so a future async variant can report partial progress)
- `GET /v1/projects/{id}/kek` → `200 {"current_version": N, "pending": [{"version": V, "dek_count": k}, ...]}` (status; `pending` lists superseded versions still referenced and how many DEKs remain)

CLI (cobra, same `janus` binary):
- `janus project rotate-kek <project>` → prints the new version.
- `janus project rewrap <project>` → prints rewrapped count + retired versions.
- `janus project kek-status <project>` → prints current version + pending versions/counts.

RBAC: reuse the existing project **owner** role check (the pattern used by other owner-scoped project mutations). No new permission constant.

## Crypto invariants (must hold; enforced by tests)

- AES-256-GCM for all wrap/unwrap; **fresh random nonce on every re-wrap** (never reuse); nonce stored alongside ciphertext (already the marshaled `Ciphertext` format).
- `ProjectKEKAAD(projectID)` and `DEKAAD(projectID, path, valueVersion)` are **unchanged** — the KEK version is tracked by DB columns, never by AAD. Re-wrapping a DEK uses the identical AAD to unwrap-old and wrap-new.
- **No secret value plaintext** is ever produced by `rotate` or `rewrap` — only 32-byte DEKs pass briefly through memory and are zeroed after use. The value `ciphertext`/`nonce` columns are never read during rewrap.
- Constant-time comparisons unchanged (AEAD tag verification is constant-time in stdlib).
- Zero plaintext key material in logs, errors, or audit. Audit records only: project id, old→new version, counts, retired version numbers.
- stdlib `crypto/*` + `golang.org/x/crypto` only; **no new crypto primitives, no third-party crypto** (reuses existing `Keyring.WrapProjectKEK`/`UnwrapProjectKEK`/`WrapKey`/`UnwrapKey`).

## Component layout

- `internal/store`: migration `000015`; new `ProjectKEKVersionRepo` (insert a superseded version, get wrapped KEK by `(project,version)`, list pending versions with per-version DEK counts, delete empty versions); new `ProjectRepo` methods to lock+read `(kek_version, wrapped_kek)` and update them in a caller-supplied tx; a batched target-row iterator over `secret_values` joined to `configs` for a project with `dek_key_version < latest` (keyset paginated, includes soft-deleted).
- New package `internal/projectkeys`: `ProjectKeyService` orchestrating `Rotate` / `Rewrap` / `Status` over the keyring + the new repos + the secrets store. (A dedicated package keeps `internal/crypto` focused on primitives; this service is orchestration, not new crypto.)
- `internal/secrets`: read-path change to resolve KEK per `dek_key_version` (per-read version→KEK cache).
- `internal/api`: three handlers + route registration + owner authz + audit.
- `cmd/janus`: three cobra verbs.

## Error handling

- Sealed → 503 for **all three** endpoints. Rotate and rewrap genuinely need the master (`ErrSealed`); the status GET is a pure read of version numbers and DEK counts, but it lives under the `/v1/projects/…` routes and is therefore short-circuited to 503 by the global `RequireUnsealed` middleware while sealed — consistent with every other project route, and it is not special-cased out of the seal gate.
- Unknown or soft-deleted project → `ErrNotFound` → 404.
- Non-owner principal → 403 (deny by default).
- A tampered/corrupt `wrapped_dek` (AEAD open fails) during rewrap → the batch tx rolls back and the operation returns an error naming the offending `secret_values.id` (never the key material); the sweep is resumable after the operator investigates.
- Rewrap is safe to retry after any failure (idempotent; only advances rows still at old versions).

## Testing

- **`internal/crypto` / service (100% branch per CLAUDE.md):** rotate creates a new version and preserves the old wrapped KEK; a secret written before rotation still decrypts after rotation (via the retained version) and again after rewrap (via the new version); rewrap advances every targeted row (across multiple configs and multiple value-versions incl. soft-deleted) and retires emptied versions; rewrap is a no-op when nothing pends; an interrupted sweep (inject an error mid-batch) leaves a consistent partial state and a re-run completes it; a tampered `wrapped_dek` makes rewrap fail without touching other rows; **nonce uniqueness** across re-wraps (assert the new wrapped-DEK nonce differs from the old); a **leak test** asserting `rewrap` never decrypts a value (e.g. drive a rewrap over a sentinel secret and assert the sentinel plaintext never appears in captured logs / the operation reads no value column — structurally, the rewrap SQL selects only `id, wrapped_dek, dek_key_version, key, value_version`, never `ciphertext`).
- **Store (testcontainers):** migration up/down; version insert/get/list-pending-with-counts/delete-empty; keyset batch iterator correctness incl. soft-deleted rows; `FOR UPDATE` serialization of concurrent rotate.
- **API (testcontainers):** owner can rotate/rewrap/status; non-owner → 403; sealed → 503 for rotate/rewrap; 404 for unknown/soft-deleted; rewrap idempotent over two calls; audit events emitted and value-free.
- **CLI:** the three verbs against a dev instance (smoke).

## Out of scope (this spec)

- **Master-key / root-KEK rotation** — separate follow-up spec (will add master-version stamping in `Ciphertext.KeyVersion` per the `keyring.go:50` TODO, re-wrap all master-wrapped blobs — projects/transit/rotation/sync/dynamic/auth/OIDC — under a dual-master crash-safe transition).
- Automatic/scheduled KEK rotation and background rewrap jobs (v1 is operator-triggered, synchronous).
- Re-wrap-on-read (rejected: turns reads into writes).
- Any change to `transit`, `rotation`, `sync`, `dynamic`, auth, or OIDC key handling (those are master-wrapped, addressed by the master-rotation spec).
