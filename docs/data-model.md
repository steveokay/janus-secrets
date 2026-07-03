# Data model & versioning

**Package:** `internal/store`. **Status:** implemented (foundation + core CRUD)
— see [`superpowers/specs/2026-07-03-store-layer-design.md`](superpowers/specs/2026-07-03-store-layer-design.md).
The schema, migrations, repositories, and two-level versioning (batched saves,
history, diff, rollback) are built and tested against real Postgres. Config
inheritance and secret references remain deferred (see the end of this doc).
This document explains the hierarchy and the two-level versioning scheme.

The store is **crypto-blind**: it persists and returns opaque encrypted bytes
(`wrapped_dek`, `ciphertext`, `nonce`, `wrapped_kek`) and never holds a key or
plaintext. All encryption happens in a layer above it.

## Hierarchy

Doppler-style, four levels:

```
Project            e.g. "acme-web"          — owns a wrapped project KEK
  └─ Environment   e.g. "prod" / "staging"  — user-definable
       └─ Config   e.g. "prod" (root) or "prod-ci" (branch)
            └─ Secrets   KEY = value         — versioned key/value pairs
```

Projects, environments, and configs are addressed by human `slug`/`name` (unique
within their parent, scoped to non-deleted rows) and carry a `uuid` primary key.
Configs have a nullable `inherits_from` column reserved for the (not-yet-built)
root/branch inheritance feature.

## Two-level versioning

Every change is versioned at **two** levels, because operators need both "what
did this whole config look like at release time?" and "when did *this one key*
change?".

1. **Config version** — each *save* creates one immutable config version (`v1`,
   `v2`, …). A save may batch edits to many keys; the config version is the unit
   of **diff** and **rollback**. This is what the UI commits when you click
   "Save as vN".
2. **Secret value version** — each key additionally has its own append-only
   value history, for per-key trace ("show me every value DB_URL has had").

### How it's stored: manifest of value pointers

Rather than snapshotting every value on every save, Janus stores versions like a
git tree — a config version is a **manifest** pointing at immutable value rows:

- **`secret_values`** — append-only. One row per *distinct* value:
  `(config_id, key, value_version, wrapped_dek, ciphertext, nonce, …)`. This
  table **is** the per-key history. Rows are never updated or deleted.
- **`config_versions`** — one row per save: `(config_id, version, message,
  created_by, created_at)`. Immutable.
- **`config_version_entries`** — the manifest: for each config version, one row
  per key pointing at the `secret_value` in effect, or a **tombstone** marking
  the key deleted at that version.

**Consequences of this design:**

- **Dedup.** A save that changes 1 of 20 keys writes *one* new `secret_values`
  row; the new manifest reuses the other 19 pointers. Unchanged (encrypted)
  values are never copied.
- **Diff(vA, vB)** is a set-compare of two manifests by `(key,
  secret_value_id)` → added / changed / removed keys.
- **Rollback to vN** creates a *new* config version whose manifest copies vN's
  entries (reusing the same value rows — no re-encryption).
- **Per-key history** is just `secret_values` filtered by key, ordered by
  `value_version`.

### Reading current state

A read defaults to the latest config version: take `MAX(version)` for the
config, then join its manifest entries to `secret_values`, skipping tombstones.

```sql
SELECT e.key, sv.*
FROM config_version_entries e
JOIN secret_values sv ON sv.id = e.secret_value_id
WHERE e.config_version_id = $latest AND NOT e.tombstone;
```

## Deletion semantics

Two distinct notions, both required by the project spec:

- **Secret delete** = a tombstone entry in a new config version. The key
  disappears from the resolved state but its history remains; "undelete" is just
  a later save that sets the key again. This is the immutable-versioning way.
- **Entity soft-delete** (project / environment / config) = a nullable
  `deleted_at` timestamp. Soft-deleted entities are hidden from reads and lists
  and can be undeleted. **Hard destroy** is a separate, explicit operation that
  actually removes rows.

## Concurrency & atomicity

A batched save runs in a single transaction. To keep version numbers contiguous
under concurrent saves to the same config, the save transaction takes
`SELECT ... FOR UPDATE` on the `configs` row — this serializes saves per-config
and doubles as an existence/liveness check (a soft-deleted or missing config is
rejected before any write). A `UNIQUE (config_id, version)` constraint is the
correctness backstop. N concurrent saves therefore produce versions `1..N` with
no duplicates.

## What's deferred

The store milestone builds the schema and repositories above. These read-time
*resolution* features are separate, later specs:

- **Config inheritance** — root config + branch configs within an environment
  (the `inherits_from` column exists but is unresolved).
- **Secret references** — `${projects.other.prod.KEY}` resolved at read time,
  with cycle-checking.
- **Encryption orchestration** — the layer that holds the unsealed keyring and
  turns plaintext into the ciphertext this store persists.
