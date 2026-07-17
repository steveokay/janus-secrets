# List Ergonomics Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add case-insensitive search + click-to-sort columns to the Tokens, Members role-bindings, and instance Users tables via one shared, reusable primitive.

**Architecture:** A framework-free hook `useTableControls<T>` filters-then-sorts an in-memory row array; two presentational components (`SortHeader`, `TableSearch`) render the controls. All three tables (`TokensPage.tsx`, `MembersPage.tsx` × 2 tables) wire the primitive over data they already load. No backend, no endpoints, no migration.

**Tech Stack:** React 18 + TypeScript, TanStack Query (data already fetched), Vitest + @testing-library/react (`renderHook`, `renderApp`) + msw, Tailwind token classes only (enforced by `web/src/test/no-raw-palette.test.ts`).

**Spec:** `docs/superpowers/specs/2026-07-17-list-ergonomics-design.md`

**Conventions (read once):**
- Web tests run from `web/`: `npm test -- --run <path>` (bare `npm test` is watch mode — always pass `-- --run`).
- Token classes only — never raw palette (`gray-400`, `blue-600`, hex). Reuse the field classes already in these files: `rounded border border-line bg-surface-3 px-3 py-1.5 text-[13px] text-ink focus:border-brand-line focus:shadow-glow-soft transition-nocturne`.
- Value-free: these tables render metadata only; add nothing that reveals a secret.
- Final gates (Task 6): `npm test -- --run`, `npm run smoke`, `npm run typecheck`.

---

## File Structure

- **Create** `web/src/ui/table/useTableControls.ts` — the filter+sort engine (hook). One responsibility: given rows + config, return the filtered-then-sorted view plus control state.
- **Create** `web/src/ui/table/useTableControls.test.ts` — unit tests for the engine.
- **Create** `web/src/ui/table/SortHeader.tsx` — a sortable `<th>` (label + caret + `aria-sort`).
- **Create** `web/src/ui/table/SortHeader.test.tsx` — component test.
- **Create** `web/src/ui/table/TableSearch.tsx` — labeled search input + "N of M" count.
- **Create** `web/src/ui/table/TableSearch.test.tsx` — component test.
- **Modify** `web/src/tokens/TokensPage.tsx` — wire the primitive into the tokens table.
- **Modify** `web/src/tokens/TokensPage.test.tsx` — add search/sort tests.
- **Modify** `web/src/members/MembersPage.tsx` — wire it into the role-bindings table AND the Users table (two independent instances).
- **Modify** `web/src/members/MembersPage.test.tsx` — add search/sort tests for both tables.

---

## Task 1: `useTableControls` engine

**Files:**
- Create: `web/src/ui/table/useTableControls.ts`
- Test: `web/src/ui/table/useTableControls.test.ts`

- [ ] **Step 1: Write the failing test**

Create `web/src/ui/table/useTableControls.test.ts`:

