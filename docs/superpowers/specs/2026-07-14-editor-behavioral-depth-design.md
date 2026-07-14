# Secret editor behavioral depth — design

**Date:** 2026-07-14
**Scope area:** `web/src/secrets/` (SPA secret editor — the flagship screen)
**Source item:** `gaps.md` §2.1 (secret editor, MED-HIGH). Companion: folds in §1.9 (export-config) via bulk Copy/Download `.env`.
**Status:** Approved (design brainstormed + accepted 2026-07-14).

## Goal

Turn the N3-restyled secret editor from a single-row-at-a-time editor into a keyboard-driven, bulk-capable control surface, **without changing** the crypto/audit/data model underneath. Four headline features + two small P2 wins:

1. Column sorting (Key / Origin / Updated / Version).
2. Multi-row selection + bulk operations (Reveal, Copy/Download `.env`, Delete).
3. Keyboard row navigation (arrows + vim keys).
4. `.env` import preview (classified, value-free).
5. P2: "changed only" toggle.
6. P2: filter zero-match empty state.

## Non-goals

- No backend changes. All endpoints already exist (`maskedSecrets`, `revealKeyRaw`, `rawConfig`, `saveSecrets`).
- No change to the dirty-buffer / config-version model, the on-demand audited reveal flow, or the value-free review-diff.
- Per-row revert is **already shipped** (`undo`) — gaps.md is stale on that bullet; not re-implemented.
- "Undo after full Discard", run-history, and key-rename diffing (the remaining §2.1 bullets) stay out of scope.
- Optimistic "saving…/saved vN" states + `useBlocker` unsaved-guard (need the data-router migration) stay deferred (gaps.md P1§5).

## Current-state facts (verified in code)

- `MaskedSecret = { value_version: number; created_at: string; origin: 'own' | 'inherited' | 'overridden' }` — per-key metadata for **all four sort keys is available with no reveal**.
- Reveal endpoints, all audited server-side:
  - `revealKeyRaw(cid, key)` → single value, one `secret.reveal` audit event.
  - `rawConfig(cid)` → whole config's own values + version, one audit event (used by existing `revealAll`).
  - There is **no** "reveal these N keys" endpoint.
- `SecretEditor.tsx` orchestrates: `buffer` (dirty edits), `revealed` (ephemeral RAW plaintext, never cached), `original` (RAW originals for dirty keys), `editing`, `filter`. It already has global `⌘S` (save) and per-row `Esc` (cancel edit), a `beforeunload` dirty guard, and 60s/blur auto-re-mask while anything is revealed.
- `SecretTable.tsx` renders rows via `rowState(key, masked, buffer, original)` → `{ change, origin, existing }`. Grid columns today: `Key | Value | Origin | Ver | Actions`.
- `dirty.ts`: `Buffer = Record<string,{value:string|null}>`; `removeKey`, `revert`, `setValue`, `toChanges`, `summarize`, `isDirty` — a `null` value means staged deletion, "effective" only when it differs from `original`.
- `ImportEnvDialog.tsx` + `parseDotenv` (in `rowState.ts`): paste → `{ pairs, skipped }` → `onApply` writes straight into the buffer with no preview.

## Architecture

`SecretEditor` **stays the orchestrator**; new concerns are pushed into pure helpers and focused hooks so the working reveal/dirty logic is untouched and the component doesn't bloat.

### New files

