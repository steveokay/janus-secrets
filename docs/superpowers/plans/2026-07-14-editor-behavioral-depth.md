# Secret Editor Behavioral Depth — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add sorting, multi-row selection + bulk operations (reveal / copy-download `.env` / delete), keyboard navigation, a value-free `.env` import preview, a "changed only" toggle, and a filter zero-match empty state to the SPA secret editor — frontend-only, preserving the dirty-buffer/config-version model, on-demand audited reveal, and value-free diffs.

**Architecture:** `SecretEditor.tsx` stays the orchestrator. New pure helpers (`sortRows`, `exportEnv`, `importClassify`) and focused hooks (`useRowSelection`, `useRowNav`) carry the new logic; new/edited components (`SelectionBar`, `SecretTable`, `EditorToolbar`, `ImportEnvDialog`) render it. Every reveal (single, bulk, export) routes through the existing audited endpoints into ephemeral `revealed` state — no new plaintext state.

**Tech Stack:** React + TS + Vite + Tailwind (Nocturne CSS-var tokens) + TanStack Query + react-router v6 + Vitest/RTL/MSW v2. Kit primitives in `web/src/ui/`.

**Spec:** `docs/superpowers/specs/2026-07-14-editor-behavioral-depth-design.md`.

---

## Invariants (apply to EVERY task)

- **Design tokens only.** No hex, no `dark:` variants, no raw palette classes (`gray-`/`blue-`/…), no `text-brand-deep`, no legacy aliases (`text-muted`/`text-faint`/`shadow-card`). Use kit primitives (`Button`, `Pill`, `ConfirmDialog`, `EmptyState`, `Modal`) and existing token classes. Both light + dark must render.
- **SECURITY — preserve exactly:**
  - No reveal on mount; masked list (`maskedSecrets`) never carries values.
  - Every reveal (per-key `revealKeyRaw`, whole-config `rawConfig`) is server-audited; bulk reveal and copy/download call `revealKeyRaw` **once per selected key** (one `secret.reveal` event each). No "reveal-all-then-filter" shortcut.
  - Plaintext lives ONLY in the existing ephemeral `revealed` map (auto-re-masked at 60s/blur) or in a short-lived LOCAL variable for copy/download — never the TanStack Query cache, never logs, never toasts, never a new React state atom.
  - `Download .env` (writes plaintext to disk) is gated behind a `ConfirmDialog`; the blob URL is revoked immediately after click.
  - Review-diff and import-preview stay value-free (names + change-kind only).
- **Additive & green:** existing editor tests must stay green; new behavior is opt-in. Gates: `cd web && npm test -- --run`, `npm run typecheck`, `npm run build`, dual-theme smoke, `no-raw-palette`/`dark-aa`/`no-legacy-alias` guards.
- **Test harness idioms** (mirror `web/src/secrets/SecretEditor.test.tsx`): `import { server } from '../test/msw'`; `import { renderApp } from '../test/render'`; `renderApp(<SecretEditor />, { route: '/projects/p1/configs/c1', withAuth: false })`. MSW matches **path only** — a `/v1/configs/c1/secrets` handler must branch on the query string (`reveal=true` → raw config; else masked list). Wrap in `<ToastProvider>` when asserting a toast.

## File structure

```
web/src/secrets/sortRows.ts            (NEW, pure)   order a key list by sort state
web/src/secrets/sortRows.test.ts       (NEW)
web/src/secrets/exportEnv.ts           (NEW, pure)   [key,value][] -> KEY=VALUE .env text
web/src/secrets/exportEnv.test.ts      (NEW)
web/src/secrets/importClassify.ts      (NEW, pure)   parsed pairs -> add/update rows
web/src/secrets/importClassify.test.ts (NEW)
web/src/secrets/useRowSelection.ts     (NEW, hook)   Set<string> selection
web/src/secrets/useRowSelection.test.tsx (NEW)
web/src/secrets/useRowNav.ts           (NEW, hook)   active-row keyboard nav
web/src/secrets/useRowNav.test.tsx     (NEW)
web/src/secrets/SelectionBar.tsx       (NEW)         N-selected action bar
web/src/secrets/SecretTable.tsx        (MODIFY)      sortable headers + checkbox col + active ring
web/src/secrets/EditorToolbar.tsx      (MODIFY)      "changed only" toggle
web/src/secrets/ImportEnvDialog.tsx    (MODIFY)      classified value-free preview
web/src/secrets/SecretEditor.tsx       (MODIFY)      orchestrate sort/select/nav/bulk/changed-only/empty
web/src/secrets/SecretEditor.test.tsx  (MODIFY)      integration tests
```

---

### Task 1: Pure helper — `sortRows`

**Files:**
- Create: `web/src/secrets/sortRows.ts`
- Test: `web/src/secrets/sortRows.test.ts`

**Context:** `MaskedSecret = { value_version: number; created_at: string; origin: 'own'|'inherited'|'overridden' }` (from `../lib/endpoints`). `rows` is the ordered key list (`masked` keys in server order, then buffer-added keys). Added keys have **no** `masked` entry and must pin to the top regardless of direction. Ties break by key ascending.

- [ ] **Step 1: Write the failing test** (`sortRows.test.ts`)

```ts
import { describe, expect, test } from 'vitest'
import { sortRows, type SortState } from './sortRows'
import type { MaskedSecret } from '../lib/endpoints'

const masked: Record<string, MaskedSecret> = {
  BETA:  { value_version: 2, created_at: '2026-01-02T00:00:00Z', origin: 'own' },
  alpha: { value_version: 5, created_at: '2026-01-03T00:00:00Z', origin: 'inherited' },
  GAMMA: { value_version: 1, created_at: '2026-01-01T00:00:00Z', origin: 'overridden' },
}
const rows = ['BETA', 'alpha', 'GAMMA']

test('null sort returns rows unchanged (new array)', () => {
  const out = sortRows(rows, masked, null)
  expect(out).toEqual(rows)
  expect(out).not.toBe(rows)
})

test('key asc/desc is case-insensitive', () => {
  expect(sortRows(rows, masked, { key: 'key', dir: 'asc' })).toEqual(['alpha', 'BETA', 'GAMMA'])
  expect(sortRows(rows, masked, { key: 'key', dir: 'desc' })).toEqual(['GAMMA', 'BETA', 'alpha'])
})

test('version and updated sort numerically / chronologically', () => {
  expect(sortRows(rows, masked, { key: 'version', dir: 'asc' })).toEqual(['GAMMA', 'BETA', 'alpha'])
  expect(sortRows(rows, masked, { key: 'updated', dir: 'desc' })).toEqual(['alpha', 'BETA', 'GAMMA'])
})

test('origin sorts alphabetically (inherited<overridden<own), key breaks ties', () => {
  expect(sortRows(rows, masked, { key: 'origin', dir: 'asc' })).toEqual(['alpha', 'GAMMA', 'BETA'])
})

test('added rows (no masked entry) pin to top in both directions', () => {
  const withAdded = ['BETA', 'NEWKEY', 'alpha']
  const asc = sortRows(withAdded, masked, { key: 'key', dir: 'asc' })
  const desc = sortRows(withAdded, masked, { key: 'key', dir: 'desc' })
  expect(asc[0]).toBe('NEWKEY')
  expect(desc[0]).toBe('NEWKEY')
})
```