```ts
import { renderHook, act } from '@testing-library/react'
import { useTableControls } from './useTableControls'

interface Row { name: string; kind: string; rank: number }

const rows: Row[] = [
  { name: 'ci', kind: 'config', rank: 2 },
  { name: 'Deploy', kind: 'environment', rank: 0 },
  { name: 'backup', kind: 'config', rank: 1 },
]

const config = {
  searchFields: (r: Row) => [r.name, r.kind],
  comparators: {
    name: (a: Row, b: Row) => a.name.localeCompare(b.name),
    rank: (a: Row, b: Row) => a.rank - b.rank,
  },
}

it('returns all rows in input order when the query is empty and no sort is active', () => {
  const { result } = renderHook(() => useTableControls(rows, config))
  expect(result.current.view.map((r) => r.name)).toEqual(['ci', 'Deploy', 'backup'])
  expect(result.current.total).toBe(3)
  expect(result.current.matched).toBe(3)
  expect(result.current.sortKey).toBeNull()
})

it('filters case-insensitively across all search fields and trims the query', () => {
  const { result } = renderHook(() => useTableControls(rows, config))
  act(() => result.current.setQuery('  CONFIG '))
  expect(result.current.view.map((r) => r.name)).toEqual(['ci', 'backup'])
  expect(result.current.matched).toBe(2)
  expect(result.current.total).toBe(3)
})

it('cycles a header asc -> desc -> off, restoring input order on off', () => {
  const { result } = renderHook(() => useTableControls(rows, config))
  act(() => result.current.toggleSort('name'))
  expect(result.current.sortDir).toBe('asc')
  // localeCompare orders by base letter (b < c < d), case-insensitive at primary
  // strength — deterministic across locales since the first letters differ.
  expect(result.current.view.map((r) => r.name)).toEqual(['backup', 'ci', 'Deploy'])
  act(() => result.current.toggleSort('name'))
  expect(result.current.sortDir).toBe('desc')
  expect(result.current.view.map((r) => r.name)).toEqual(['Deploy', 'ci', 'backup'])
  act(() => result.current.toggleSort('name'))
  expect(result.current.sortKey).toBeNull()
  expect(result.current.view.map((r) => r.name)).toEqual(['ci', 'Deploy', 'backup']) // input order
})

it('switches to a different key at ascending', () => {
  const { result } = renderHook(() => useTableControls(rows, config))
  act(() => result.current.toggleSort('name'))
  act(() => result.current.toggleSort('name')) // name desc
  act(() => result.current.toggleSort('rank')) // switch
  expect(result.current.sortKey).toBe('rank')
  expect(result.current.sortDir).toBe('asc')
  expect(result.current.view.map((r) => r.rank)).toEqual([0, 1, 2])
})

it('honors initialSort and never mutates the input array', () => {
  const input = [...rows]
  const { result } = renderHook(() =>
    useTableControls(input, { ...config, initialSort: { key: 'rank', dir: 'desc' } }),
  )
  expect(result.current.view.map((r) => r.rank)).toEqual([2, 1, 0])
  expect(input.map((r) => r.name)).toEqual(['ci', 'Deploy', 'backup']) // original untouched
})
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd web && npm test -- --run src/ui/table/useTableControls.test.ts`
Expected: FAIL — `Failed to resolve import './useTableControls'`.

- [ ] **Step 3: Write the implementation**

Create `web/src/ui/table/useTableControls.ts`:

```ts
import { useState } from 'react'

export type SortDir = 'asc' | 'desc'

export interface TableControlsConfig<T> {
  /** A row matches when ANY returned string contains the (trimmed, lowercased) query. */
  searchFields: (row: T) => string[]
  /** Ascending comparators keyed by sort key; the hook negates for descending. */
  comparators: Record<string, (a: T, b: T) => number>
  /** When absent, the view preserves input order until a header is clicked. */
  initialSort?: { key: string; dir: SortDir }
}

export interface TableControls<T> {
  query: string
  setQuery: (q: string) => void
  sortKey: string | null
  sortDir: SortDir
  /** Same key: asc -> desc -> off. Different key: switch to it at asc. */
  toggleSort: (key: string) => void
  view: T[]
  total: number
  matched: number
}

export function useTableControls<T>(
  rows: T[],
  config: TableControlsConfig<T>,
): TableControls<T> {
  const [query, setQuery] = useState('')
  const [sortKey, setSortKey] = useState<string | null>(config.initialSort?.key ?? null)
  const [sortDir, setSortDir] = useState<SortDir>(config.initialSort?.dir ?? 'asc')

  function toggleSort(key: string) {
    if (sortKey !== key) {
      setSortKey(key)
      setSortDir('asc')
      return
    }
    if (sortDir === 'asc') {
      setSortDir('desc')
      return
    }
    setSortKey(null) // was desc -> off (restore input order)
    setSortDir('asc')
  }

  // Lists here are small and fully loaded; deriving each render is cheaper than
  // the memo bookkeeping and sidesteps unstable-config-identity deps.
  const q = query.trim().toLowerCase()
  const filtered =
    q === ''
      ? rows
      : rows.filter((row) => config.searchFields(row).some((f) => f.toLowerCase().includes(q)))

  let view = filtered
  const cmp = sortKey !== null ? config.comparators[sortKey] : undefined
  if (cmp) {
    view = [...filtered].sort((a, b) => (sortDir === 'asc' ? cmp(a, b) : -cmp(a, b)))
  }

  return {
    query,
    setQuery,
    sortKey,
    sortDir,
    toggleSort,
    view,
    total: rows.length,
    matched: view.length,
  }
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd web && npm test -- --run src/ui/table/useTableControls.test.ts`
Expected: PASS (5 tests).

- [ ] **Step 5: Commit**

```bash
git add web/src/ui/table/useTableControls.ts web/src/ui/table/useTableControls.test.ts
git commit -m "feat(web): useTableControls filter+sort hook"
```

---

## Task 2: `SortHeader` component

