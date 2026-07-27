# Data model & versioning

**Package:** `internal/store`. **Status:** implemented — see
[`superpowers/specs/2026-07-03-store-layer-design.md`](superpowers/specs/2026-07-03-store-layer-design.md)
for the original design (schema has since grown well past that snapshot; see
migrations below for current state). The schema, migrations, repositories, and
two-level versioning (batched saves, history, diff, rollback) are built and
tested against real Postgres. Config inheritance and read-time secret
references are implemented as a resolution layer over these reads — see
[references.md](references.md). This document explains the hierarchy and the
two-level versioning scheme.

The store is **crypto-blind**: it persists and returns opaque encrypted bytes
(`wrapped_dek`, `ciphertext`, `nonce`, `wrapped_kek`) and never holds a key or
plaintext. All encryption happens in a layer above it.

## Hierarchy

Four levels:

```
Project            e.g. "acme-web"          — owns a wrapped project KEK
  └─ Environment   e.g. "prod" / "staging"  — user-definable
       └─ Config   e.g. "prod" (root) or "prod-ci" (branch)
            └─ Secrets   KEY = value         — versioned key/value pairs
```

Projects, environments, and configs are addressed by human `slug`/`name` (unique
within their parent, scoped to non-deleted rows) and carry a `uuid` primary key.
Configs have a nullable `inherits_from` column: a root config plus branch
configs within the same environment, resolved read-time (child wins per key)
— see [references.md](references.md).

### Secret keys

A secret `key` is filename-style: `[A-Za-z0-9._-]+`, 1–255 chars, and never
`.`/`..` or a path separator (`internal/secrets` `validateKey`). This is a
strict superset of a shell-safe env-var identifier, so a key may be a dotted
filename (e.g. `config.prod.json`) to support secrets that are files rather
than env vars. Consequences:

- `janus run` and `.env` export skip keys that aren't valid env-var
  identifiers (with a warning) — only `secrets download --format files`
  materializes every key, one file per key, to `<dir>/<key>` (traversal-guarded
  by the same charset rule).
- Dotted/filename-style keys are **not** `${KEY}`-referenceable and are skipped
  by the GitHub Actions sync integration (both require env-var-shaped names).

### Typed secrets

Each `secret_values` row carries a `type` (migration `000022`): `value`
(default, stored as `'string'`), `password`, `json`, `ssh_key`, `certificate`,
or `note`. This is purely a **display/handling hint** for the CLI and web
editor (multiline editors, password generation, non-blocking JSON/PEM
validation) — it has no effect on storage or encryption; the value is still an
opaque encrypted blob regardless of type.

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
  table **is** the per-key history. Rows are never updated, and are only ever
  removed by the explicit, owner-gated retention prune described below — never
  as a side effect of an ordinary write.
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

### Retention: pruning history (roadmap 8.2)

Because history is append-only, a long-lived instance accumulates every value
every key has ever held. `POST /v1/configs/{cid}/versions/prune` (owner-only,
audited, **preview by default**, disabled unless asked for) bounds that growth.

**It prunes at the config-version granularity, never the value-version
granularity, and that is load-bearing.** Migration `000005` declares

```sql
config_version_entries.secret_value_id → secret_values (id) ON DELETE CASCADE
```

so deleting a `secret_values` row **silently deletes the manifest entries that
point at it**. A naive "delete value versions older than N days" would strip
keys out of old config versions with no error anywhere, and a rollback to one of
them would restore an *incomplete* config. The prune therefore:

1. deletes whole `config_versions` rows (their manifest entries go with them via
   the `config_version_id` cascade), then
2. garbage-collects `secret_values` rows of that config that no surviving
   `config_version_entries` row references
   (migration `000043` adds the partial index on `secret_value_id` this needs).

This preserves the invariant **every retained config version is fully
restorable**, which the prune transaction re-asserts by counting the surviving
versions' live manifest entries before and after the deletes and rolling back on
any mismatch. Guards (all fail-closed): the latest version is never pruned; a
soft-deleted config is refused; a config with a pending/in-flight edit request is
refused outright; and a version a pending promotion request names as its source
is pinned. Retention floors live in `config_version_retention` (per config,
migration `000043`) and `JANUS_SECRET_RETAIN_MIN_VERSIONS` /
`JANUS_SECRET_RETAIN_MIN_DAYS` (instance-wide); the effective floor is the
**strictest** of those and the request, so an override can only ever retain
more. See `docs/operations.md` → *Value-version retention*.

## Deletion semantics

Two distinct notions, both required by the project spec:

- **Secret delete** = a tombstone entry in a new config version. The key
  disappears from the resolved state but its history remains; "undelete" is just
  a later save that sets the key again. This is the immutable-versioning way.