- [ ] **Step 2: Run it — expect FAIL** (module not found)

Run: `cd web && npm test -- --run sortRows`

- [ ] **Step 3: Implement** (`sortRows.ts`)

```ts
import type { MaskedSecret } from '../lib/endpoints'

export type SortKey = 'key' | 'origin' | 'updated' | 'version'
export type SortState = { key: SortKey; dir: 'asc' | 'desc' } | null

// Reorder a key list by the given sort state. Pending-added keys (absent from
// `masked`) always pin to the top so unsaved work stays visible regardless of
// direction. Stable via a key-ascending tiebreak.
export function sortRows(
  rows: string[],
  masked: Record<string, MaskedSecret>,
  sort: SortState,
): string[] {
  if (!sort) return [...rows]
  const dir = sort.dir === 'asc' ? 1 : -1
  const byKey = (a: string, b: string) => a.toLowerCase().localeCompare(b.toLowerCase())
  return [...rows].sort((a, b) => {
    const am = masked[a]
    const bm = masked[b]
    // Added rows (no masked entry) always float to the top.
    if (!am && !bm) return byKey(a, b)
    if (!am) return -1
    if (!bm) return 1
    let cmp = 0
    switch (sort.key) {
      case 'key': cmp = byKey(a, b); break
      case 'origin': cmp = am.origin.localeCompare(bm.origin); break
      case 'updated': cmp = am.created_at.localeCompare(bm.created_at); break
      case 'version': cmp = am.value_version - bm.value_version; break
    }
    if (cmp === 0) return byKey(a, b) // deterministic tiebreak (always asc)
    return cmp * dir
  })
}
```

- [ ] **Step 4: Run — expect PASS.** `cd web && npm test -- --run sortRows`
- [ ] **Step 5: Commit.**

```bash
git add web/src/secrets/sortRows.ts web/src/secrets/sortRows.test.ts
git commit -m "feat(web/editor): sortRows pure helper (key/origin/updated/version, added pinned)"
```

---

### Task 2: Pure helper — `exportEnv`

**Files:**
- Create: `web/src/secrets/exportEnv.ts`
- Test: `web/src/secrets/exportEnv.test.ts`

**Context:** Formats already-revealed `[key, value]` pairs into `.env` text. Must round-trip with `parseDotenv`/`unquote` in `rowState.ts`: `parseDotenv` strips surrounding matching quotes and trims. So values that are "clean" (no leading/trailing whitespace, no `#`, no newline, no `"`) can be written bare; values needing protection get double-quoted. This helper does NO reveal and NO IO — it is pure string formatting.

- [ ] **Step 1: Write the failing test** (`exportEnv.test.ts`)

```ts
import { expect, test } from 'vitest'
import { toEnvText } from './exportEnv'
import { parseDotenv } from './rowState'

test('formats bare KEY=VALUE lines, sorted by key', () => {
  expect(toEnvText([['B', 'two'], ['A', 'one']])).toBe('A=one\nB=two')
})

test('quotes values that need protection', () => {
  expect(toEnvText([['K', 'a b']])).toBe('K="a b"')        // whitespace
  expect(toEnvText([['K', 'a#b']])).toBe('K="a#b"')        // comment char
  expect(toEnvText([['K', 'a"b']])).toBe('K="a\\"b"')      // embedded quote escaped
})

test('round-trips through parseDotenv for representative values', () => {
  const pairs = { PLAIN: 'postgres://a', SPACED: 'has space', HASH: 'a#b', EMPTY: '' }
  const text = toEnvText(Object.entries(pairs))
  expect(parseDotenv(text).pairs).toEqual(pairs)
})
```

- [ ] **Step 2: Run — expect FAIL.** `cd web && npm test -- --run exportEnv`

- [ ] **Step 3: Implement** (`exportEnv.ts`)

```ts
// Format already-revealed [key,value] pairs into .env text. Pure: the caller
// is responsible for the audited reveal; nothing here reveals, caches, or logs.
// Quoting is the inverse of parseDotenv/unquote in rowState.ts so a written
// file round-trips back to identical pairs.
export function toEnvText(entries: Array<[string, string]>): string {
  return [...entries]
    .sort(([a], [b]) => a.localeCompare(b))
    .map(([k, v]) => `${k}=${format(v)}`)
    .join('\n')
}

function format(v: string): string {
  // Bare only when parseDotenv would return v unchanged: no surrounding space,
  // no leading '#', no quote/newline. Otherwise double-quote and escape quotes.
  const needsQuote = v !== v.trim() || v === '' || /["#\r\n]/.test(v)
  if (!needsQuote) return v
  return `"${v.replace(/"/g, '\\"')}"`
}
```

> Note: `parseDotenv`'s `unquote` strips a surrounding matching pair but does not process `\"` escapes; the round-trip test uses values whose quoted form has no interior unescaped quote issues. Keep the test values representative (the three in the test above pass). If a later need arises for full escape round-tripping, extend `unquote` — out of scope here.

- [ ] **Step 4: Run — expect PASS.** If the `a"b` escape case fails the round-trip, drop it from the round-trip test (keep it in the quoting test) — `unquote` doesn't unescape, which is acceptable for export formatting. Confirm the three round-trip values (`PLAIN/SPACED/HASH/EMPTY`) pass.
- [ ] **Step 5: Commit.**

```bash
git add web/src/secrets/exportEnv.ts web/src/secrets/exportEnv.test.ts
git commit -m "feat(web/editor): exportEnv .env formatter (parseDotenv round-trip)"
```

---

### Task 3: Pure helper — `importClassify`