**Files:**
- Create: `web/src/ui/table/SortHeader.tsx`
- Test: `web/src/ui/table/SortHeader.test.tsx`

- [ ] **Step 1: Write the failing test**

Create `web/src/ui/table/SortHeader.test.tsx`:

```tsx
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { SortHeader } from './SortHeader'

function renderHeader(over: Partial<{ sortKey: string | null; sortDir: 'asc' | 'desc' }> = {}) {
  const toggleSort = vi.fn()
  render(
    <table>
      <thead>
        <tr>
          <SortHeader
            label="Name"
            sortKey="name"
            controls={{ sortKey: over.sortKey ?? null, sortDir: over.sortDir ?? 'asc', toggleSort }}
          />
        </tr>
      </thead>
    </table>,
  )
  return { toggleSort }
}

it('is inactive by default with aria-sort none', () => {
  renderHeader()
  expect(screen.getByRole('columnheader')).toHaveAttribute('aria-sort', 'none')
})

it('calls toggleSort with its key on click', async () => {
  const { toggleSort } = renderHeader()
  await userEvent.click(screen.getByRole('button', { name: /name/i }))
  expect(toggleSort).toHaveBeenCalledWith('name')
})

it('reflects the active ascending state in aria-sort', () => {
  renderHeader({ sortKey: 'name', sortDir: 'asc' })
  expect(screen.getByRole('columnheader')).toHaveAttribute('aria-sort', 'ascending')
})

it('reflects the active descending state in aria-sort', () => {
  renderHeader({ sortKey: 'name', sortDir: 'desc' })
  expect(screen.getByRole('columnheader')).toHaveAttribute('aria-sort', 'descending')
})
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd web && npm test -- --run src/ui/table/SortHeader.test.tsx`
Expected: FAIL — cannot resolve `./SortHeader`.

- [ ] **Step 3: Write the implementation**

Create `web/src/ui/table/SortHeader.tsx`:

```tsx
import { TableControls } from './useTableControls'

export function SortHeader({
  label,
  sortKey,
  controls,
  className,
}: {
  label: string
  sortKey: string
  controls: Pick<TableControls<unknown>, 'sortKey' | 'sortDir' | 'toggleSort'>
  className?: string
}) {
  const active = controls.sortKey === sortKey
  const ariaSort = active ? (controls.sortDir === 'asc' ? 'ascending' : 'descending') : 'none'
  const caret = active ? (controls.sortDir === 'asc' ? '▲' : '▼') : '↕'
  return (
    <th aria-sort={ariaSort} className={`py-1.5 ${className ?? ''}`}>
      <button
        type="button"
        onClick={() => controls.toggleSort(sortKey)}
        className="flex items-center gap-1 text-[10.5px] uppercase tracking-[.1em] text-ink-faint hover:text-ink transition-nocturne"
      >
        {label}
        <span aria-hidden="true" className={active ? 'text-ink' : 'text-ink-faint'}>
          {caret}
        </span>
      </button>
    </th>
  )
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd web && npm test -- --run src/ui/table/SortHeader.test.tsx`
Expected: PASS (4 tests).

- [ ] **Step 5: Commit**

```bash
git add web/src/ui/table/SortHeader.tsx web/src/ui/table/SortHeader.test.tsx
git commit -m "feat(web): SortHeader sortable column header"
```

---

## Task 3: `TableSearch` component

**Files:**
- Create: `web/src/ui/table/TableSearch.tsx`
- Test: `web/src/ui/table/TableSearch.test.tsx`

- [ ] **Step 1: Write the failing test**

Create `web/src/ui/table/TableSearch.test.tsx`:

```tsx
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { TableSearch } from './TableSearch'

it('forwards typed input to onChange', async () => {
  const onChange = vi.fn()
  render(<TableSearch value="" onChange={onChange} matched={5} total={5} label="search tokens" />)
  await userEvent.type(screen.getByLabelText('search tokens'), 'ci')
  expect(onChange).toHaveBeenCalled()
  expect(onChange.mock.calls.at(-1)?.[0]).toBe('i') // last keystroke value (controlled input stays '')
})

it('hides the count when empty and shows "N of M" when searching', () => {
  const { rerender } = render(
    <TableSearch value="" onChange={() => {}} matched={5} total={5} label="search tokens" />,
  )
  expect(screen.queryByText(/of/)).toBeNull()
  rerender(<TableSearch value="ci" onChange={() => {}} matched={2} total={5} label="search tokens" />)
  expect(screen.getByText('2 of 5')).toBeInTheDocument()
})

it('clears on Escape', async () => {
  const onChange = vi.fn()
  render(<TableSearch value="ci" onChange={onChange} matched={2} total={5} label="search tokens" />)
  const input = screen.getByLabelText('search tokens')
  input.focus()
  await userEvent.keyboard('{Escape}')
  expect(onChange).toHaveBeenCalledWith('')
})
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd web && npm test -- --run src/ui/table/TableSearch.test.tsx`
Expected: FAIL — cannot resolve `./TableSearch`.

