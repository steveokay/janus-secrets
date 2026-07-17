# List ergonomics — search + sort for Tokens, Members, Users

**Status:** Approved 2026-07-17
**Scope:** Frontend-only (`web/`). No backend, no new endpoints, no migration.
**Gap:** gaps.md §2.5 (Tokens: "No search/filter/sort") and §2.6 (Members: "no user
search"). First slice of the "UI depth pass" program; other slices (audit depth,
cross-cutting polish, RBAC matrix) are separate future specs.

## Problem

Three admin tables are flat and unsearchable, so they stop being usable past a
few dozen rows:

- **Tokens** (`web/src/tokens/TokensPage.tsx`) — Name / Scope / Access / Created /
  Expires / Status. No search, no sort.
- **Members role-bindings** (`web/src/members/MembersPage.tsx`) — Email / Role.
  No search, no sort.
- **Users** (same file, instance scope only) — Email / Status. No search, no sort.

The backing lists are fully loaded (not paginated), so search and sort are pure
client-side concerns. Token `last_used` / user `last_login` columns are **out of
scope** — the backend does not record them (gaps.md §5), so they can't be a
frontend-only pass.

## Approach

One small shared primitive applied to all three tables, rather than bespoke
search/sort code per page. This keeps the tables consistent, DRY, and each unit
independently testable.

### New shared units — `web/src/ui/table/`

**`useTableControls.ts`** — the sort/filter engine.

```ts
export type SortDir = 'asc' | 'desc'

export interface TableControlsConfig<T> {
  // Case-insensitive substring match: a row matches if ANY field's string
  // contains the (trimmed, lowercased) query. Empty query matches all.
  searchFields: (row: T) => string[]
  // Comparators keyed by sort key. Each returns a stable a-b ordering for
  // ascending; the hook negates it for descending.
  comparators: Record<string, (a: T, b: T) => number>
  // Optional initial sort. When absent, the view preserves input order until
  // the user clicks a header (no surprise re-ordering on load).
  initialSort?: { key: string; dir: SortDir }
}

export interface TableControls<T> {
  query: string
  setQuery: (q: string) => void
  sortKey: string | null
  sortDir: SortDir
  // Cycle for a header: inactive -> asc -> desc -> inactive (back to input order).
  toggleSort: (key: string) => void
  view: T[]          // filtered THEN sorted
  total: number      // rows.length (pre-filter)
  matched: number    // view.length (post-filter)
}

export function useTableControls<T>(rows: T[], config: TableControlsConfig<T>): TableControls<T>
```

Rules:
- Filter first, then sort, both derived via `useMemo` on `[rows, query, sortKey, sortDir]`.
- Substring match is case-insensitive; the query is trimmed before compare; an
  empty/whitespace query matches every row.
- `toggleSort(key)` cycles the SAME key `asc → desc → off`; clicking a DIFFERENT
  key switches to it at `asc`. "off" clears `sortKey` to `null` and restores the
  original input order.
- Sorting never mutates `rows` (copy before sort).

**`SortHeader.tsx`** — a sortable column header.

```tsx
export function SortHeader({ label, sortKey, controls, className }: {
  label: string
  sortKey: string
  controls: Pick<TableControls<unknown>, 'sortKey' | 'sortDir' | 'toggleSort'>
  className?: string
}): JSX.Element
```

- Renders a `<th>` whose content is a `<button type="button">` (keyboard + click)
  showing `label` and, when this column is active, a caret: `▲` for asc, `▼` for
  desc. Inactive columns show a low-emphasis neutral affordance so they read as
  clickable.
- Sets `aria-sort` on the `<th>` to `ascending` / `descending` / `none`.
- Uses existing header token classes (`text-[10.5px] uppercase tracking-[.1em]
  text-ink-faint`, etc.) — no new palette. The button is `text-left` and inherits
  the header typography; the caret uses `text-ink-mute` / active `text-ink`.

**`TableSearch.tsx`** — the search input + count.

```tsx
export function TableSearch({ value, onChange, matched, total, label, placeholder }: {
  value: string
  onChange: (v: string) => void
  matched: number
  total: number
  label: string            // aria-label, e.g. "search tokens"
  placeholder?: string
}): JSX.Element
```

- A labeled `<input>` reusing the standard field classes already used across the
  app (`rounded border border-line bg-surface-3 px-3 py-1.5 text-[13px] …
  focus:border-brand-line focus:shadow-glow-soft transition-nocturne`).
- Shows a count hint "`matched` of `total`" beside/under the box (only when
  `value` is non-empty, to avoid noise on the default view).
