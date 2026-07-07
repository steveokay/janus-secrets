# §3 Secret Editor Redesign — Design Spec

**Status:** Approved (2026-07-07). Redesign of the flagship secret-editor screen to match mockup **§06** (`docs/design/ui-redesign-mockup.html`), in the dark redesign system. Frontend-only; no new `/v1` endpoints.

## Scope (locked: full mockup §06)

In: table layout · origin pills · per-row icon actions (reveal/copy/edit/discard/revert/restore) · reveal+copy on hover · colored left rails + change pills for pending edits · removed-row strike · Ver column · **dirty-bar** (summary + Review diff + Discard + Save as vN) · **key filter** · **Import .env** modal · **Review-diff** modal · inherited→override on edit.

Deferred to a later P2 polish pass (NOT in the mockup): reveal-all toggle, auto-re-mask on blur/idle, `⌘S`/row keyboard navigation.

## Visual authority

Mockup §06 is canonical for look. Key CSS cues (translate to tokens): table `bg-card border-line rounded-card`; grid columns `Key 1.3fr · Value 1.5fr · Origin 108px · Ver 56px · Actions 92px`; sticky header row `bg-page` (dark `#101017`) with `text-faint` uppercase micro-labels; rails `w-[3px]` absolute-left (add=`bg-success`, edit=`bg-warning`, remove=`bg-danger`); change pills (`Pill` won't fit inline — use small `text-[10px] uppercase` chips: add=success, edit=warning, remove=danger); reveal/copy icons `opacity-0` → `group-hover:opacity-100`. Origin pills use the kit `<Pill>`: own=`success`, inherited=`muted`, overridden=`brand`. Token classes ONLY (gates `no-raw-palette`, `dark-aa`; `text-brand-deep` banned).

## Component breakdown

- `web/src/secrets/rowState.ts` (pure) — display-state derivation + `.env` parser.
- `web/src/secrets/SecretTable.tsx` — the grid table + rows.
- `web/src/secrets/EditorToolbar.tsx` — key filter + Import .env + History triggers.
- `web/src/secrets/DirtyBar.tsx` — bottom pending-changes bar.
- `web/src/secrets/ReviewDiffDialog.tsx` — pre-save change list.
- `web/src/secrets/ImportEnvDialog.tsx` — `.env` bulk-paste modal.
- `web/src/secrets/SecretEditor.tsx` — orchestrates queries/buffer/mutations and composes the above (existing `dirty.ts`, `VersionHistory` sheet, `AddKeyRow` retained).

## Row display semantics (the subtle part)

Change-type is computed against the **displayed masked rows**, not `original` (own raw values), so editing an *inherited* row reads as an override, not an add:

`rowState(key, masked, buffer, original)` returns:
- `change`: `'added'` (key ∉ masked, buffer sets a value) · `'edited'` (key ∈ masked, buffer sets a value that differs from `original`) · `'removed'` (key ∈ masked, buffer value === null) · `null` (no effective change — reuse `dirty.effective` semantics: a buffer value equal to `original[key]` is NOT a change).
- `origin`: server origin, except an **edited inherited** row displays `'overridden'`. own→own, overridden→overridden.
- `existing`: `key in masked`.

Per-row **actions** by state: own/overridden (not editing) → edit + remove; inherited (not editing) → edit (→ creates override) ; editing → the inline value input; added → discard (drops the buffer key); edited → revert (drops the buffer key) + remove; removed → restore (drops the buffer key). Reveal + copy show on hover for any existing row not currently being edited.

**Ver column:** existing row → `v{value_version}`; added row → `—`. (The mockup's `v6→v7` flourish needs the post-save config version, unknown pre-save, so we show the current per-secret value version only — documented deviation.)

## Interaction decisions (mockup-silent → decided)

- **Import .env:** modal with a `<textarea>`; `parseDotenv` accepts `KEY=VALUE` lines, ignores blank lines and `#` comments, strips one layer of matching single/double quotes, validates keys against `^[A-Za-z_][A-Za-z0-9_]*$`; invalid/blank lines are counted as skipped. Parsed pairs are applied into the **dirty buffer** (`setValue` per key — existing keys become edits, new keys adds); nothing is persisted until Save. Modal reports "N applied · M skipped".
- **Review diff:** modal listing pending changes as **key name + change type only** (added/changed/removed), grouped, from `summarize`/`rowState` — **no values** (values remain visible in-row on reveal; names-only keeps this surface value-free). Save button also lives here.
- **Copy:** copy reveals the value first if not already revealed (an audited read) then writes to the clipboard — copying a secret is a read. Guarded `navigator.clipboard` with a toast confirmation.

## Security / invariants (unchanged, must hold)

- Masked list load performs **no reveal** (no audited read on mount). Reveal and copy are the only value reads; each hits the audited `GET …/secrets/{key}`.
- Revealed plaintext lives in ephemeral component state only — never the query cache, never logs. `.env` import values live only in the dirty buffer (component state). Review-diff shows no values.
- Save batches the whole buffer into one `PUT …/secrets` = one config version (unchanged).

## Testing

Preserve the existing `SecretEditor.test.tsx` behaviors (stable aria-labels: `reveal {key}`, `new key`/`new value`, History; origin text; "No secrets yet"; no-reveal-on-load). New tests: rowState + parseDotenv units; pending edit shows rail + change pill; inherited edit → overridden pill; removed row struck + restore; copy triggers an audited reveal + toast; filter narrows rows; Import .env applies pairs to the buffer (applied/skipped counts); Review-diff lists names+types (no values); dirty-bar Save/Discard. `no-raw-palette` + `dark-aa` gates green; `npm run smoke` both themes.

## Out of scope

Backend changes; reveal-all/auto-remask/keyboard (P2 follow-up); the `v6→v7` per-key version arrow.