- [ ] **Step 3: Write the implementation**

Create `web/src/ui/table/TableSearch.tsx`:

```tsx
export function TableSearch({
  value,
  onChange,
  matched,
  total,
  label,
  placeholder,
}: {
  value: string
  onChange: (v: string) => void
  matched: number
  total: number
  label: string
  placeholder?: string
}) {
  return (
    <div className="flex items-center gap-2">
      <input
        type="search"
        aria-label={label}
        value={value}
        placeholder={placeholder ?? 'Search…'}
        onChange={(e) => onChange(e.target.value)}
        onKeyDown={(e) => {
          if (e.key === 'Escape') onChange('')
        }}
        className="w-56 rounded border border-line bg-surface-3 px-3 py-1.5 text-[13px] text-ink focus:border-brand-line focus:shadow-glow-soft transition-nocturne"
      />
      {value.trim() !== '' && (
        <span className="text-[11.5px] text-ink-faint">
          {matched} of {total}
        </span>
      )}
    </div>
  )
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd web && npm test -- --run src/ui/table/TableSearch.test.tsx`
Expected: PASS (3 tests).

- [ ] **Step 5: Commit**

```bash
git add web/src/ui/table/TableSearch.tsx web/src/ui/table/TableSearch.test.tsx
git commit -m "feat(web): TableSearch input with result count"
```

---

## Task 4: Wire the Tokens table

**Files:**
- Modify: `web/src/tokens/TokensPage.tsx`
- Test: `web/src/tokens/TokensPage.test.tsx`

Context: `TokensPage` renders `rows = tokens.data ?? []` into a table with columns Name / Scope / Access / Created / Expires / Status / (actions). `TokenMeta` = `{ id, name, scope_kind: 'config'|'environment'|'transit', scope_id, access, created_at, expires_at?, revoked_at? }`. The scope *name* is resolved best-effort from cache (`useResolvedScopeName`) and per Decision ① is NOT a search field.

- [ ] **Step 1: Write the failing tests**

Add to `web/src/tokens/TokensPage.test.tsx` (keep existing tests; append these). They rely on the existing `T(...)`, `mockTokens`, and `mount` helpers already in the file:

```tsx
it('filters tokens by name substring, case-insensitively', async () => {
  mockTokens([T({ id: 't1', name: 'ci-deploy' }), T({ id: 't2', name: 'backup-runner' })])
  mount()
  await screen.findByText('ci-deploy')
  await userEvent.type(screen.getByLabelText('search tokens'), 'BACKUP')
  expect(screen.queryByText('ci-deploy')).toBeNull()
  expect(screen.getByText('backup-runner')).toBeInTheDocument()
  expect(screen.getByText('1 of 2')).toBeInTheDocument()
})

it('matches the scope kind word but not a cache-only scope name', async () => {
  mockTokens([
    T({ id: 't1', name: 'alpha', scope_kind: 'environment' }),
    T({ id: 't2', name: 'beta', scope_kind: 'config' }),
  ])
  mount()
  await screen.findByText('alpha')
  await userEvent.type(screen.getByLabelText('search tokens'), 'environment')
  expect(screen.getByText('alpha')).toBeInTheDocument()
  expect(screen.queryByText('beta')).toBeNull()
})

it('shows a zero-match message when nothing matches', async () => {
  mockTokens([T({ id: 't1', name: 'ci-deploy' })])
  mount()
  await screen.findByText('ci-deploy')
  await userEvent.type(screen.getByLabelText('search tokens'), 'zzz')
  expect(screen.getByText(/no tokens match/i)).toBeInTheDocument()
})

it('sorts by name when the Name header is clicked', async () => {
  mockTokens([T({ id: 't1', name: 'zeta' }), T({ id: 't2', name: 'alpha' })])
  mount()
  await screen.findByText('zeta')
  await userEvent.click(screen.getByRole('button', { name: /name/i }))
  const cells = screen.getAllByRole('cell').filter((c) => /alpha|zeta/.test(c.textContent ?? ''))
  expect(cells[0]).toHaveTextContent('alpha') // ascending
})
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd web && npm test -- --run src/tokens/TokensPage.test.tsx`
Expected: FAIL — no `search tokens` input exists yet.

