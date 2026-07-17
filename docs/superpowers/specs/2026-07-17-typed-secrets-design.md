# Typed secrets — design

_Date: 2026-07-17. Adds a lightweight per-secret **type** (value/password/json/ssh_key/certificate/note) that drives editor rendering, optional validation, and a per-type icon. Files/binary are explicitly **out of scope** (a possible separate future spec)._

## Problem

Every secret value in Janus is edited as a single-line `<input>` (`web/src/secrets/SecretTable.tsx:154`) and stored as opaque encrypted bytes with no notion of what kind of secret it is. In practice a config holds different kinds of material — plain values, passwords, JSON blobs, SSH private keys, PEM certificates, freeform notes — and they want different handling:

- Multi-line material (JSON, SSH keys, PEM, notes) needs a **bigger, monospace, multi-line box**, not a one-line input.
- JSON benefits from **validation + pretty-print**; passwords benefit from a **generator**.
- A visible **type label/icon** makes a config scannable ("this one is an SSH key").

## Key reframe (scope guardrail)

In Janus, **type is a display/handling hint, not a storage or crypto change.** All these types are strings; the envelope encryption (`internal/crypto`, `internal/secrets`) stores arbitrary bytes and is untouched. Multi-line text (incl. SSH keys and PEM) already flows through `janus run` env injection, `.env`/JSON download, and config-version diff unchanged — env vars and JSON hold newlines fine. **Only files/binary would break the string model, and files are out of scope here.**

## Verified starting facts

- `secret_values` (migration 000001) has **no `type` column**: `id, config_id, key, value_version, wrapped_dek, ciphertext, nonce, dek_key_version, created_at`. Latest migration is 000021, so this adds **000022**.
- `MaskedSecret` (`web/src/lib/endpoints.ts`) = `{ value_version, created_at, origin }` — no type.
- The write path is `secrets.Service.SetSecrets(configID, []SecretChange{Key,Value,Delete}, message, actor)`; masked list is `GET /v1/configs/{cid}/secrets`.
- The editor is `web/src/secrets/`: `SecretEditor.tsx` (controller + dirty buffer), `SecretTable.tsx` (row rendering incl. the inline value `<input>`), `rowState.ts` / `dirty.ts` (dirty-state model), `ImportEnvDialog`/`importClassify` (.env import), `ReviewDiffDialog` (value-free diff).
- Env→env **promotion** re-encrypts via `SetSecrets` ([[env-promotion-progress]]), so it is a type carry-through touch-point.

## Architecture decision — where `type` lives

**Column on `secret_values`, per-version, `NOT NULL DEFAULT 'string'`.** No new table; reuses the existing write path. The editor sends the type with each save; the **latest version's type is authoritative**. Changing a type is a normal save (a new version). Rejected alternatives: a separate `secret_meta(config_id,key)` table (adds a join to every read, makes per-key metadata first-class when nothing else needs it); encoding type inside the value (pollutes ciphertext, breaks run/download).

## Section A — Type registry (shared contract)

A capability registry, authored once per side (Go constant set for validation; a TS table for rendering), kept in sync by tests. Types and capabilities:

| type | multiline | monospace | validate (warn-only) | extra affordance | icon (lucide) |
|------|-----------|-----------|----------------------|------------------|---------------|
| `string` | no | yes | — | — | Key |
| `password` | no | yes | — | generate | KeyRound |
| `json` | yes | yes | `JSON.parse` | pretty-print | Braces |
| `ssh_key` | yes | yes | — | — | TerminalSquare |
| `certificate` | yes | yes | PEM `BEGIN/END` present | — | BadgeCheck |
| `note` | yes | yes | — | — | FileText |

- The **allowed set** is the single source of truth for validation. Unknown or absent type → treated as `string` (forward-compatible; never errors on read).
- Validation is **warn-only, never blocks a save** (secrets are freeform; we surface a non-blocking badge/hint, e.g. "not valid JSON").

## Section B — Backend (thin)