- `web/src/secrets/sortRows.ts` — pure. `sortRows(rows, masked, sort)` returns a reordered key list. `SortKey = 'key' | 'origin' | 'updated' | 'version'`; `SortState = { key: SortKey; dir: 'asc' | 'desc' } | null`.
- `web/src/secrets/exportEnv.ts` — pure formatting: `toEnvText(entries: Array<[string,string]>): string` producing `KEY=VALUE` lines (with minimal quoting rules mirroring `parseDotenv`'s `unquote`, so a round-trip is stable). No IO, no reveal — caller supplies already-revealed pairs.
- `web/src/secrets/importClassify.ts` — pure. `classifyImport(pairs, masked)` → `Array<{ key: string; kind: 'add' | 'update' }>` + carries the `skipped` count through. `update` = key present in `masked` (will edit/override); `add` = new key.
- `web/src/secrets/useRowSelection.ts` — hook. Holds `Set<string>`; `toggle(key)`, `clear()`, `setAll(keys)`, `isSelected(key)`, `count`. Caller prunes to visible keys.
- `web/src/secrets/useRowNav.ts` — hook. Holds `active: string | null`; exposes `onKeyDown` wiring (or installs its own `window` listener) implementing the key map below, **guarded** so it is inert while a text input/textarea/`contenteditable` is the active element.
- `web/src/secrets/SelectionBar.tsx` — presentational. Given `count` + callbacks, renders `N selected · [Reveal] [Copy .env] [Download .env] [Delete] [Clear]` using kit `Button`s + tokens.

### Modified files

- `SecretTable.tsx` — add a leading checkbox column; make the `Key/Origin/Updated/Version` headers sortable (click → `onSort(key)`), showing an asc/desc caret on the active column; render the active-row ring; surface a per-row selected state. Grid template gains a checkbox column; the `min-w` bumps accordingly.
- `EditorToolbar.tsx` — add a "Changed only" toggle (checkbox/segmented control) next to the filter.
- `ImportEnvDialog.tsx` — after parse, render the classified value-free preview list (scrollable) with Add/Update badges + skipped count; Import unchanged.
- `SecretEditor.tsx` — own the new `sort`, `selected` (via hook), `active` (via hook), `changedOnly` state; compute the visible list as `sortRows(rows) → filter → changedOnly`; wire bulk handlers; render `SelectionBar` when `count > 0`; pass sort/selection/active props to `SecretTable`.

### Data flow for the visible list

`rows` (masked keys in server order + buffer-added keys) → `sortRows(rows, masked, sort)` → text filter (existing substring) → `changedOnly` filter (`rowState(key).change != null`) → `visible`. Selection "select all" and keyboard nav operate over `visible`.

## Feature detail

### F1 — Column sorting

- Headers `Key`, `Origin`, `Updated`, `Version` are buttons. Click cycles: default → asc → desc → default.
- `sortRows` comparators: `key` = case-insensitive locale compare; `origin` = alphabetical on the origin string (`inherited` < `overridden` < `own`) for predictability; `updated` = `created_at` timestamp; `version` = `value_version` numeric. Ties break by key (asc) so order is deterministic.
- **Pending-added rows** have no `masked` entry → always pinned to the **top** regardless of `dir` (newest unsaved work stays visible). Stable sort otherwise.
- Session-only (React state), resets on unmount. Sorting reorders only; washes/rails/edit-state unaffected.
- The active sort column shows a caret (↑/↓); tokens only.

### F2 — Multi-row selection

- Leading checkbox column; header checkbox = select/deselect **all visible** rows (indeterminate when partial).
- `useRowSelection` holds the `Set`. `SecretEditor` prunes selection to `visible` keys whenever `visible` changes (so a filtered-out key isn't secretly acted on).
- `x` on the focused row toggles its selection.
- When `count > 0`, `SelectionBar` renders as a dedicated bar between the `EditorToolbar` and the table (toolbar stays put; the bar appears/disappears with selection). Selection clears on Save success and on Discard.

### F3 — Bulk operations

- **Delete**: for each selected key, apply the same rule as per-row: existing own/overridden → `removeKey` (stage deletion in buffer); pending-added → `revert` (discard the add); inherited-not-overridden → skip. Emit a summary toast: `"Deleted N · skipped M inherited"` (omit the skipped clause when M=0). No server call. Clears selection.
- **Reveal**: for each selected **existing** key, `revealKeyRaw(cid, key)` (audited, one event/key) → merge into `revealed`. Skips added keys (their value is already in buffer/original) and inherited-unstored keys gracefully. Subject to the existing auto-re-mask. Does not clear selection.
- **Copy .env / Download .env**: see Security.

### F4 — Keyboard navigation

Key map (active only when no text input/textarea is focused; coexists with global `⌘S`):

| Key | Action |
|-----|--------|
| `↑` / `k` | move active row up (within `visible`) |
| `↓` / `j` | move active row down |
| `x` | toggle selection of active row |
| `/` | focus the filter input (prevent default so `/` isn't typed) |
| `e` | edit active row (same as row Pencil) |
| `Enter` | reveal active row (same as row Eye) |
| `Del` / `Backspace` | remove active row (same as row X) |
| `Esc` | clear active + selection; if a row edit is open, cancel it (existing behavior wins first) |

- Active row = token ring (`focus`-like), no layout shift. If `active` falls out of `visible` (filter change), reset to null.
- Guard: `useRowNav` checks `document.activeElement` tag; if `INPUT`/`TEXTAREA`/editable, it no-ops (so typing a value, or `/` inside the filter, is normal).

### F5 — Import `.env` preview

- `ImportEnvDialog` computes `classifyImport(parsed.pairs, masked)`.
- Renders a scrollable list: each row = `KEY` (mono) + a badge `Add` (success tone) or `Update` (warning/brand tone). No values shown. Footer keeps `N keys · M skipped`.
- "Import N" stages into the buffer exactly as today (`onApply(pairs)`), then closes + resets.
- Empty paste → no list, Import disabled (unchanged).

### F6 — P2 wins

- **Changed only** toggle in `EditorToolbar`: boolean; when on, `visible` keeps only `rowState(key).change != null`. Composes with text filter. Off by default.
- **Zero-match empty state**: when `visible.length === 0` **and** (`filter` non-empty or `changedOnly`), render an `EmptyState` ("No keys match '<filter>'" / "No changed keys") instead of an empty table body. Distinct from the existing "No secrets yet" (which is for a config with zero rows total).

## Security

The audit/crypto invariants from CLAUDE.md and prior phases are preserved; the **only** new threat surface is bulk Copy/Download `.env`.

- **All reveals route through audited endpoints.** Bulk Reveal and Copy/Download call `revealKeyRaw` per selected key — **one `secret.reveal` audit event per key** — matching the audit model ("revealing a value in the UI MUST emit an audit event"). No masked/list path exposes values.
- **Plaintext stays ephemeral.** Copy/Download assemble `KEY=VALUE` text in a **local variable** (not React state, not the query cache), pass it to `navigator.clipboard.writeText` / a blob download, and drop the reference. Existing `revealed` remains the only plaintext state and is still auto-re-masked (60s/blur).
- **Download writes plaintext to disk** → gated behind a `ConfirmDialog` ("This writes N secret values in plaintext to a file. Continue?"), mirroring the CLI's explicit `--plain`. The blob URL is `revoke`d immediately after click (not cached).
- **Copy** is one-click (clipboard is transient) but explicit, with a success toast (and a danger toast on failure, matching the N7 copy-error convention). Neither path logs values.
- **Import preview and review-diff stay value-free** — names + change-kind only.
- No plaintext in logs, toasts, or error strings; constant-time/token rules unaffected (frontend-only change).

## Testing

- **Pure helpers** (table-driven unit tests): `sortRows` (each key, both dirs, added-rows-pinned, stability); `exportEnv.toEnvText` (quoting round-trips with `parseDotenv`); `classifyImport` (add/update/skip).
- **Hooks** (RTL): `useRowSelection` (toggle/clear/select-all/prune); `useRowNav` (arrow+vim movement, input-focus guard, `/` focuses filter).
- **Integration** (RTL + MSW): selection bar appears/acts; bulk delete stages buffer removals + skips inherited with the right toast; bulk reveal fires N audited `revealKeyRaw` calls; Download shows the confirm and only then downloads; import preview classifies; changed-only + zero-match render.
- **Leak test**: assert the export path reveals via the audited endpoint and that no plaintext lands in the TanStack Query cache (extend the existing editor leak-style coverage).
- All existing editor tests remain green (changes are additive; sorting/selection/keyboard are opt-in).
- Gates unchanged: `npm test`, dual-theme smoke, `no-raw-palette` / `dark-aa` / `no-legacy-alias` guards, tsc, build; token-only markup; both themes.

## Rough decomposition (for the plan)

1. Pure helpers + tests: `sortRows`, `exportEnv`, `importClassify`.
2. Sorting: sortable headers in `SecretTable` + wire in `SecretEditor`.
3. Selection: `useRowSelection` + checkbox column + `SelectionBar` shell.
4. Bulk delete (buffer-only) + inherited-skip toast.
5. Bulk reveal (audited per-key).
6. Bulk Copy/Download `.env` (+ confirm gate) — the security task.
7. Keyboard nav hook + active-row highlight.
8. Import preview.
9. P2: changed-only toggle + zero-match empty state.
10. Verification sweep (gates, security re-check, docs/tracker) + PR.