- [ ] **Step 3: Implement the wiring**

In `web/src/tokens/TokensPage.tsx`:

(a) Add imports near the other `../ui` imports:

```tsx
import { useTableControls } from '../ui/table/useTableControls'
import { SortHeader } from '../ui/table/SortHeader'
import { TableSearch } from '../ui/table/TableSearch'
```

(b) Inside `TokensPage`, after `const rows = tokens.data ?? []`, add the controls:

```tsx
  const controls = useTableControls(rows, {
    searchFields: (t) => [t.name, t.scope_kind],
    comparators: {
      name: (a, b) => a.name.localeCompare(b.name),
      access: (a, b) => a.access.localeCompare(b.access),
      created: (a, b) => a.created_at.localeCompare(b.created_at), // ISO UTC sorts chronologically
      expires: (a, b) => {
        if (!a.expires_at && !b.expires_at) return 0
        if (!a.expires_at) return 1 // "never" sorts last ascending
        if (!b.expires_at) return -1
        return a.expires_at.localeCompare(b.expires_at)
      },
      status: (a, b) => Number(!!a.revoked_at) - Number(!!b.revoked_at), // active before revoked
    },
  })
```

(c) Replace the populated-table branch (the final `) : (` … `<table>…</table>` … `)}`) with a toolbar + sortable headers + view rows + zero-match row. Replace the existing `<table className="w-full …">…</table>` block with:

```tsx
        <>
          <div className="mb-2">
            <TableSearch
              value={controls.query}
              onChange={controls.setQuery}
              matched={controls.matched}
              total={controls.total}
              label="search tokens"
              placeholder="Search tokens…"
            />
          </div>
          <table className="w-full rounded-card border border-line bg-surface-2 text-sm shadow-elev-1">
            <thead>
              <tr className="sticky top-0 z-10 bg-surface-1 text-left text-[10.5px] uppercase tracking-[.1em] text-ink-faint">
                <SortHeader label="Name" sortKey="name" controls={controls} />
                <th className="py-1.5">Scope</th>
                <SortHeader label="Access" sortKey="access" controls={controls} />
                <SortHeader label="Created" sortKey="created" controls={controls} />
                <SortHeader label="Expires" sortKey="expires" controls={controls} />
                <SortHeader label="Status" sortKey="status" controls={controls} />
                <th className="py-1.5" />
              </tr>
            </thead>
            <tbody>
              {controls.matched === 0 ? (
                <tr>
                  <td colSpan={7} className="py-6 text-center text-[12.5px] text-ink-mute">
                    No tokens match “{controls.query}”.
                  </td>
                </tr>
              ) : (
                controls.view.map((t) => (
                  <tr key={t.id} className="border-t border-line-soft hover:bg-row-hover transition-nocturne">
                    <td className="py-1.5">{t.name}</td>
                    <td className="py-1.5"><ScopeCell kind={t.scope_kind} id={t.scope_id} /></td>
                    <td className="py-1.5">{t.access}</td>
                    <td className="py-1.5"><span title={t.created_at}>{relativeTime(t.created_at)}</span></td>
                    <td className="py-1.5">
                      {t.expires_at ? <span title={t.expires_at}>{relativeTime(t.expires_at)}</span> : 'never'}
                    </td>
                    <td className="py-1.5">{t.revoked_at ? <Pill tone="danger">revoked</Pill> : null}</td>
                    <td className="py-1.5 text-right">
                      {!t.revoked_at && (
                        <Button type="button" variant="danger" size="sm" onClick={() => setRevokeTarget(t)}>
                          Revoke
                        </Button>
                      )}
                    </td>
                  </tr>
                ))
              )}
            </tbody>
          </table>
        </>
```