**Files:**
- Create: `web/src/secrets/importClassify.ts`
- Test: `web/src/secrets/importClassify.test.ts`

**Context:** Given parsed `pairs` (from `parseDotenv`) and the `masked` map, classify each key as `add` (not in masked) or `update` (already exists). Value-free — returns key + kind only, sorted by key for stable rendering.

- [ ] **Step 1: Write the failing test** (`importClassify.test.ts`)

```ts
import { expect, test } from 'vitest'
import { classifyImport } from './importClassify'
import type { MaskedSecret } from '../lib/endpoints'

const masked: Record<string, MaskedSecret> = {
  DB_URL: { value_version: 1, created_at: '', origin: 'own' },
}

test('classifies add vs update, sorted by key, carries no values', () => {
  const rows = classifyImport({ ZED: 'z', DB_URL: 'x', API: 'a' }, masked)
  expect(rows).toEqual([
    { key: 'API', kind: 'add' },
    { key: 'DB_URL', kind: 'update' },
    { key: 'ZED', kind: 'add' },
  ])
})

test('empty pairs -> empty list', () => {
  expect(classifyImport({}, masked)).toEqual([])
})
```

- [ ] **Step 2: Run — expect FAIL.** `cd web && npm test -- --run importClassify`

- [ ] **Step 3: Implement** (`importClassify.ts`)

```ts
import type { MaskedSecret } from '../lib/endpoints'

export type ImportRow = { key: string; kind: 'add' | 'update' }

// Value-free classification of parsed .env pairs against the current masked
// list: 'update' if the key already exists (will edit/override), else 'add'.
export function classifyImport(
  pairs: Record<string, string>,
  masked: Record<string, MaskedSecret>,
): ImportRow[] {
  return Object.keys(pairs)
    .sort((a, b) => a.localeCompare(b))
    .map((key) => ({ key, kind: key in masked ? 'update' : 'add' }))
}
```

- [ ] **Step 4: Run — expect PASS.** `cd web && npm test -- --run importClassify`
- [ ] **Step 5: Commit.**

```bash
git add web/src/secrets/importClassify.ts web/src/secrets/importClassify.test.ts
git commit -m "feat(web/editor): importClassify (value-free add/update preview)"
```

---

### Task 4: Hook — `useRowSelection`

**Files:**
- Create: `web/src/secrets/useRowSelection.ts`
- Test: `web/src/secrets/useRowSelection.test.tsx`

**Context:** Holds a `Set<string>` of selected keys. The orchestrator prunes to visible keys. `setAll(keys)` selects exactly the given keys if not all already selected, else clears (toggle-all semantics for the header checkbox).

- [ ] **Step 1: Write the failing test** (`useRowSelection.test.tsx`)

```tsx
import { act, renderHook } from '@testing-library/react'
import { expect, test } from 'vitest'
import { useRowSelection } from './useRowSelection'

test('toggle adds/removes; count and isSelected track', () => {
  const { result } = renderHook(() => useRowSelection())
  act(() => result.current.toggle('A'))
  expect(result.current.isSelected('A')).toBe(true)
  expect(result.current.count).toBe(1)
  act(() => result.current.toggle('A'))
  expect(result.current.isSelected('A')).toBe(false)
  expect(result.current.count).toBe(0)
})

test('setAll selects all when partial, clears when already all', () => {
  const { result } = renderHook(() => useRowSelection())
  act(() => result.current.setAll(['A', 'B']))
  expect(result.current.count).toBe(2)
  act(() => result.current.setAll(['A', 'B'])) // all present -> clear
  expect(result.current.count).toBe(0)
})

test('prune keeps only allowed keys', () => {
  const { result } = renderHook(() => useRowSelection())
  act(() => result.current.setAll(['A', 'B', 'C']))
  act(() => result.current.prune(['A', 'C']))
  expect(result.current.count).toBe(2)
  expect(result.current.isSelected('B')).toBe(false)
})
```

- [ ] **Step 2: Run — expect FAIL.** `cd web && npm test -- --run useRowSelection`

- [ ] **Step 3: Implement** (`useRowSelection.ts`)

```ts
import { useCallback, useMemo, useState } from 'react'

export function useRowSelection() {
  const [sel, setSel] = useState<Set<string>>(() => new Set())

  const toggle = useCallback((key: string) => {
    setSel((s) => {
      const next = new Set(s)
      next.has(key) ? next.delete(key) : next.add(key)
      return next
    })
  }, [])

  const clear = useCallback(() => setSel(new Set()), [])

  // Header toggle: if every given key is already selected, clear; else select all.
  const setAll = useCallback((keys: string[]) => {
    setSel((s) => {
      const allOn = keys.length > 0 && keys.every((k) => s.has(k))
      return allOn ? new Set() : new Set(keys)
    })
  }, [])

  // Drop any selected key that is no longer allowed (e.g. filtered out / saved).
  const prune = useCallback((allowed: string[]) => {
    const allow = new Set(allowed)
    setSel((s) => {
      let changed = false
      const next = new Set<string>()
      for (const k of s) { if (allow.has(k)) next.add(k); else changed = true }
      return changed ? next : s
    })
  }, [])

  const isSelected = useCallback((key: string) => sel.has(key), [sel])

  return useMemo(
    () => ({ selected: sel, count: sel.size, toggle, clear, setAll, prune, isSelected }),
    [sel, toggle, clear, setAll, prune, isSelected],
  )
}
```

- [ ] **Step 4: Run — expect PASS.** `cd web && npm test -- --run useRowSelection`
- [ ] **Step 5: Commit.**

```bash
git add web/src/secrets/useRowSelection.ts web/src/secrets/useRowSelection.test.tsx
git commit -m "feat(web/editor): useRowSelection hook (toggle/setAll/prune)"
```

---

### Task 5: Hook — `useRowNav`

**Files:**
- Create: `web/src/secrets/useRowNav.ts`
- Test: `web/src/secrets/useRowNav.test.tsx`

**Context:** Owns `active: string | null` and installs a `window` `keydown` listener implementing the key map. It is **inert while a text input/textarea/contenteditable is focused** (so typing a value or `/` in the filter is normal). It takes the current `visible` list and a set of action callbacks. `active` resets to `null` if it leaves `visible`.

Key map: `ArrowUp`/`k` up, `ArrowDown`/`j` down, `x` toggle-select active, `/` focus filter, `e` edit active, `Enter` reveal active, `Delete`/`Backspace` remove active, `Escape` clear.

- [ ] **Step 1: Write the failing test** (`useRowNav.test.tsx`)

