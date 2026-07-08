# On-Demand Editor Reveal — Design

Security fix for the secret editor. **Frontend-only.** Today `SecretEditor`
fires a `raw` query on mount (`GET …/secrets?reveal=true&raw=true`) that returns
**every** secret's plaintext, lands it in the TanStack **Query cache**, and emits
one audited `secret.reveal` per editor open. This contradicts the project posture
(plaintext ephemeral-only, never in the Query cache, reveal = explicit audited
action) and makes the mask / reveal-on-hover UI cosmetic. Rework so values load
**on demand**, into ephemeral state, only on explicit user intent.

## Goal

- **Mount reveals nothing.** Opening the editor loads only masked metadata + the
  config version — no secret values, no `secret.reveal` audit event, nothing in
  the Query cache.
- Values are fetched **on demand** (per-key eye/copy/edit, or bulk reveal-all),
  each an explicit audited read, into **ephemeral React state only**.

## Data model (two ephemeral maps, both raw)

The editor works in **raw** stored values throughout (what you edit and save is
the raw string, including unresolved `${...}` references — showing the resolved
target while editing would be misleading). We split the old dual role of the
mount `raw` map into two ephemeral maps:

1. **`revealed: Record<string,string>`** — values fetched for **viewing** (eye,
   reveal-all, copy). Drives display unmasking. **Auto-re-masks** (cleared) on
   window blur + 60s idle, exactly as today. (Renamed conceptually from the
   PR #44 `revealed`; now holds RAW values, not resolved.)
2. **`original: Record<string,string>`** — the raw stored value of a key captured
   when it enters **edit** mode. Needed for the diff engine (`rowState` /
   `dirty.ts`) and to prefill the edit input. **Persists while dirty** (must NOT
   be cleared by auto-re-mask, or pending edits would mis-diff). Cleared on
   save / discard, and per-key on revert.

Nothing is stored in the Query cache. The `['config', cid, 'raw']` `useQuery` is
removed entirely.

## Endpoints

- **New** `endpoints.revealKeyRaw(cid, key)` → `GET /v1/configs/{cid}/secrets/{key}?raw=true`
  → `{ key: string; value: string }`. Backend already supports `?raw=true` on the
  single-key GET (`secrets_handlers.go:116-127`, audited `secret.reveal` detail
  `"raw"`). Returns the RAW stored value.
- **Bulk reveal-all** uses the existing `endpoints.rawConfig(cid)`
  (`?reveal=true&raw=true` → `{ version, secrets }`, one audited event) — RAW,
  consistent with per-key. (We ignore its `version`; see below.)
- **Version** comes from the existing `endpoints.listVersions(cid)` (config:read,
  **not** audited, key-names-only, no values): `version = versions[0]?.version ?? 0`.
  A fresh config with no versions → `0` → "Save as v1". This replaces the version
  previously read from the mount `raw` response — no backend change required.
- **Remove** the now-orphaned `endpoints.revealAll` (resolved bulk, added in
  PR #44) **iff** nothing else references it (grep first; update any test).

## Editor behavior

- **Mount:** `masked` query (metadata) + `versions` query (for the version number).
  Both value-free. No `raw` query.
- **`reveal(key)`** (eye): `revealed[key] ??= await revealKeyRaw(cid,key).value`.
- **`revealAll()`**: `setRevealed((await rawConfig(cid)).secrets)` — one audited
  bulk reveal into `revealed`.
- **`hideAll()` / auto-re-mask:** clear `revealed` only. `original` untouched.
- **`copy(key)`**: ensure raw fetched into `revealed` (audited), copy that.
- **`edit(key)`** becomes async: ensure the key's raw value is known —
  `original[key] ??= revealed[key] ?? await revealKeyRaw(cid,key).value` — **then**
  set `editing[key]=true`. (Editing a secret means seeing its value ⇒ a legitimate
  audited reveal. Reuse an already-revealed value to avoid a duplicate fetch.)
  Added (new) keys have no server value → skip the fetch.
- **`undo(key)`**: drop the buffer entry (as today) and drop `original[key]`.
- **`save` onSuccess / `discard`:** clear `buffer`, `editing`, `revealed`, **and
  `original`** (stale after a save; a fresh edit re-fetches).

## Rendering (props rewired, components largely unchanged)

`SecretTable` keeps its `original` and `revealed` props; only their **sources**
change:
- **Display** (`SecretTable.tsx:108`): `key in revealed ? revealed[key] : '••••'` —
  unchanged, now raw values.
- **Edit prefill** (`SecretTable.tsx:101`): `key in buffer ? buffer[key].value :
  original[key] ?? ''` — unchanged; `original[key]` is guaranteed present because
  `edit()` fetches it before entering edit mode.
- **`rowState` / `dirty.ts`** diff against `original` (the edit-originals map).
  Every edited existing key has its original (fetched on edit), so `changed`
  classification stays correct. `ReviewDiffDialog` is value-free (key names only)
  and needs no change.

## Accepted imprecision (YAGNI)

**Import .env** stages buffer values without fetching originals, so an imported
key whose value equals the stored value is classified `edited` (and saved as a
new identical version) rather than a no-op. Rare, harmless (a redundant version),
and avoids a bulk reveal on import. Documented, not fixed.

## Security invariants (must hold; verified in review)

1. Mount issues **no** `?reveal=` request and writes **nothing** to the Query
   cache — assert via msw (no reveal call on render) + that `revealed`/`original`
   start empty.
2. Revealed/edited plaintext lives **only** in `revealed`/`original` component
   state — never `useQuery`/`useMutation` cache, never logged, never in a URL /
   toast / tooltip.
3. Auto-re-mask clears `revealed`; pending edits (`original`, `buffer`) survive a
   blur so the diff/save stay correct.
4. Each on-demand fetch (`revealKeyRaw`, `rawConfig`) is one audited reveal on an
   explicit user action; reveal-all is exactly one request.

## Testing

- **Mount = no reveal:** render the editor; assert no `?reveal=…`/`?raw=…` request
  fired and values render masked (`••••`).
- **Reveal on demand:** clicking the eye fetches that one key's raw value (one
  request) and unmasks only it.
- **Edit fetches original:** clicking edit on a masked existing key fetches its raw
  value, prefills the input with it, and a same-value edit is a no-op (not dirty);
  a real edit is dirty and saves the batch.
- **Reveal-all:** one bulk raw request populates all rows; Hide all / blur re-masks;
  a pending edit survives blur (still dirty, original intact).
- **Version:** dirty bar shows `Save as v{latest+1}` sourced from `listVersions`.
- Gates: `no-raw-palette`, `typecheck`, full `vitest`, `build`, dual-theme `smoke`.

## Task decomposition (TDD, subagent-driven)

1. `endpoints.revealKeyRaw` (+ remove orphaned `revealAll` if unused) + endpoint test.
2. Editor rework: drop `raw` query; add `versions` query for version; `revealed`
   (viewing, re-maskable, raw) + `original` (edit, persists-while-dirty) maps;
   async `edit`/`reveal`/`copy`/`revealAll`/`undo`; wire props. Update
   `SecretEditor.test.tsx` + `SecretEditor.save.test.tsx`.
3. Verify `SecretTable`/`rowState`/`DirtyBar`/`ReviewDiffDialog` render correctly
   against the new sources (mostly prop-source changes; fix any test fixture that
   relied on mount-time full `original`).