- Provides a clear control (a small × button, or `Esc` clears) with
  `aria-label="clear search"`. `Esc` in the input clears the query.

### Per-table wiring

**Tokens** (`TokensPage.tsx`):
- `searchFields: (t) => [t.name, t.scope_kind]` — token name + the literal scope
  kind word ("config" / "environment" / "transit"). **Decision ①:** the
  cache-resolved scope *name* is intentionally NOT a search field, because it is
  best-effort and cache-dependent (`useResolvedScopeName`), which would make
  search results non-deterministic. Scope name still renders in the row.
- Comparators: `name` (localeCompare), `access` (localeCompare), `created`
  (`created_at` date), `expires` (`expires_at`; nulls — "never" — sort last in
  asc), `status` (revoked after active, keyed on `!!revoked_at`).
- Sortable headers: Name, Access, Created, Expires, Status. The trailing actions
  column is not sortable.
- Search box sits in the header row next to (left of) the Mint button.

**Members role-bindings** (`MembersPage.tsx`):
- `searchFields: (m) => [displayName(m.user_id, usersById)]` — the same
  email-or-id-prefix label shown in the cell.
- Comparators: `email` (localeCompare on the label), `role` — **Decision ②:**
  sorts by **privilege rank** `viewer(0) < developer(1) < admin(2) < owner(3)`,
  NOT alphabetically. Rank map derived from the existing `ROLES` array order.
- Sortable headers: Email, Role.
- Search box sits above the role-bindings table (below the scope selectors), so it
  composes with the scope filter: changing scope keeps the query and re-filters
  the new rows.

**Users** (`MembersPage.tsx`, instance scope):
- `searchFields: (u) => [u.email]`.
- Comparators: `email` (localeCompare), `status` (active before disabled, keyed on
  `!!disabled`).
- Sortable headers: Email, Status.
- Its own independent search box (own `useTableControls` instance) above the Users
  table.

### Behavior

- Controls are **ephemeral** component state (approved): they reset on navigation;
  no localStorage persistence.
- **Zero-match** state: when a non-empty query filters everything out, render a
  distinct empty state — "No matches for _query_" — separate from the existing
  "No … yet" empty states (which mean the underlying list is genuinely empty).
  The "… yet" state still shows when `total === 0`.
- **Default order**: no `initialSort` — the view preserves API order until the
  user clicks a header. Avoids surprising re-ordering on load and keeps existing
  snapshot/order expectations intact.
- Forbidden / loading / error branches are unchanged; search + sort render only in
  the populated-table branch.

## Value-safety

Nothing new is revealed. Tokens already never render secret values (only
metadata); the mint-once flow is untouched. Search operates on names/emails/scope
kinds already on screen. No audit-relevant reads are added.

## Testing

**`useTableControls` unit tests** (`web/src/ui/table/useTableControls.test.ts`):
- Empty query → all rows, input order preserved when no `initialSort`.
- Substring filter is case-insensitive and trims the query; matches when ANY field
  contains it; multi-field OR.
- `toggleSort` cycle: same key asc → desc → off (restores input order); switching
  keys resets to asc.
- Comparator correctness including the two custom orderings (role rank; nulls-last
  for expires; active-before-disabled/revoked).
- Sorting does not mutate the input array.

**`SortHeader` test** (`web/src/ui/table/SortHeader.test.tsx`):
- Renders label; click toggles direction; `aria-sort` reflects state; inactive vs
  active caret.

**Component tests** (extend existing `TokensPage.test.tsx`,
`MembersPage.test.tsx`):
- Type a query → rows filter, count hint updates, zero-match state on no results.
- Click a header → row order changes and `aria-sort` flips; Tokens Status and
  Members Role use their custom orderings.
- Token search matches `scope_kind` literal but not a scope name that only exists
  in resolved cache (Decision ① guard).
- Members: search composes with scope selector; Users table has an independent
  search from role-bindings.

**Full-suite gates:** `npm test` (all existing + new green), `npm run smoke`
(dual-theme render), typecheck, and the no-raw-palette test (new components use
token classes only).

## Out of scope (explicitly not this slice)

- Token `last_used` / user `last_login` columns (backend doesn't record them).
- Status/kind *filter chips* (search + sort only; a status dropdown is a separate
  enhancement).
- Revoke+remint rotation flow, stale-token highlighting (gaps.md §2.5 tail).
- RBAC matrix view, add-member picker search (gaps.md §2.6 — that's the separate
  "Members RBAC matrix" slice).
- Persistence of search/sort across navigation.
- Pagination / virtualization (lists are small and fully loaded).