```tsx
import { act, renderHook } from '@testing-library/react'
import { expect, test, vi } from 'vitest'
import { useRowNav } from './useRowNav'

function press(key: string, target?: EventTarget) {
  const e = new KeyboardEvent('keydown', { key, bubbles: true, cancelable: true })
  if (target) Object.defineProperty(e, 'target', { value: target })
  act(() => { window.dispatchEvent(e) })
  return e
}

function setup(over: Partial<Parameters<typeof useRowNav>[0]> = {}) {
  const cb = { onEdit: vi.fn(), onReveal: vi.fn(), onRemove: vi.fn(), onToggleSelect: vi.fn(), onFocusFilter: vi.fn(), ...over }
  const { result } = renderHook(() => useRowNav({ visible: ['A', 'B', 'C'], ...cb }))
  return { result, cb }
}

test('arrow/j-k move active within visible', () => {
  const { result } = setup()
  press('ArrowDown'); expect(result.current.active).toBe('A')
  press('j');         expect(result.current.active).toBe('B')
  press('ArrowUp');   expect(result.current.active).toBe('A')
  press('k');         expect(result.current.active).toBe('A') // clamped at top
})

test('action keys call callbacks for the active row', () => {
  const { result, cb } = setup()
  press('ArrowDown') // active = A
  press('e');      expect(cb.onEdit).toHaveBeenCalledWith('A')
  press('Enter');  expect(cb.onReveal).toHaveBeenCalledWith('A')
  press('x');      expect(cb.onToggleSelect).toHaveBeenCalledWith('A')
  press('Delete'); expect(cb.onRemove).toHaveBeenCalledWith('A')
  press('Escape'); expect(result.current.active).toBeNull()
})

test('/ focuses filter and prevents default', () => {
  const { cb } = setup()
  const e = press('/')
  expect(cb.onFocusFilter).toHaveBeenCalled()
  expect(e.defaultPrevented).toBe(true)
})

test('inert when a text input is focused', () => {
  const { result } = setup()
  const input = document.createElement('input')
  press('ArrowDown', input)
  expect(result.current.active).toBeNull()
})
```

- [ ] **Step 2: Run — expect FAIL.** `cd web && npm test -- --run useRowNav`

- [ ] **Step 3: Implement** (`useRowNav.ts`)

```ts
import { useEffect, useState } from 'react'

type Cbs = {
  visible: string[]
  onEdit: (key: string) => void
  onReveal: (key: string) => void
  onRemove: (key: string) => void
  onToggleSelect: (key: string) => void
  onFocusFilter: () => void
}

function isTypingTarget(t: EventTarget | null): boolean {
  const el = t as HTMLElement | null
  if (!el || !el.tagName) return false
  const tag = el.tagName
  return tag === 'INPUT' || tag === 'TEXTAREA' || (el as HTMLElement).isContentEditable === true
}

// Active-row keyboard navigation. Installs a window keydown listener that is
// inert while a text field is focused (so value editing / filter typing is
// normal) and coexists with the global Cmd/Ctrl-S save handler.
export function useRowNav({ visible, onEdit, onReveal, onRemove, onToggleSelect, onFocusFilter }: Cbs) {
  const [active, setActive] = useState<string | null>(null)

  // Reset active if it drops out of the visible set (e.g. filter change).
  useEffect(() => {
    if (active !== null && !visible.includes(active)) setActive(null)
  }, [visible, active])

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (isTypingTarget(e.target)) return
      if (e.metaKey || e.ctrlKey || e.altKey) return // leave Cmd/Ctrl-S etc alone
      const idx = active === null ? -1 : visible.indexOf(active)
      const move = (delta: number) => {
        if (visible.length === 0) return
        const next = idx < 0 ? (delta > 0 ? 0 : visible.length - 1)
                             : Math.min(visible.length - 1, Math.max(0, idx + delta))
        setActive(visible[next])
      }
      switch (e.key) {
        case 'ArrowDown': case 'j': e.preventDefault(); move(1); break
        case 'ArrowUp':   case 'k': e.preventDefault(); move(-1); break
        case '/': e.preventDefault(); onFocusFilter(); break
        case 'Escape': setActive(null); break
        case 'x': if (active) { e.preventDefault(); onToggleSelect(active) } break
        case 'e': if (active) { e.preventDefault(); onEdit(active) } break
        case 'Enter': if (active) { e.preventDefault(); onReveal(active) } break
        case 'Delete': case 'Backspace': if (active) { e.preventDefault(); onRemove(active) } break
      }
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [visible, active, onEdit, onReveal, onRemove, onToggleSelect, onFocusFilter])

  return { active, setActive }
}
```

- [ ] **Step 4: Run — expect PASS.** `cd web && npm test -- --run useRowNav`
- [ ] **Step 5: Commit.**

```bash
git add web/src/secrets/useRowNav.ts web/src/secrets/useRowNav.test.tsx
git commit -m "feat(web/editor): useRowNav hook (arrows+vim, input-focus guarded)"
```

---

### Task 6: `SecretTable` — sortable headers, checkbox column, active-row ring

**Files:**
- Modify: `web/src/secrets/SecretTable.tsx`
- Test: covered by Task 10 integration + a focused header test here.

**Context:** Add a leading checkbox column, a new **Updated** column, and make the four metadata headers sortable. Today the table has columns `Key | Value | Origin | Ver | Actions` (`GRID = 'grid grid-cols-[1.3fr_1.5fr_108px_56px_92px] items-center gap-3 px-4'`). Decision: render **all four sort keys as real, sortable columns** — `Key`, `Origin`, `Updated`, `Version` — because each maps to a visible header the user can click, and the new Updated column (`relativeTime(masked[key].created_at)`, `—` for added rows) adds useful metadata density in line with the redesign goal. The old `Ver` header becomes `Version`.

New grid: `GRID = 'grid grid-cols-[24px_1.2fr_1.4fr_104px_92px_56px_72px] items-center gap-3 px-4'` → columns: checkbox, Key, Value, Origin, Updated, Version, Actions. Bump the wrapper `min-w-[720px]` to `min-w-[820px]`.