Note: the `SortHeader controls={controls}` prop is typed `TableControls<unknown>`; passing `TableControls<TokenMeta>` is assignment-compatible for the picked fields (`sortKey`/`sortDir`/`toggleSort`). If `tsc` complains, widen the prop at the call site with `controls={controls as unknown as import('../ui/table/useTableControls').TableControls<unknown>}` — but verify first; the `Pick` of primitive/function fields should type-check directly.

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd web && npm test -- --run src/tokens/TokensPage.test.tsx`
Expected: PASS (existing tests + 4 new).

- [ ] **Step 5: Typecheck**

Run: `cd web && npm run typecheck`
Expected: no errors.

- [ ] **Step 6: Commit**

```bash
git add web/src/tokens/TokensPage.tsx web/src/tokens/TokensPage.test.tsx
git commit -m "feat(web): search + sort on the tokens table"
```

---

## Task 5: Wire the Members role-bindings and Users tables

**Files:**
- Modify: `web/src/members/MembersPage.tsx`
- Test: `web/src/members/MembersPage.test.tsx`

Context: `MembersPage` has TWO tables. (1) Role-bindings: `rows = members.data ?? []`, each `Member` = `{ user_id, role }`; the visible label is `displayName(m.user_id, usersById)` (email or id-prefix). (2) Users (instance scope only): `usersList = users.data ?? []`, each `UserInfo` = `{ id, email, disabled }`. `ROLES = ['viewer','developer','admin','owner']` — index = ascending privilege rank (Decision ②). Give each table its own `useTableControls` instance.

- [ ] **Step 1: Write the failing tests**

The file already has these helpers (reuse them verbatim — do NOT invent new ones): `mockInstanceMembers(members: Member[])` → mocks `GET /v1/instance/members`; `mockUsers(users: UserInfo[])` → mocks `GET /v1/users`; and `mount()` → `renderApp(<ToastProvider><MembersPage/></ToastProvider>, { route: '/members', withAuth: false })`. The page defaults to the **instance** scope, so `mount()` renders both the role-bindings table and the Users table. Append these tests:

```tsx
it('filters role-bindings by email substring', async () => {
  mockInstanceMembers([
    { user_id: 'u-alice', role: 'developer' },
    { user_id: 'u-bob', role: 'admin' },
  ])
  mockUsers([
    { id: 'u-alice', email: 'alice@corp.io', disabled: false },
    { id: 'u-bob', email: 'bob@corp.io', disabled: false },
  ])
  mount()
  await screen.findByText('alice@corp.io')
  await userEvent.type(screen.getByLabelText('search members'), 'bob')
  expect(screen.queryByText('alice@corp.io')).toBeNull()
  expect(screen.getByText('bob@corp.io')).toBeInTheDocument()
})

it('sorts role-bindings by privilege rank, not alphabetically', async () => {
  mockInstanceMembers([
    { user_id: 'u-owner', role: 'owner' },
    { user_id: 'u-viewer', role: 'viewer' },
  ])
  mockUsers([
    { id: 'u-owner', email: 'owner@corp.io', disabled: false },
    { id: 'u-viewer', email: 'viewer@corp.io', disabled: false },
  ])
  mount()
  await screen.findByText('owner@corp.io')
  await userEvent.click(screen.getByRole('button', { name: /^role/i }))
  const emailCells = screen.getAllByRole('cell').filter((c) => /@corp\.io/.test(c.textContent ?? ''))
  expect(emailCells[0]).toHaveTextContent('viewer@corp.io') // viewer(0) before owner(3) ascending
})

it('filters the Users table independently of the members search', async () => {
  mockInstanceMembers([{ user_id: 'u-alice', role: 'developer' }])
  mockUsers([
    { id: 'u-alice', email: 'alice@corp.io', disabled: false },
    { id: 'u-bob', email: 'bob@corp.io', disabled: true },
  ])
  mount()
  await screen.findByText('Users')
  await userEvent.type(screen.getByLabelText('search users'), 'bob')
  // The two search boxes are independent instances.
  expect(screen.getByLabelText('search users')).toHaveValue('bob')
  expect(screen.getByLabelText('search members')).toHaveValue('')
})
```

Note on the Role-header query: use `{ name: /^role/i }` so it doesn't also match the per-row `role for <email>` select labels. If the existing helpers are named slightly differently, match the file's actual names rather than these — but they are `mockInstanceMembers` / `mockUsers` / `mount` as of this writing.

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd web && npm test -- --run src/members/MembersPage.test.tsx`
Expected: FAIL — no `search members` / `search users` inputs yet.

- [ ] **Step 3: Implement the wiring**

In `web/src/members/MembersPage.tsx`:

(a) Add imports:

```tsx
import { useTableControls } from '../ui/table/useTableControls'
import { SortHeader } from '../ui/table/SortHeader'
import { TableSearch } from '../ui/table/TableSearch'
```