- **Migration 000022** (`up`): `ALTER TABLE secret_values ADD COLUMN type text NOT NULL DEFAULT 'string';`. `down`: drop the column. Existing rows default to `string`. No index needed (type is read with the row, never filtered in SQL).
- **Store**: `SecretValue` / `EncryptedValue` gain `Type`; the insert in the batch-save path writes it; reads scan it. Latest-version read returns the type.
- **Service**: `SecretChange` gains `Type string` (empty → `"string"`). `SetSecrets` validates `Type` against the allowed set → on unknown, return a validation error (mapped to 400). Type is carried on the written `secret_value` row. `RevealConfig`/`GetLatest` surface the type. The **promotion** re-encrypt path copies the source secret's type into the target write so a promoted secret keeps its type.
- **API**: `MaskedSecret` (masked list) and the reveal/get responses gain `type`. The batch-write request per-key gains an optional `type`. Type is **metadata, not secret material** — safe in masked views; audit may include it in detail; values are still never logged.
- **CLI**: `janus secrets set --type <t>` (default `string`, validated); `janus secrets list` shows a `TYPE` column. `janus run` / `secrets download` are unchanged (all values are strings).
- **OpenAPI**: document the new `type` field on the secret schemas + the write request; drift test stays green (no new routes).

## Section C — Editor (frontend, the bulk)

- **New `web/src/secrets/secretTypes.ts`**: the capability registry — `SECRET_TYPES: Record<Type, { label; icon; multiline; monospace; validate?(v): string | null; generate?: boolean }>` + a `normalizeType(t?)` that maps unknown/absent → `string`.
- **`SecretTable` value cell**: render a single-line `<input>` for non-multiline types and an **auto-growing monospace `<textarea>`** for multiline types (`json`/`ssh_key`/`certificate`/`note`) — the bigger box. A small **type dropdown** per row (icon + label) sets/changes the type.
- **Dirty model**: `dirty.ts` buffer entries and `rowState.ts` track `type` alongside `value`, so a **type-only change** is a real diff and enables Save. `ReviewDiffDialog` stays value-free but shows `type: string → json` when it changes.
- **Per-type affordances**: `password` → a **Generate** button (length + charset, inserts into the buffer, never auto-reveals beyond the existing on-demand reveal model); `json` → inline **validate** (non-blocking warn badge) + **pretty-print** action; `certificate` → PEM-shape warn badge. All ephemeral/local; no new reveal semantics.
- **Icons in masked views**: the row and the masked list show the type icon/label (from `MaskedSecret.type`).
- **Import .env** → all imported keys are `string` (unchanged classify), type editable afterward.
- Token classes only; correct in both themes (`npm run smoke`); monospace stays reserved for keys/values.

## Data flow

Save: editor row `{key, value, type}` → batch-write request `{key, value, type}` → `SetSecrets` validates type, encrypts value (crypto unchanged), writes `secret_value` row incl. `type`. Read: masked list / reveal returns `type` per key → editor renders per the registry. Promotion: source type read → carried into the target `SetSecrets`.

Because `type` lives on the value row, a **type-only change** (same value, new type) is submitted as a normal change for that key and produces a new encrypted `secret_value` version — there is no separate "set type" path. This is the same cost as any edit and keeps the write path single.

## Error handling

- Unknown type on write → 400 validation error (defensive; the UI only ever sends allowed types).
- Invalid JSON / non-PEM cert → **non-blocking** warn badge in the editor; save still allowed.
- Absent type anywhere (old rows, old clients) → normalized to `string`.

## Testing

- **Backend**: migration up/down; type persisted + returned by masked/reveal; unknown-type write rejected; type carried through an edit that doesn't touch type; type carried through **promotion**; leak test unaffected (type is not a value); OpenAPI drift green.
- **Web**: `secretTypes` registry (normalize unknown→string, validators); SecretTable renders textarea for multiline types and input otherwise; type dropdown change marks dirty; JSON validate badge; password generate inserts a value; review-diff shows type change; dual-theme smoke; no-raw-palette guard.

## Non-goals

- **Files / binary secrets** (size caps, base64 API, download UX, `janus run` materialization) — explicitly excluded; possible separate future spec.
- Type-based access control, per-type rotation, or schema enforcement (validation is warn-only).
- Changing crypto/storage of the value itself, or `run`/download semantics.

## Rollout

Migration 000022 (additive, backward-compatible default). Frontend-heavy. After merge: rebuild dev containers (`docker compose up -d --build`) + `dev-unseal.sh`, container bumps to migration v22.