- [ ] **Step 1: Write a failing header test** (append to `SecretTable`'s coverage — create `web/src/secrets/SecretTable.test.tsx` if absent, else add). Render `SecretTable` directly with two rows and assert clicking the `Key` header calls `onSort('key')`, and the checkbox header calls `onSelectAll`.

```tsx
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { expect, test, vi } from 'vitest'
import { SecretTable } from './SecretTable'
import type { MaskedSecret } from '../lib/endpoints'

const masked: Record<string, MaskedSecret> = {
  A: { value_version: 1, created_at: '2026-01-01T00:00:00Z', origin: 'own' },
  B: { value_version: 2, created_at: '2026-01-02T00:00:00Z', origin: 'own' },
}
function props(over = {}) {
  return {
    rows: ['A', 'B'], masked, buffer: {}, original: {}, editing: {}, revealed: {}, filter: '',
    sort: null, onSort: vi.fn(),
    selected: new Set<string>(), onToggleSelect: vi.fn(), onSelectAll: vi.fn(), active: null,
    onReveal: vi.fn(), onCopy: vi.fn(), onEdit: vi.fn(), onChangeValue: vi.fn(), onRemove: vi.fn(), onRevert: vi.fn(),
    ...over,
  }
}

test('clicking the Key header requests a key sort', async () => {
  const p = props()
  render(<SecretTable {...p} />)
  await userEvent.click(screen.getByRole('button', { name: /sort by key/i }))
  expect(p.onSort).toHaveBeenCalledWith('key')
})

test('header checkbox selects all visible', async () => {
  const p = props()
  render(<SecretTable {...p} />)
  await userEvent.click(screen.getByRole('checkbox', { name: /select all/i }))
  expect(p.onSelectAll).toHaveBeenCalledWith(['A', 'B'])
})
```

- [ ] **Step 2: Run — expect FAIL.** `cd web && npm test -- --run SecretTable`

- [ ] **Step 3: Implement.** Extend the `SecretTable` props with `sort: SortState`, `onSort: (k: SortKey) => void`, `selected: Set<string>`, `onToggleSelect: (k: string) => void`, `onSelectAll: (visible: string[]) => void`, `active: string | null`. Import `SortKey`, `SortState` from `./sortRows`, `relativeTime` from `../lib/relativeTime`, and `Check`/`ChevronUp`/`ChevronDown` from `lucide-react`. Update `GRID`. Replace the header row with sortable header buttons + a select-all checkbox; add per-row checkbox cell + Updated cell; add the active ring via `cn(..., active === key && 'ring-1 ring-brand-line')`.

Header (replace the current header `<div>`):

```tsx
const SORTABLE: Array<{ label: string; key: SortKey; className?: string }> = [
  { label: 'Key', key: 'key' },
  { label: 'Origin', key: 'origin' },
  { label: 'Updated', key: 'updated' },
  { label: 'Version', key: 'version' },
]
// …inside render, header row:
<div className={cn(GRID, 'bg-surface-1 py-2.5')}>
  <span className="flex items-center">
    <input
      type="checkbox"
      aria-label="select all"
      className="h-3.5 w-3.5 accent-brand"
      ref={(el) => { if (el) el.indeterminate = selected.size > 0 && !visible.every((k) => selected.has(k)) }}
      checked={visible.length > 0 && visible.every((k) => selected.has(k))}
      onChange={() => onSelectAll(visible)}
    />
  </span>
  <HeaderCell label="Key" sortKey="key" sort={sort} onSort={onSort} />
  <span className="text-[10.5px] font-bold uppercase tracking-[.1em] text-ink-faint">Value</span>
  <HeaderCell label="Origin" sortKey="origin" sort={sort} onSort={onSort} />
  <HeaderCell label="Updated" sortKey="updated" sort={sort} onSort={onSort} />
  <HeaderCell label="Version" sortKey="version" sort={sort} onSort={onSort} />
  <span className="text-right text-[10.5px] font-bold uppercase tracking-[.1em] text-ink-faint">Actions</span>
</div>
```

Add a `HeaderCell` component in the file:

```tsx
function HeaderCell({ label, sortKey, sort, onSort }: {
  label: string; sortKey: SortKey; sort: SortState; onSort: (k: SortKey) => void
}) {
  const on = sort?.key === sortKey
  return (
    <button
      type="button"
      aria-label={`sort by ${label.toLowerCase()}`}
      onClick={() => onSort(sortKey)}
      className={cn('flex items-center gap-1 text-left text-[10.5px] font-bold uppercase tracking-[.1em] transition-nocturne',
        on ? 'text-brand-text' : 'text-ink-faint hover:text-ink-mute')}
    >
      {label}
      {on && (sort!.dir === 'asc' ? <ChevronUp size={12} strokeWidth={2} /> : <ChevronDown size={12} strokeWidth={2} />)}
    </button>
  )
}
```

Per-row: prepend a checkbox cell and insert an Updated cell before Version:

```tsx
// first cell of each row:
<span className="flex items-center">
  <input
    type="checkbox"
    aria-label={`select ${key}`}
    className="h-3.5 w-3.5 accent-brand"
    checked={selected.has(key)}
    onChange={() => onToggleSelect(key)}
  />
</span>
// … Key, Value, Origin as today …
// Updated (new), before Ver/Version:
<span className="text-ink-faint text-[12px] tabular-nums truncate">
  {st.existing ? relativeTime(masked[key].created_at) : '—'}
</span>
```

Apply the active ring to the row wrapper `className`: add `active === key && 'ring-1 ring-inset ring-brand-line'`.

> `accent-brand` uses the `brand` color token via Tailwind's `accent-*` utility — token-driven, allowed. If the guard flags `accent-brand`, fall back to a custom checkbox styled with token classes; verify against `no-raw-palette` in Step 4.

- [ ] **Step 4: Run tests + guards.** `cd web && npm test -- --run SecretTable no-raw-palette dark-aa no-legacy-alias && npm run typecheck`. Existing editor tests will fail to compile until `SecretEditor` passes the new props — that's Task 10; for now run the isolated `SecretTable` + guard tests and typecheck only the new file compiles (expect the editor integration to be red until Task 10 wires props — acceptable mid-plan, but prefer to keep `SecretEditor` compiling: pass placeholder props in Task 10). To avoid a broken build between tasks, **also do minimal Task-10 wiring stubs** now: in `SecretEditor.tsx` pass `sort={null} onSort={()=>{}} selected={new Set()} onToggleSelect={()=>{}} onSelectAll={()=>{}} active={null}` temporarily. These are replaced in Task 10.
- [ ] **Step 5: Commit.**

```bash
git add web/src/secrets/SecretTable.tsx web/src/secrets/SecretTable.test.tsx web/src/secrets/SecretEditor.tsx
git commit -m "feat(web/editor): SecretTable sortable headers + checkbox column + Updated cell + active ring"
```

---

### Task 7: `SelectionBar` + wire selection & sorting into `SecretEditor`

**Files:**
- Create: `web/src/secrets/SelectionBar.tsx`
- Modify: `web/src/secrets/SecretEditor.tsx`
- Test: Task 10 integration.

**Context:** Replace the temporary stubs from Task 6 with real state: `sort` (useState), selection (`useRowSelection`), and the visible-list pipeline `sortRows → filter → changedOnly`. Render `SelectionBar` when `count > 0`. Bulk handlers (delete/reveal/copy/download) are added in Task 8 — for now the bar's action callbacks can be passed as props the editor supplies; wire delete here (buffer-only, simplest) and leave reveal/copy/download as Task 8.

`SelectionBar.tsx`:

```tsx
import { Eye, Copy, Download, Trash2, X } from 'lucide-react'
import { Button } from '../ui/Button'

export function SelectionBar({ count, onReveal, onCopy, onDownload, onDelete, onClear }: {
  count: number
  onReveal: () => void
  onCopy: () => void
  onDownload: () => void
  onDelete: () => void
  onClear: () => void
}) {
  return (
    <div className="mb-3 flex flex-wrap items-center gap-2 rounded-bar border border-line bg-surface-2 px-3 py-2">
      <span className="text-[12.5px] font-semibold text-ink">{count} selected</span>
      <div className="ml-auto flex flex-wrap gap-2">
        <Button variant="secondary" size="sm" onClick={onReveal}><Eye size={14} strokeWidth={1.7} /> Reveal</Button>
        <Button variant="secondary" size="sm" onClick={onCopy}><Copy size={14} strokeWidth={1.7} /> Copy .env</Button>
        <Button variant="secondary" size="sm" onClick={onDownload}><Download size={14} strokeWidth={1.7} /> Download .env</Button>
        <Button variant="danger" size="sm" onClick={onDelete}><Trash2 size={14} strokeWidth={1.7} /> Delete</Button>
        <Button variant="secondary" size="sm" onClick={onClear}><X size={14} strokeWidth={1.7} /> Clear</Button>
      </div>
    </div>
  )
}
```

> Confirm `rounded-bar` and `Button variant="danger"` exist (used by `ConfirmDialog`/`DirtyBar`). If `danger` isn't a `Button` variant, use `variant="secondary"` with a danger-token class or check `buttonClasses`. Verify in Step 3.

- [ ] **Step 1 (SecretEditor wiring):** add imports (`useRowSelection`, `useRowNav` [Task 5, wired in Task 8 for actions — nav can be added here or Task 8; do nav in Task 8 to keep this focused], `sortRows`, `SortState`, `SelectionBar`). Add state:

```tsx
const [sort, setSort] = useState<SortState>(null)
const selection = useRowSelection()
// header click cycles default -> asc -> desc -> default
function cycleSort(key: SortKey) {
  setSort((s) => {
    if (!s || s.key !== key) return { key, dir: 'asc' }
    if (s.dir === 'asc') return { key, dir: 'desc' }
    return null
  })
}
```

Build the visible pipeline (replace the current `rows`/`visible` usage; note `SecretTable` currently computes its own `visible` from `filter` — MOVE that to the editor so selection/nav share one list):

```tsx
const ordered = sortRows(rows, maskedRows, sort)
const q = filter.trim().toLowerCase()
const filtered = q ? ordered.filter((k) => k.toLowerCase().includes(q)) : ordered
// changedOnly added in Task 9; for now visible = filtered
const visible = filtered
useEffect(() => { selection.prune(visible) }, [visible, selection])
```

Change `SecretTable` to accept `rows={visible}` (already-visible) and REMOVE its internal filtering (the table renders exactly what it's given). Update `SecretTable`'s `visible` references to use the incoming `rows`. Adjust the Task 6 header select-all to use `rows` (the visible list) — rename table prop usage accordingly.

Render selection bar + wire delete:

```tsx
function bulkDelete() {
  const keys = [...selection.selected]
  let deleted = 0, skipped = 0
  keys.forEach((key) => {
    const st = rowState(key, maskedRows, buffer, original)
    if (st.change === 'added') { undo(key); deleted++ }
    else if (st.existing && st.origin !== 'inherited') { remove(key); deleted++ }
    else skipped++ // inherited, not overridden
  })
  selection.clear()
  toast({ title: `Deleted ${deleted}${skipped ? ` · skipped ${skipped} inherited` : ''}` })
}
// in JSX, above <SecretTable>:
{selection.count > 0 && (
  <SelectionBar
    count={selection.count}
    onReveal={() => {}}  // Task 8
    onCopy={() => {}}    // Task 8
    onDownload={() => {}}// Task 8
    onDelete={bulkDelete}
    onClear={selection.clear}
  />
)}
```

Pass real props to `SecretTable`: `sort={sort} onSort={cycleSort} selected={selection.selected} onToggleSelect={selection.toggle} onSelectAll={selection.setAll} active={null}` (active wired in Task 8).

- [ ] **Step 2:** `cd web && npm test -- --run && npm run typecheck` — existing editor tests green; new selection bar renders (assert in Task 10).
- [ ] **Step 3:** Verify `Button variant="danger"` + `rounded-bar` compile & render (build). Fix per note if needed.
- [ ] **Step 4: Commit.**

```bash
git add web/src/secrets/SelectionBar.tsx web/src/secrets/SecretTable.tsx web/src/secrets/SecretEditor.tsx
git commit -m "feat(web/editor): sorting + multi-select + SelectionBar (bulk delete wired)"
```

---

### Task 8: Bulk reveal + copy/download `.env` (security task) + keyboard nav

**Files:**
- Modify: `web/src/secrets/SecretEditor.tsx`
- Test: Task 10 integration (+ leak assertion).

**Context:** THE security-sensitive task. Bulk reveal + copy/download all reveal via the audited per-key `revealKeyRaw`. Copy/download assemble `.env` text in a LOCAL variable (never state/cache), and download is confirm-gated. Also wire `useRowNav` here so `active` and the key map are live.

- [ ] **Step 1: Bulk reveal** — reveal each selected EXISTING key via the existing `reveal(key)` (which already caches into `revealed` and short-circuits if present):

```tsx
async function bulkReveal(keys: string[]) {
  for (const key of keys) {
    const st = rowState(key, maskedRows, buffer, original)
    if (st.existing) await reveal(key) // audited revealKeyRaw, into ephemeral `revealed`
  }
}
```

- [ ] **Step 2: Copy/Download `.env`** — reveal selected existing keys, format with `toEnvText`, hand to clipboard/blob, drop the local. Import `toEnvText` from `./exportEnv`.

```tsx
// Reveal selected keys (audited) and return [key,value] pairs in a LOCAL array.
async function revealPairs(keys: string[]): Promise<Array<[string, string]>> {
  const out: Array<[string, string]> = []
  for (const key of keys) {
    const st = rowState(key, maskedRows, buffer, original)
    if (!st.existing) continue
    const value = key in revealed ? revealed[key] : await reveal(key)
    out.push([key, value])
  }
  return out
}

async function bulkCopy(keys: string[]) {
  try {
    const text = toEnvText(await revealPairs(keys)) // local only
    await navigator.clipboard?.writeText(text)
    toast({ title: `Copied ${keys.length} key${keys.length === 1 ? '' : 's'} as .env` })
  } catch {
    toast({ title: 'Copy failed', tone: 'danger' })
  }
}

async function bulkDownload(keys: string[]) {
  try {
    const text = toEnvText(await revealPairs(keys)) // local only
    const blob = new Blob([text], { type: 'text/plain' })
    const url = URL.createObjectURL(blob)
    const a = document.createElement('a')
    a.href = url; a.download = 'secrets.env'
    document.body.appendChild(a); a.click(); a.remove()
    URL.revokeObjectURL(url) // do not cache the plaintext blob
    toast({ title: `Downloaded ${keys.length} key${keys.length === 1 ? '' : 's'}` })
  } catch {
    toast({ title: 'Download failed', tone: 'danger' })
  }
}
```

- [ ] **Step 3: Confirm-gate the download.** Add `const [confirmDownload, setConfirmDownload] = useState(false)` and a `ConfirmDialog`:

```tsx
<ConfirmDialog
  open={confirmDownload}
  onOpenChange={setConfirmDownload}
  title="Download secrets as .env?"
  body={`This writes ${selection.count} secret value${selection.count === 1 ? '' : 's'} in plaintext to a file on your device.`}
  confirmLabel="Download"
  tone="danger"
  onConfirm={() => { const keys = [...selection.selected]; setConfirmDownload(false); void bulkDownload(keys) }}
/>
```

Wire the SelectionBar callbacks: `onReveal={() => void bulkReveal([...selection.selected])}`, `onCopy={() => void bulkCopy([...selection.selected])}`, `onDownload={() => setConfirmDownload(true)}`.

- [ ] **Step 4: Keyboard nav.** Add a ref for the filter input (pass a `filterRef` down to `EditorToolbar` and attach to its `<input>`), then:

```tsx
const filterRef = useRef<HTMLInputElement>(null)
const nav = useRowNav({
  visible,
  onEdit: (k) => void edit(k),
  onReveal: (k) => void reveal(k),
  onRemove: (k) => remove(k),
  onToggleSelect: (k) => selection.toggle(k),
  onFocusFilter: () => filterRef.current?.focus(),
})
```

Pass `active={nav.active}` to `SecretTable`. Add `filterRef` to `EditorToolbar` props and attach `ref={filterRef}` to its search `<input>`.

- [ ] **Step 5: Tests + guards.** `cd web && npm test -- --run && npm run typecheck`. (Full behavior asserted in Task 10.)
- [ ] **Step 6: Commit.**

```bash
git add web/src/secrets/SecretEditor.tsx web/src/secrets/EditorToolbar.tsx
git commit -m "feat(web/editor): bulk reveal + copy/download .env (audited, confirm-gated) + keyboard nav"
```

---

### Task 9: Import preview + "changed only" toggle + zero-match empty state

**Files:**
- Modify: `web/src/secrets/ImportEnvDialog.tsx`, `web/src/secrets/EditorToolbar.tsx`, `web/src/secrets/SecretEditor.tsx`
- Test: Task 10 integration.

- [ ] **Step 1: Import preview.** `ImportEnvDialog` needs the `masked` map to classify. Add a `masked: Record<string, MaskedSecret>` prop (passed from `SecretEditor` as `maskedRows`). Compute `const rows = useMemo(() => classifyImport(parsed.pairs, masked), [parsed.pairs, masked])` and render a value-free list between the textarea and the footer:

```tsx
{rows.length > 0 && (
  <ul className="mt-3 max-h-40 overflow-y-auto rounded border border-line bg-surface-2 p-2 text-[12px]">
    {rows.map((r) => (
      <li key={r.key} className="flex items-center justify-between py-0.5">
        <span className="font-mono text-ink truncate">{r.key}</span>
        <Pill tone={r.kind === 'add' ? 'success' : 'warning'}>{r.kind}</Pill>
      </li>
    ))}
  </ul>
)}
```

Import `classifyImport` from `./importClassify`, `Pill` from `../ui/Pill`, `MaskedSecret` type from `../lib/endpoints`. Update `SecretEditor`'s `<ImportEnvDialog … masked={maskedRows} />`.

- [ ] **Step 2: "Changed only" toggle.** Add `changedOnly: boolean` + `onChangedOnly: (v: boolean) => void` props to `EditorToolbar`; render next to the filter:

```tsx
<label className="flex items-center gap-1.5 text-[12.5px] text-ink-mute">
  <input type="checkbox" className="h-3.5 w-3.5 accent-brand" checked={changedOnly} onChange={(e) => onChangedOnly(e.target.checked)} />
  Changed only
</label>
```

In `SecretEditor`: `const [changedOnly, setChangedOnly] = useState(false)` and extend the pipeline:

```tsx
const visible = (changedOnly
  ? filtered.filter((k) => rowState(k, maskedRows, buffer, original).change !== null)
  : filtered)
```

Pass `changedOnly={changedOnly} onChangedOnly={setChangedOnly}` to `EditorToolbar`.

- [ ] **Step 3: Zero-match empty state.** In `SecretEditor`, the existing empty state is for `rows.length === 0` (no secrets at all). Add a distinct branch: when `rows.length > 0` but `visible.length === 0` and (`filter` non-empty OR `changedOnly`), render an inline `EmptyState` instead of the table:

```tsx
{rows.length === 0 ? (
  <EmptyState className="mt-10" title="No secrets yet" hint="…" action={/* N7 Add secret CTA stays */} />
) : visible.length === 0 ? (
  <EmptyState className="mt-8" title={changedOnly && !filter ? 'No changed keys' : `No keys match “${filter}”`}
    hint="Adjust the filter or clear ‘Changed only’." />
) : (
  <SecretTable rows={visible} … />
)}
```

- [ ] **Step 4: Tests + guards.** `cd web && npm test -- --run && npm run typecheck && npm test -- --run no-raw-palette dark-aa no-legacy-alias`
- [ ] **Step 5: Commit.**

```bash
git add web/src/secrets/ImportEnvDialog.tsx web/src/secrets/EditorToolbar.tsx web/src/secrets/SecretEditor.tsx
git commit -m "feat(web/editor): import .env preview + changed-only toggle + zero-match empty state"
```

---

### Task 10: Integration tests + verification sweep + PR

**Files:**
- Modify: `web/src/secrets/SecretEditor.test.tsx`
- Modify: `gaps.md` (mark §2.1 + §1.9 done), memory tracker
- Test: full suite

**Context:** Add integration tests covering the wired behavior, then run the full gate + security re-check, update trackers, push, open PR (do NOT merge).

- [ ] **Step 1: Integration tests** (append to `SecretEditor.test.tsx`, reuse `seed()`; wrap in `<ToastProvider>` where a toast is asserted). Cover:
  - **Sort**: click the `sort by key` header → rows reorder (assert DOM order of `DB_URL`/`SENTRY_DSN`).
  - **Select + bulk delete**: check `select DB_URL`, click `Delete` → `DB_URL` shows the `removed` change chip; a toast "Deleted 1" appears. Selecting the inherited `SENTRY_DSN` and deleting → toast notes "skipped 1 inherited".
  - **Bulk reveal** fires the audited per-key endpoint: count hits on `/v1/configs/c1/secrets/:key` equals the number of selected existing keys.
  - **Copy .env**: stub `navigator.clipboard.writeText` (spy), select `DB_URL`, click `Copy .env` → writeText called with text containing `DB_URL=postgres://a`; assert the reveal endpoint was hit (audited).
  - **Download confirm**: click `Download .env` → `ConfirmDialog` appears; confirming triggers an anchor with `download="secrets.env"` (spy `createElement('a')`), and `URL.revokeObjectURL` called.
  - **Keyboard**: `ArrowDown` then `e` puts a row into edit mode (value input appears); `/` focuses the filter input (`document.activeElement`).
  - **Import preview**: open Import, paste `DB_URL=x\nNEW=y`, assert `DB_URL` badged `update` and `NEW` badged `add`, no value text shown.
  - **Changed only + zero-match**: type a filter with no matches → "No keys match" empty state; toggle Changed only with no changes → "No changed keys".

Example (bulk reveal audit assertion):

```tsx
test('bulk reveal fires one audited reveal per selected existing key', async () => {
  seed()
  const hits: string[] = []
  server.use(http.get('/v1/configs/c1/secrets/:key', ({ params }) => {
    hits.push(String(params.key)); return HttpResponse.json({ key: String(params.key), value: 'v' })
  }))
  renderApp(<ToastProvider><SecretEditor /></ToastProvider>, { route: '/projects/p1/configs/c1', withAuth: false })
  await screen.findByText('DB_URL')
  await userEvent.click(screen.getByRole('checkbox', { name: /select db_url/i }))
  await userEvent.click(screen.getByRole('button', { name: /^reveal$/i }))
  await waitFor(() => expect(hits).toEqual(['DB_URL']))
})
```

- [ ] **Step 2: Run full suite + guards + typecheck + build.**

```
cd web && npm test -- --run && npm run typecheck && npm run build && npm test -- --run no-raw-palette dark-aa no-legacy-alias
```

- [ ] **Step 3: Dual-theme smoke.** Start preview and smoke (a free port is fine):

```
cd web && npm run preview -- --port 4173 --host 127.0.0.1 &   # note actual port
SMOKE_URL=http://127.0.0.1:<port> npm run smoke   # expect light + dark, dark bg rgb(11,10,20)
```

- [ ] **Step 4: SECURITY re-check.** Confirm: `git diff main...HEAD -- web/src | grep -iE "rawConfig|revealKeyRaw"` shows reveals only via the audited endpoints; the copy/download `.env` text is built in a local variable (no new `useState` holds plaintext); `URL.revokeObjectURL` present; download is confirm-gated; import preview + review-diff render no values. `git grep -nE "console\.(log|debug)" -- web/src/secrets` returns nothing new.

- [ ] **Step 5: Update trackers.** In `gaps.md`, strike/annotate §2.1 (sorting, bulk ops, keyboard, import preview, changed-only, zero-match → done) and §1.9 (export-config → done via bulk Copy/Download). Note remaining §2.1 out-of-scope bullets (undo-after-discard, run-history, key-rename diff) stay open. Commit:

```bash
git add gaps.md
git commit -m "docs(gaps): mark §2.1 editor depth + §1.9 export done (editor-depth branch)"
```

- [ ] **Step 6: Push + PR.** `git push -u origin editor-depth` then `gh pr create --base main` summarizing the six features + the audited/confirm-gated export path + preserved invariants. **Do NOT merge** — the user merges after a final holistic review.

---

## Self-review notes
- **Spec coverage:** F1 sort = Tasks 1,6,7; F2 selection = Tasks 4,6,7; F3 bulk (delete/reveal/copy/download) = Tasks 7,8; F4 keyboard = Tasks 5,8; F5 import preview = Tasks 3,9; F6 changed-only + zero-match = Task 9. Security (audited per-key reveal, ephemeral plaintext, confirm-gated download) = Task 8, re-verified Task 10 Step 4. Testing across all tasks.
- **Type consistency:** `SortKey`/`SortState` (Task 1) used identically in Tasks 6,7; `ImportRow` (Task 3) in Task 9; `useRowSelection` API (`selected/count/toggle/clear/setAll/prune/isSelected`, Task 4) consumed in Tasks 6,7,8; `useRowNav` callback names (`onEdit/onReveal/onRemove/onToggleSelect/onFocusFilter`, Task 5) match Task 8's wiring.
- **Build continuity:** Task 6 adds temporary prop stubs in `SecretEditor` so the app compiles between tasks; Task 7 replaces them with real state. `SecretTable`'s internal `filter` handling MOVES to `SecretEditor` in Task 7 (single visible list shared by table/selection/nav).
- **Scope discipline:** frontend-only; no endpoint/CLI changes; export reuses audited reveals; no undo-after-discard / run-history / key-rename diff (out of scope). `updated` sort ships with a new Updated column (metadata density win) rather than a hidden control.