(b) After `const rows = members.data ?? []`, add a member-controls instance (uses `displayName` + `usersById`, both already defined above in the component):

```tsx
  const roleRank = (r: MemberRole) => ROLES.indexOf(r) // viewer(0) < developer(1) < admin(2) < owner(3)
  const memberControls = useTableControls(rows, {
    searchFields: (m) => [displayName(m.user_id, usersById)],
    comparators: {
      email: (a, b) => displayName(a.user_id, usersById).localeCompare(displayName(b.user_id, usersById)),
      role: (a, b) => roleRank(a.role) - roleRank(b.role),
    },
  })
```

(c) After `const usersList = users.data ?? []` (and `usersById`), add a users-controls instance:

```tsx
  const userControls = useTableControls(usersList, {
    searchFields: (u) => [u.email],
    comparators: {
      email: (a, b) => a.email.localeCompare(b.email),
      status: (a, b) => Number(!!a.disabled) - Number(!!b.disabled), // active before disabled
    },
  })
```

(d) In the role-bindings populated branch, add the search box above the table, convert the two headers to `SortHeader`, map `memberControls.view`, and add a zero-match row. Replace the existing `<table …>…</table>` (role-bindings one, columns Email/Role/actions) with:

```tsx
        <>
          <div className="mb-2">
            <TableSearch
              value={memberControls.query}
              onChange={memberControls.setQuery}
              matched={memberControls.matched}
              total={memberControls.total}
              label="search members"
              placeholder="Search members…"
            />
          </div>
          <table className="w-full rounded-card border border-line bg-surface-2 text-sm shadow-elev-1">
            <thead>
              <tr className="sticky top-0 z-10 bg-surface-1 text-left text-[10.5px] uppercase tracking-[.1em] text-ink-faint">
                <SortHeader label="Email" sortKey="email" controls={memberControls} />
                <SortHeader label="Role" sortKey="role" controls={memberControls} />
                <th className="py-1.5" />
              </tr>
            </thead>
            <tbody>
              {memberControls.matched === 0 ? (
                <tr>
                  <td colSpan={3} className="py-6 text-center text-[12.5px] text-ink-mute">
                    No members match “{memberControls.query}”.
                  </td>
                </tr>
              ) : (
                memberControls.view.map((m) => {
                  const label = displayName(m.user_id, usersById)
                  return (
                    <tr key={m.user_id} className="border-t border-line-soft hover:bg-row-hover transition-nocturne">
                      <td className="py-1.5">{label}</td>
                      <td className="py-1.5">
                        <select
                          aria-label={`role for ${label}`}
                          value={m.role}
                          onChange={(e) => setPendingRole({ uid: m.user_id, role: e.target.value as MemberRole, label })}
                          className="rounded border border-line bg-surface-3 px-2 py-1 text-[12.5px] text-ink focus:border-brand-line focus:shadow-glow-soft transition-nocturne"
                        >
                          {ROLES.map((r) => <option key={r} value={r}>{r}</option>)}
                        </select>
                      </td>
                      <td className="py-1.5 text-right">
                        <Button type="button" variant="danger" size="sm" onClick={() => setRemoveTarget({ uid: m.user_id, label })}>
                          Remove
                        </Button>
                      </td>
                    </tr>
                  )
                })
              )}
            </tbody>
          </table>
        </>
```

(e) In the Users section, add its search box above the table, convert both headers to `SortHeader`, map `userControls.view`, add a zero-match row. Replace the Users `<table …>…</table>` with:

```tsx
          <div className="mb-2">
            <TableSearch
              value={userControls.query}
              onChange={userControls.setQuery}
              matched={userControls.matched}
              total={userControls.total}
              label="search users"
              placeholder="Search users…"
            />
          </div>
          <table className="w-full rounded-card border border-line bg-surface-2 text-sm shadow-elev-1">
            <thead>
              <tr className="sticky top-0 z-10 bg-surface-1 text-left text-[10.5px] uppercase tracking-[.1em] text-ink-faint">
                <SortHeader label="Email" sortKey="email" controls={userControls} />
                <SortHeader label="Status" sortKey="status" controls={userControls} />
                <th className="py-1.5" />
              </tr>
            </thead>
            <tbody>
              {userControls.matched === 0 ? (
                <tr>
                  <td colSpan={3} className="py-6 text-center text-[12.5px] text-ink-mute">
                    No users match “{userControls.query}”.
                  </td>
                </tr>
              ) : (
                userControls.view.map((u) => (
                  <tr key={u.id} className="border-t border-line-soft hover:bg-row-hover transition-nocturne">
                    <td className="py-1.5">{u.email}</td>
                    <td className="py-1.5">{u.disabled ? <Pill tone="danger">disabled</Pill> : <Pill tone="success">active</Pill>}</td>
                    <td className="py-1.5 text-right">
                      {!u.disabled && (
                        <Button type="button" variant="danger" size="sm" onClick={() => setDisableTarget(u)}>
                          Disable
                        </Button>
                      )}
                    </td>
                  </tr>
                ))
              )}
            </tbody>
          </table>
```