- **Entity soft-delete** (project / environment / config) = a nullable
  `deleted_at` timestamp. Soft-deleted entities are hidden from reads and lists
  and can be undeleted via a `.../restore` endpoint. `GET /v1/trash` lists an
  actor's soft-deleted entities across the hierarchy (per-item authorized, no
  new table — it queries the existing `deleted_at` columns). **Hard destroy**
  is a separate, explicit operation that actually removes rows.

Migration `000005_cascade_destroy` makes hard destroy transitive: every
*ownership* foreign key (environment→project, config→environment,
config_version→config, secret_value→config, and the config_version_entries
links) is `ON DELETE CASCADE`, so destroying a project (or environment) removes
its whole subtree in one statement. The `configs.inherits_from` reference is
deliberately left `NO ACTION`: a config that is still an inheritance base for a
branch config cannot be destroyed (the FK violation surfaces as
`store.ErrParentNotFound` → `409`), so an inheritance relationship is never
silently broken by a destroy.

## Concurrency & atomicity

A batched save runs in a single transaction. To keep version numbers contiguous
under concurrent saves to the same config, the save transaction takes
`SELECT ... FOR UPDATE` on the `configs` row — this serializes saves per-config
and doubles as an existence/liveness check (a soft-deleted or missing config is
rejected before any write). A `UNIQUE (config_id, version)` constraint is the
correctness backstop. N concurrent saves therefore produce versions `1..N` with
no duplicates.

## Identity & access tables

Later milestones added identity, authorization, and audit tables alongside the
secret hierarchy (migrations `000002_auth`, `000003_rbac`, `000004_audit`, and
later `000045_groups`). They
hold no secret values and are outside the crypto-blind ciphertext path.

- **`users`** — `id`, `email` (unique), `password_hash` (Argon2id PHC string),
  `created_at`, `updated_at`, `disabled_at` (nullable; set to soft-disable a
  login). No plaintext password is ever stored.
- **`sessions`** — opaque server-side sessions for the UI cookie; the raw cookie
  is never stored (only its HMAC), and expiry is swept on boot.
- **`service_tokens`** — scoped machine credentials; only the token HMAC is
  stored (never the raw `janus_svc_…`), with `scope_kind`/`scope_id`, `access`,
  and optional `expires_at`/`revoked_at`.
- **`auth_config`** — the master-key-wrapped token-HMAC key, materialized at
  first unseal (so a DB dump is not verifiable offline).
- **`role_bindings`** — the RBAC store: `subject_user_id`, `scope_level`
  (`instance`/`project`/`environment`), nullable `project_id`/`environment_id`,
  `role` (`viewer`/`developer`/`admin`/`owner`), `created_by`. A CHECK enforces
  that exactly the right scope-id column is set per level, and a COALESCE-based
  unique index makes each (subject, scope) binding singular (upsert-in-place).
- **`groups` / `group_members` / `group_role_bindings`** (migration `000045`) —
  a binding may name a **group** instead of a user. A group is `kind='oidc'`
  (carrying a `claim_value` matched against the IdP's group claim, membership
  refreshed at each login) or `kind='local'` (an explicit admin-managed list),
  and a CHECK ties `claim_value` to exactly the `oidc` kind. Two schema facts do
  the security work rather than handler code: `group_members` carries a
  denormalised `group_kind` and a **composite FK** to `groups(id, kind)`, which
  makes a hand-added member of an IdP-fed group *unrepresentable* — the
  invariant behind "access granted via an IdP group is fully described by the
  IdP"; and `group_role_bindings.role` omits `'owner'`, so every instance owner
  remains a direct `role_bindings` row and the never-lock-out counter is
  unaffected. Scope shape and the COALESCE unique index mirror `role_bindings`
  exactly, so group bindings inherit the same cascade when a project or
  environment is deleted. The authorization engine reads them as ordinary
  bindings stamped with their origin, so group and direct bindings union under
  one rule with no precedence.
- **`audit_events`** — the append-only, hash-chained audit log: `seq`
  (chain position, PK), `occurred_at`, actor (`actor_kind`/`actor_id`/
  `actor_name`), `action`, `resource`, `detail`, `result`/`result_code`, `ip`,
  and `prev_hash`/`hash` (SHA-256 chain). No update or delete path; the store
  exposes only append, ordered iteration (verify), and filtered list (export).
  Never holds a secret value — resource/detail carry paths and non-secret
  specifics only.

## Read-time resolution (implemented)

The store milestone builds the secret-hierarchy schema and repositories above.
Two read-time *resolution* features compose over these reads (in
`internal/resolve`, documented in [references.md](references.md)):

- **Config inheritance** — root config + branch configs within an environment
  (child wins per key); the `inherits_from` column is now resolved.
- **Secret references** — `${projects.<project>.<env>.<config>.KEY}` (and local
  `${KEY}`) resolved at read time, transitively, with cycle detection and strict
  per-target authorization.

Reveals resolve by default; `?raw=true` returns stored values verbatim.

(Encryption orchestration — the layer that holds the unsealed keyring and turns
plaintext into the ciphertext this store persists — shipped in `internal/secrets`.)