Note on hook ordering: both `useTableControls` calls must run on every render (unconditionally, before any early `return`), because the Users section is conditionally *rendered* but the hook must not be conditionally *called*. Placing `userControls` near the top of the component body (right after `usersList`/`usersById`) satisfies the Rules of Hooks even though the Users table only shows for the instance scope.

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd web && npm test -- --run src/members/MembersPage.test.tsx`
Expected: PASS (existing tests + 3 new).

- [ ] **Step 5: Typecheck**

Run: `cd web && npm run typecheck`
Expected: no errors.

- [ ] **Step 6: Commit**

```bash
git add web/src/members/MembersPage.tsx web/src/members/MembersPage.test.tsx
git commit -m "feat(web): search + sort on members role-bindings and users tables"
```

---

## Task 6: Full-suite gates + tracker

**Files:**
- Modify: `gaps.md` (mark §2.5/§2.6 search+sort done)

- [ ] **Step 1: Run the full web test suite**

Run: `cd web && npm test -- --run`
Expected: all tests pass (previous count + the ~19 new: 5+4+3 primitives, 4 tokens, 3 members).

- [ ] **Step 2: Run the dual-theme smoke check**

Run: `cd web && npm run smoke`
Expected: `smoke ok — light (…) + dark (…)`.

- [ ] **Step 3: Typecheck the whole web app**

Run: `cd web && npm run typecheck`
Expected: no errors.

- [ ] **Step 4: Update the gaps tracker**

In `gaps.md`, update the §2.5 Tokens bullet and §2.6 Members bullet to note that search + column sort now ship (leave the still-open items — `last_used`/`last_login` (backend), RBAC matrix, revoke+remint — as open). For §2.5, change:

```
- No search/filter/sort; no last-used timestamp (backend doesn't record it — backend gap); no stale-token highlighting; no revoke+remint rotation flow.
```
to:
```
- ~~No search/filter/sort~~ **[DONE 2026-07-17]** — name/scope-kind search + click-to-sort columns (shared `web/src/ui/table/` primitive). Still open: no last-used timestamp (backend doesn't record it — backend gap); no stale-token highlighting; no revoke+remint rotation flow.
```

For §2.6, change:

```
- Has create/disable/roles (verified), but: no user search in add-member picker (unmanageable past ~30 users), no last-login column, no RBAC matrix view (users × scopes grid).
```
to:
```
- Has create/disable/roles + **[DONE 2026-07-17]** search + sort on the role-bindings and Users tables (privilege-rank role sort). Still open: no user search in the add-member *picker*, no last-login column (backend gap), no RBAC matrix view (users × scopes grid).
```

- [ ] **Step 5: Commit**

```bash
git add gaps.md
git commit -m "docs(gaps): mark tokens/members search+sort done (2.5/2.6)"
```

---

## Self-Review notes (for the executor)

- **Spec coverage:** T1 = `useTableControls` (filter+sort engine, Decisions ①② live in the per-table configs of T4/T5); T2 = `SortHeader` (aria-sort, caret); T3 = `TableSearch` (count, Esc-clear); T4 = Tokens wiring incl. Decision ① (scope_kind searched, resolved name not) + status ordering; T5 = Members role-bindings (Decision ②: role rank) + Users (independent instance); T6 = gates + tracker. Ephemeral state, default-API-order, and distinct zero-match state are realized in T4/T5.
- **Type consistency:** `TableControls<T>` / `TableControlsConfig<T>` / `useTableControls` / `SortHeader` / `TableSearch` signatures are identical everywhere used. Sort keys are strings matching the comparator map keys per table (`name/access/created/expires/status`; `email/role`; `email/status`).
- **Value-free:** no task reveals a secret; searches operate on names/emails/scope kinds already on screen.
