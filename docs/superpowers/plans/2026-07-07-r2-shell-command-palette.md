# R2 — Shell & ⌘K Command Palette Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Re-theme the app shell to the dev-focused Doppler layout and add a keyboard-driven ⌘K command palette that jumps to projects, configs, secret key names, and nav actions.

**Architecture:** A new `web/src/palette/` module owns a fuzzy matcher, a cache-sourced item index (built from already-loaded React Query data — no new fetches, secret KEY NAMES only), a Radix-Dialog command palette with arrow-key navigation, and a `PaletteProvider` exposing open state + a global ⌘K listener. The `TopBar` gains a palette trigger + a standalone theme toggle; the `Sidebar` gets a primary nav block (Projects/Activity/Members/Tokens/Settings) above the existing (now contextual) project/env/config tree, all re-themed with R1's `sidebar`/`topbar`/`elevated` tokens. A new instance-level `/audit` route gives "Activity" a home.

**Tech Stack:** React 18 + TS + Vite + Tailwind (R1 CSS-var tokens) + Radix Dialog/DropdownMenu + TanStack Query v5 + React Router v6 + lucide-react + Vitest/MSW.

**Spec:** `docs/superpowers/specs/2026-07-07-dark-redesign-design.md` (§Screen treatments 1 & 3, §Decomposition R2, hard rules). **Mockup:** `docs/design/ui-redesign-mockup.html` (sections 1 & 3).

**Branch:** create `milestone-19-r2-shell-palette` off `main` before Task 1.

**Security rule (non-negotiable):** the palette indexes secret **key names** only (from cached `['config', cid, 'masked']` metadata, which is unaudited and value-free). It NEVER reads, requests, caches, or displays secret VALUES, and never triggers an audited reveal.

---

## File structure

- Create `web/src/palette/fuzzy.ts` (+ `.test.ts`) — subsequence matcher + ranker.
- Create `web/src/palette/usePaletteItems.ts` (+ `.test.tsx`) — builds grouped `PaletteItem[]` from query cache + navigation callbacks.
- Create `web/src/palette/CommandPalette.tsx` (+ `.test.tsx`) — Radix Dialog overlay, search input, grouped results, arrow-key nav.
- Create `web/src/palette/PaletteProvider.tsx` (+ `.test.tsx`) — open-state context + global ⌘K/Ctrl+K listener; renders `CommandPalette`.
- Create `web/src/shell/ThemeToggle.tsx` — standalone sun/moon toggle button (uses `useTheme`).
- Modify `web/src/shell/TopBar.tsx` — palette trigger button + ThemeToggle.
- Modify `web/src/shell/Sidebar.tsx` — primary nav block + re-theme; keep contextual project tree.
- Modify `web/src/App.tsx` — mount `PaletteProvider` around the authed shell; add instance `/audit` route.
- Modify `fe-improvements.md` — check off R2.

---

### Task 1: Fuzzy matcher

A tiny, dependency-free subsequence matcher + ranker used to filter palette items.

**Files:** Create `web/src/palette/fuzzy.ts`; test `web/src/palette/fuzzy.test.ts`.

- [ ] **Step 1 (TDD): write `web/src/palette/fuzzy.test.ts`:**

```ts
import { expect, test } from 'vitest'
import { fuzzyScore } from './fuzzy'

test('empty query matches everything with score 0', () => {
  expect(fuzzyScore('', 'anything')).toBe(0)
})

test('substring match scores higher than scattered subsequence', () => {
  const contiguous = fuzzyScore('prod', 'production')
  const scattered = fuzzyScore('prod', 'p-r-o-d-uction')
  expect(contiguous).not.toBeNull()
  expect(scattered).not.toBeNull()
  expect((contiguous as number) > (scattered as number)).toBe(true)
})

test('prefix match scores higher than mid-string match', () => {
  const prefix = fuzzyScore('api', 'api-gateway')
  const mid = fuzzyScore('api', 'legacy-api')
  expect((prefix as number) > (mid as number)).toBe(true)
})

test('non-subsequence returns null', () => {
  expect(fuzzyScore('xyz', 'production')).toBeNull()
})

test('case-insensitive', () => {
  expect(fuzzyScore('PROD', 'production')).not.toBeNull()
})
```

Run: `npx vitest run src/palette/fuzzy.test.ts` → FAIL (module missing).

- [ ] **Step 2: implement `web/src/palette/fuzzy.ts`:**

```ts
// Subsequence fuzzy matcher. Returns null when `query` is not a subsequence of
// `target`; otherwise a score in [0,1] where 1 is best (contiguous prefix).
export function fuzzyScore(query: string, target: string): number | null {
  const q = query.trim().toLowerCase()
  if (q === '') return 0
  const t = target.toLowerCase()

  // Fast path: contiguous substring — score by earliness + tightness.
  const idx = t.indexOf(q)
  if (idx !== -1) {
    const positionBonus = 1 - idx / (t.length + 1) // earlier = higher
    const coverage = q.length / t.length
    return 0.6 + 0.25 * positionBonus + 0.15 * coverage
  }

  // Subsequence match: every query char appears in order.
  let ti = 0
  let gaps = 0
  for (let qi = 0; qi < q.length; qi++) {
    const found = t.indexOf(q[qi], ti)
    if (found === -1) return null
    if (qi > 0 && found > ti) gaps += found - ti
    ti = found + 1
  }
  // Scattered subsequence scores below any substring match (< 0.6).
  const tightness = 1 - Math.min(1, gaps / (t.length + 1))
  return 0.55 * tightness
}
```

- [ ] **Step 3: run → PASS.** `npx vitest run src/palette/fuzzy.test.ts` (5/5).
- [ ] **Step 4: commit.**
```bash
git add web/src/palette/fuzzy.ts web/src/palette/fuzzy.test.ts
git commit -m "feat(web): fuzzy subsequence matcher for command palette"
```

---

### Task 2: Palette item index from cache

A hook that assembles grouped `PaletteItem[]` from React Query's cache (no new network) plus navigation callbacks. Projects always; configs + secret KEY NAMES for the active project only (derived from URL).

**Files:** Create `web/src/palette/usePaletteItems.ts`; test `web/src/palette/usePaletteItems.test.tsx`.

- [ ] **Step 1 (TDD): write `web/src/palette/usePaletteItems.test.tsx`.** It seeds a `QueryClient` cache with the real wire shapes and asserts the produced items. Use the app's real query keys.

```tsx
import { renderHook } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { MemoryRouter } from 'react-router-dom'
import { expect, test } from 'vitest'
import { usePaletteItems, type PaletteItem } from './usePaletteItems'

function wrapper(qc: QueryClient, route: string) {
  return function W({ children }: { children: React.ReactNode }) {
    return (
      <QueryClientProvider client={qc}>
        <MemoryRouter initialEntries={[route]}>{children}</MemoryRouter>
      </QueryClientProvider>
    )
  }
}

function seed(): QueryClient {
  const qc = new QueryClient()
  qc.setQueryData(['projects'], [{ id: 'p1', slug: 'gw', name: 'api-gateway' }])
  qc.setQueryData(['envs', 'p1'], [{ id: 'e1', slug: 'dev', name: 'Development' }])
  qc.setQueryData(['configs', 'p1', 'e1'], [
    { id: 'c1', environment_id: 'e1', name: 'dev', inherits_from: null, created_at: '2026-07-01T00:00:00Z' },
  ])
  qc.setQueryData(['config', 'c1', 'masked'], {
    DATABASE_URL: { value_version: 1, created_at: '2026-07-01T00:00:00Z', origin: 'own' },
    STRIPE_KEY: { value_version: 1, created_at: '2026-07-01T00:00:00Z', origin: 'own' },
  })
  return qc
}

test('always includes projects and nav actions', () => {
  const qc = seed()
  const { result } = renderHook(() => usePaletteItems(), { wrapper: wrapper(qc, '/') })
  const items = result.current
  expect(items.some((i: PaletteItem) => i.group === 'Projects' && i.label === 'api-gateway')).toBe(true)
  expect(items.some((i: PaletteItem) => i.group === 'Actions' && /activity/i.test(i.label))).toBe(true)
})

test('includes active-project configs and secret KEY NAMES (never values)', () => {
  const qc = seed()
  const { result } = renderHook(() => usePaletteItems(), { wrapper: wrapper(qc, '/projects/p1/configs/c1') })
  const items = result.current
  expect(items.some((i) => i.group === 'Configs' && i.label === 'dev')).toBe(true)
  const secret = items.find((i) => i.group === 'Secrets' && i.label === 'DATABASE_URL')
  expect(secret).toBeTruthy()
  expect(secret!.to).toBe('/projects/p1/configs/c1')
  // No item anywhere carries a secret value (masked metadata has none to leak).
  const serialized = JSON.stringify(items)
  expect(serialized).not.toContain('value_version')
})

test('omits configs/secrets when no active project (top-level route)', () => {
  const qc = seed()
  const { result } = renderHook(() => usePaletteItems(), { wrapper: wrapper(qc, '/') })
  expect(result.current.some((i) => i.group === 'Configs')).toBe(false)
  expect(result.current.some((i) => i.group === 'Secrets')).toBe(false)
})
```

Run: `npx vitest run src/palette/usePaletteItems.test.tsx` → FAIL (module missing).

- [ ] **Step 2: implement `web/src/palette/usePaletteItems.ts`:**

```ts
import { useQueryClient } from '@tanstack/react-query'
import { useLocation, matchPath } from 'react-router-dom'
import type { Project, Environment, Config, MaskedSecret } from '../lib/endpoints'

export type PaletteGroup = 'Projects' | 'Configs' | 'Secrets' | 'Actions'

export interface PaletteItem {
  id: string
  group: PaletteGroup
  label: string
  sublabel?: string
  keywords: string
  to: string // route to navigate to on select
}

const NAV_ACTIONS: { label: string; to: string; keywords: string }[] = [
  { label: 'Go to Projects', to: '/', keywords: 'projects home' },
  { label: 'Go to Activity', to: '/audit', keywords: 'activity audit log events' },
  { label: 'Go to Members', to: '/members', keywords: 'members users roles team' },
  { label: 'Go to Tokens', to: '/tokens', keywords: 'tokens service api' },
  { label: 'Go to Settings', to: '/settings', keywords: 'settings config' },
]

// Builds palette items from ALREADY-CACHED query data only (no fetches). Secret
// entries are KEY NAMES from unaudited masked metadata — never values.
export function usePaletteItems(): PaletteItem[] {
  const qc = useQueryClient()
  const loc = useLocation()
  const pid = matchPath('/projects/:projectId/*', loc.pathname)?.params.projectId
    ?? matchPath('/projects/:projectId', loc.pathname)?.params.projectId

  const items: PaletteItem[] = []

  const projects = qc.getQueryData<Project[]>(['projects']) ?? []
  for (const p of projects) {
    items.push({
      id: `project:${p.id}`, group: 'Projects', label: p.name,
      sublabel: p.slug, keywords: `${p.name} ${p.slug}`, to: `/projects/${p.id}`,
    })
  }

  if (pid) {
    const envs = qc.getQueryData<Environment[]>(['envs', pid]) ?? []
    for (const e of envs) {
      const configs = qc.getQueryData<Config[]>(['configs', pid, e.id]) ?? []
      for (const c of configs) {
        const to = `/projects/${pid}/configs/${c.id}`
        items.push({
          id: `config:${c.id}`, group: 'Configs', label: c.name,
          sublabel: e.name, keywords: `${c.name} ${e.name}`, to,
        })
        const masked = qc.getQueryData<Record<string, MaskedSecret>>(['config', c.id, 'masked'])
        if (masked) {
          for (const key of Object.keys(masked)) {
            items.push({
              id: `secret:${c.id}:${key}`, group: 'Secrets', label: key,
              sublabel: `${e.name} / ${c.name}`, keywords: `${key} ${c.name} ${e.name}`, to,
            })
          }
        }
      }
    }
  }

  for (const a of NAV_ACTIONS) {
    items.push({ id: `action:${a.to}`, group: 'Actions', label: a.label, keywords: a.keywords, to: a.to })
  }

  return items
}
```

- [ ] **Step 3: run → PASS** (3/3). `npx vitest run src/palette/usePaletteItems.test.tsx`.
- [ ] **Step 4: commit.**
```bash
git add web/src/palette/usePaletteItems.ts web/src/palette/usePaletteItems.test.tsx
git commit -m "feat(web): command-palette item index from cache (key names only)"
```

---

### Task 3: CommandPalette component

A Radix Dialog overlay with a search input, fuzzy-filtered grouped results, arrow-key navigation, and select-to-navigate. Driven by injected `items` + `open`/`onClose` props (so it's testable in isolation).

**Files:** Create `web/src/palette/CommandPalette.tsx`; test `web/src/palette/CommandPalette.test.tsx`.

- [ ] **Step 1 (TDD): write `web/src/palette/CommandPalette.test.tsx`:**

```tsx
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { expect, test, vi } from 'vitest'
import type { PaletteItem } from './usePaletteItems'
import { CommandPalette } from './CommandPalette'

const ITEMS: PaletteItem[] = [
  { id: 'project:p1', group: 'Projects', label: 'api-gateway', keywords: 'api-gateway gw', to: '/projects/p1' },
  { id: 'config:c1', group: 'Configs', label: 'production', sublabel: 'Production', keywords: 'production prod', to: '/x' },
  { id: 'secret:c1:DATABASE_URL', group: 'Secrets', label: 'DATABASE_URL', keywords: 'DATABASE_URL', to: '/x' },
]

function setup(onSelect = vi.fn()) {
  render(<CommandPalette open items={ITEMS} onClose={vi.fn()} onSelect={onSelect} />)
  return onSelect
}

test('filters items by fuzzy query', async () => {
  setup()
  await userEvent.type(screen.getByRole('combobox', { name: /search/i }), 'prod')
  expect(screen.getByText('production')).toBeInTheDocument()
  expect(screen.queryByText('api-gateway')).not.toBeInTheDocument()
})

test('Enter selects the highlighted item', async () => {
  const onSelect = setup()
  const input = screen.getByRole('combobox', { name: /search/i })
  await userEvent.type(input, 'data')
  await userEvent.keyboard('{Enter}')
  expect(onSelect).toHaveBeenCalledWith(expect.objectContaining({ id: 'secret:c1:DATABASE_URL' }))
})

test('ArrowDown moves highlight before Enter', async () => {
  const onSelect = setup()
  const input = screen.getByRole('combobox', { name: /search/i })
  // empty query → all items; highlight starts at index 0 (api-gateway)
  await userEvent.type(input, '{ArrowDown}') // → index 1 (production)
  await userEvent.keyboard('{Enter}')
  expect(onSelect).toHaveBeenCalledWith(expect.objectContaining({ id: 'config:c1' }))
})

test('shows empty state when nothing matches', async () => {
  setup()
  await userEvent.type(screen.getByRole('combobox', { name: /search/i }), 'zzzzz')
  expect(screen.getByText(/no matches/i)).toBeInTheDocument()
})
```

Run: `npx vitest run src/palette/CommandPalette.test.tsx` → FAIL (module missing).

- [ ] **Step 2: implement `web/src/palette/CommandPalette.tsx`:**

```tsx
import { useMemo, useState, useEffect, useRef } from 'react'
import * as Dialog from '@radix-ui/react-dialog'
import { fuzzyScore } from './fuzzy'
import type { PaletteItem, PaletteGroup } from './usePaletteItems'

const GROUP_ORDER: PaletteGroup[] = ['Projects', 'Configs', 'Secrets', 'Actions']

export function CommandPalette({
  open, items, onClose, onSelect,
}: {
  open: boolean
  items: PaletteItem[]
  onClose: () => void
  onSelect: (item: PaletteItem) => void
}) {
  const [query, setQuery] = useState('')
  const [active, setActive] = useState(0)
  const listRef = useRef<HTMLDivElement>(null)

  // Filter + rank, then order by group. `filtered` is the flat nav order.
  const filtered = useMemo(() => {
    const scored = items
      .map((it) => ({ it, score: fuzzyScore(query, `${it.label} ${it.keywords}`) }))
      .filter((s): s is { it: PaletteItem; score: number } => s.score !== null)
    scored.sort((a, b) => b.score - a.score)
    const byGroup = GROUP_ORDER.flatMap((g) => scored.filter((s) => s.it.group === g).map((s) => s.it))
    return byGroup
  }, [items, query])

  // Reset highlight to top whenever the result set changes.
  useEffect(() => { setActive(0) }, [query, open])

  function commit(item: PaletteItem | undefined) {
    if (!item) return
    onSelect(item)
  }

  function onKeyDown(e: React.KeyboardEvent) {
    if (e.key === 'ArrowDown') { e.preventDefault(); setActive((a) => Math.min(a + 1, filtered.length - 1)) }
    else if (e.key === 'ArrowUp') { e.preventDefault(); setActive((a) => Math.max(a - 1, 0)) }
    else if (e.key === 'Enter') { e.preventDefault(); commit(filtered[active]) }
  }

  // Track which flat index each rendered row is, to highlight + group headers.
  let flatIndex = -1

  return (
    <Dialog.Root open={open} onOpenChange={(o) => { if (!o) onClose() }}>
      <Dialog.Portal>
        <Dialog.Overlay className="fixed inset-0 z-40 bg-ink/40" />
        <Dialog.Content
          aria-label="Command palette"
          onOpenAutoFocus={(e) => { e.preventDefault() }}
          className="fixed left-1/2 top-[15vh] z-50 w-[min(560px,92vw)] -translate-x-1/2 overflow-hidden rounded-card border border-line bg-elevated shadow-pop"
        >
          <Dialog.Title className="sr-only">Command palette</Dialog.Title>
          <input
            role="combobox"
            aria-label="Search projects, configs, secrets"
            aria-expanded="true"
            aria-controls="palette-list"
            autoFocus
            value={query}
            onChange={(e) => setQuery(e.target.value)}
            onKeyDown={onKeyDown}
            placeholder="Search projects, configs, secrets…"
            className="w-full border-b border-line bg-transparent px-4 py-3 text-[14px] text-ink outline-none placeholder:text-faint"
          />
          <div id="palette-list" ref={listRef} role="listbox" className="max-h-[50vh] overflow-y-auto p-1.5">
            {filtered.length === 0 && (
              <p className="px-3 py-6 text-center text-[12.5px] text-faint">No matches</p>
            )}
            {GROUP_ORDER.map((group) => {
              const rows = filtered.filter((it) => it.group === group)
              if (rows.length === 0) return null
              return (
                <div key={group} className="mb-1">
                  <div className="px-3 pb-0.5 pt-2 text-[10.5px] font-bold uppercase tracking-[.12em] text-faint">
                    {group}
                  </div>
                  {rows.map((it) => {
                    flatIndex += 1
                    const isActive = flatIndex === active
                    return (
                      <button
                        key={it.id}
                        type="button"
                        role="option"
                        aria-selected={isActive}
                        onMouseEnter={() => setActive(filtered.indexOf(it))}
                        onClick={() => commit(it)}
                        className={
                          'flex w-full items-center justify-between rounded px-3 py-2 text-left text-[13px] ' +
                          (isActive ? 'bg-brand-soft text-brand-text' : 'text-ink')
                        }
                      >
                        <span className={it.group === 'Secrets' || it.group === 'Configs' ? 'font-mono text-[12.5px]' : ''}>
                          {it.label}
                        </span>
                        {it.sublabel && <span className="ml-3 shrink-0 text-[11.5px] text-faint">{it.sublabel}</span>}
                      </button>
                    )
                  })}
                </div>
              )
            })}
          </div>
          <div className="flex gap-3 border-t border-line px-3 py-1.5 text-[10.5px] text-faint">
            <span>↑↓ navigate</span><span>↵ open</span><span>esc close</span>
          </div>
        </Dialog.Content>
      </Dialog.Portal>
    </Dialog.Root>
  )
}
```

- [ ] **Step 3: run → PASS** (4/4). If the `flatIndex`/`active` mapping desyncs from `filtered.indexOf`, note that rows render in the SAME group order as `filtered` (both use `GROUP_ORDER`), so the running `flatIndex` equals the item's index in `filtered` — the tests lock this. `npx vitest run src/palette/CommandPalette.test.tsx`.
- [ ] **Step 4: commit.**
```bash
git add web/src/palette/CommandPalette.tsx web/src/palette/CommandPalette.test.tsx
git commit -m "feat(web): command palette overlay with fuzzy search + keyboard nav"
```

---

### Task 4: PaletteProvider + global ⌘K

Context that owns open state, a global ⌘K/Ctrl+K listener, and wires `usePaletteItems` + navigation into `CommandPalette`.

**Files:** Create `web/src/palette/PaletteProvider.tsx`; test `web/src/palette/PaletteProvider.test.tsx`.

- [ ] **Step 1 (TDD): write `web/src/palette/PaletteProvider.test.tsx`:**

```tsx
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { MemoryRouter } from 'react-router-dom'
import { expect, test } from 'vitest'
import { PaletteProvider, usePalette } from './PaletteProvider'

function Opener() {
  const { open } = usePalette()
  return <button onClick={open}>open-palette</button>
}

function shell() {
  const qc = new QueryClient()
  qc.setQueryData(['projects'], [{ id: 'p1', slug: 'gw', name: 'api-gateway' }])
  return render(
    <QueryClientProvider client={qc}>
      <MemoryRouter>
        <PaletteProvider><Opener /></PaletteProvider>
      </MemoryRouter>
    </QueryClientProvider>,
  )
}

test('Cmd/Ctrl+K opens the palette', async () => {
  shell()
  expect(screen.queryByRole('combobox', { name: /search/i })).not.toBeInTheDocument()
  await userEvent.keyboard('{Control>}k{/Control}')
  expect(await screen.findByRole('combobox', { name: /search/i })).toBeInTheDocument()
})

test('usePalette().open() opens it and shows a project', async () => {
  shell()
  await userEvent.click(screen.getByText('open-palette'))
  expect(await screen.findByText('api-gateway')).toBeInTheDocument()
})
```

Run: `npx vitest run src/palette/PaletteProvider.test.tsx` → FAIL (module missing).

- [ ] **Step 2: implement `web/src/palette/PaletteProvider.tsx`:**

```tsx
import { createContext, useCallback, useContext, useEffect, useMemo, useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { usePaletteItems, type PaletteItem } from './usePaletteItems'
import { CommandPalette } from './CommandPalette'

interface PaletteCtx { open: () => void; close: () => void; isOpen: boolean }
const Ctx = createContext<PaletteCtx | null>(null)

export function PaletteProvider({ children }: { children: React.ReactNode }) {
  const [isOpen, setOpen] = useState(false)
  const navigate = useNavigate()
  const items = usePaletteItems()

  const open = useCallback(() => setOpen(true), [])
  const close = useCallback(() => setOpen(false), [])

  useEffect(() => {
    function onKey(e: KeyboardEvent) {
      if ((e.metaKey || e.ctrlKey) && (e.key === 'k' || e.key === 'K')) {
        e.preventDefault()
        setOpen((o) => !o)
      }
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [])

  const onSelect = useCallback((item: PaletteItem) => {
    setOpen(false)
    navigate(item.to)
  }, [navigate])

  const value = useMemo(() => ({ open, close, isOpen }), [open, close, isOpen])
  return (
    <Ctx.Provider value={value}>
      {children}
      <CommandPalette open={isOpen} items={items} onClose={close} onSelect={onSelect} />
    </Ctx.Provider>
  )
}

export function usePalette(): PaletteCtx {
  const v = useContext(Ctx)
  if (!v) throw new Error('usePalette must be used within PaletteProvider')
  return v
}
```

- [ ] **Step 3: run → PASS** (2/2). `npx vitest run src/palette/PaletteProvider.test.tsx`.
- [ ] **Step 4: commit.**
```bash
git add web/src/palette/PaletteProvider.tsx web/src/palette/PaletteProvider.test.tsx
git commit -m "feat(web): PaletteProvider with global Cmd/Ctrl+K"
```

---

### Task 5: TopBar trigger + standalone theme toggle; mount the provider; /audit route

Wire the palette into the authed shell, add the topbar ⌘K trigger + a sun/moon theme toggle, and give "Activity" an instance-level route.

**Files:** Create `web/src/shell/ThemeToggle.tsx`; modify `web/src/shell/TopBar.tsx`, `web/src/App.tsx`, `web/src/test/render.tsx`.

- [ ] **Step 1: create `web/src/shell/ThemeToggle.tsx`:**

```tsx
import { Moon, Sun } from 'lucide-react'
import { useTheme } from '../theme/ThemeProvider'

// Standalone quick toggle: flips between light and dark based on the RESOLVED
// theme (the user-menu radio still offers the explicit 3-way incl. System).
export function ThemeToggle() {
  const { resolved, setTheme } = useTheme()
  const next = resolved === 'dark' ? 'light' : 'dark'
  return (
    <button
      type="button"
      aria-label={`Switch to ${next} theme`}
      onClick={() => setTheme(next)}
      className="flex h-7 w-7 items-center justify-center rounded text-muted hover:bg-line-soft hover:text-ink"
    >
      {resolved === 'dark' ? <Sun size={16} strokeWidth={1.7} /> : <Moon size={16} strokeWidth={1.7} />}
    </button>
  )
}
```

- [ ] **Step 2: rewrite `web/src/shell/TopBar.tsx`** to add the palette trigger + ThemeToggle and use the `topbar` token:

```tsx
import { Search } from 'lucide-react'
import { Brand } from '../ui/Brand'
import { Pill } from '../ui/Pill'
import { Breadcrumb } from './Breadcrumb'
import { UserMenu } from './UserMenu'
import { ThemeToggle } from './ThemeToggle'
import { usePalette } from '../palette/PaletteProvider'

export function TopBar({ sealed }: { sealed: boolean }) {
  const { open } = usePalette()
  return (
    <header className="flex items-center gap-4 border-b border-line bg-topbar px-4 py-2">
      <Brand />
      <Breadcrumb />
      <button
        type="button"
        onClick={open}
        aria-label="Open command palette"
        className="ml-4 flex min-w-[260px] items-center gap-2 rounded border border-line bg-card px-3 py-1.5 text-[12.5px] text-faint hover:border-brand-line"
      >
        <Search size={14} strokeWidth={1.7} />
        <span>Search projects, configs, secrets…</span>
        <span className="ml-auto rounded border border-line px-1.5 py-0.5 text-[10.5px] font-semibold text-muted">⌘K</span>
      </button>
      <div className="ml-auto flex items-center gap-2.5">
        {sealed ? <Pill tone="danger" dot>Sealed</Pill> : <Pill tone="success" dot>Unsealed</Pill>}
        <ThemeToggle />
        <UserMenu />
      </div>
    </header>
  )
}
```

- [ ] **Step 3: mount `PaletteProvider` + add the `/audit` route** in `web/src/App.tsx`. Add the import `import { PaletteProvider } from './palette/PaletteProvider'`, wrap the authed shell's return, and add the route. The authed branch of `Gate()` becomes:

```tsx
  return (
    <PaletteProvider>
      <AppLayout sealed={seal.sealed} sidebar={<Sidebar />}>
        <Routes>
          <Route path="/" element={<Landing />} />
          <Route path="/projects/:projectId" element={<ProjectOverview />} />
          <Route path="/projects/:projectId/configs/:configId" element={<SecretEditor />} />
          <Route path="/projects/:projectId/audit" element={<AuditPage />} />
          <Route path="/audit" element={<AuditPage />} />
          <Route path="/tokens" element={<TokensPage />} />
          <Route path="/members" element={<MembersPage />} />
          <Route path="/transit" element={<Placeholder feature="Transit UI" />} />
          <Route path="/settings" element={<Placeholder feature="Settings" />} />
          <Route path="*" element={<Navigate to="/" replace />} />
        </Routes>
      </AppLayout>
    </PaletteProvider>
  )
```
(`PaletteProvider` must be INSIDE `BrowserRouter` — it is, since `Gate` renders within `<BrowserRouter>` in `App()`. It uses `useNavigate`/`useLocation`, so it cannot go above the router.)

- [ ] **Step 4: keep the test harness working.** `TopBar` now calls `usePalette()`, so any test rendering the shell needs a `PaletteProvider`. Update `web/src/test/render.tsx` to wrap in `PaletteProvider` INSIDE the `MemoryRouter` (it needs router context):

```tsx
import { PaletteProvider } from '../palette/PaletteProvider'
```
and change the returned tree so the wrap is `MemoryRouter > PaletteProvider > (wrap(...))`:

```tsx
      <ThemeProvider>
        <MemoryRouter initialEntries={[route]}>
          <PaletteProvider>
            {wrap(
              <Routes>
                <Route path={pattern} element={ui} />
              </Routes>,
            )}
          </PaletteProvider>
        </MemoryRouter>
      </ThemeProvider>
```
Also, `App.test.tsx` renders `<App/>` directly (App mounts its own PaletteProvider inside Gate), so it needs no change for the palette — but verify it still passes.

- [ ] **Step 5: verify + commit.**
Run (from `web/`): `npx vitest run && npm run typecheck`. Expected: all green (existing shell tests now render through PaletteProvider; the palette mounts closed so no visible change). If a test asserts exact TopBar contents, update it to accommodate the new search button (do NOT weaken unrelated assertions).
```bash
git add web/src/shell/ThemeToggle.tsx web/src/shell/TopBar.tsx web/src/App.tsx web/src/test/render.tsx
git commit -m "feat(web): topbar command-palette trigger + theme toggle; instance /audit route"
```

---

### Task 6: Sidebar — primary nav + re-theme (contextual tree preserved)

Add a Doppler primary nav block (Projects/Activity/Members/Tokens/Settings) at the top; keep the project/env/config tree as a contextual block below (so config navigation never regresses); re-theme with tokens. Drop the redundant bottom "Instance" duplicates (Audit→Activity at top; Tokens/Members move up). Transit stays reachable by URL and is added to nav by B5 (not shown in the Doppler mockup).

**Files:** Modify `web/src/shell/Sidebar.tsx`; check `web/src/shell/Sidebar.test.tsx`.

- [ ] **Step 1: add a failing test** to `web/src/shell/Sidebar.test.tsx`. The file already imports `http`, `HttpResponse`, `server`, `screen`, `renderApp`, `Sidebar`. Append:

```tsx
test('primary nav links to the five dev-focused destinations', async () => {
  server.use(http.get('/v1/projects', () => HttpResponse.json({ projects: [] })))
  renderApp(<Sidebar />, { route: '/', withAuth: false })
  expect(await screen.findByRole('link', { name: 'Projects' })).toHaveAttribute('href', '/')
  expect(screen.getByRole('link', { name: 'Activity' })).toHaveAttribute('href', '/audit')
  expect(screen.getByRole('link', { name: 'Members' })).toHaveAttribute('href', '/members')
  expect(screen.getByRole('link', { name: 'Tokens' })).toHaveAttribute('href', '/tokens')
  expect(screen.getByRole('link', { name: 'Settings' })).toHaveAttribute('href', '/settings')
})
```
Run: `npx vitest run src/shell/Sidebar.test.tsx` → the new test FAILS (no primary nav yet); the EXISTING "renders projects, then the selected project's env → config tree" test must still PASS after Step 2 (the env/config tree is preserved; the project name still appears exactly once, now as the section header instead of a `<select>` option).

- [ ] **Step 2: replace `web/src/shell/Sidebar.tsx` in full.** This restructure (primary nav at top, project tree becomes a contextual block, project `<select>` and the standalone add-project affordance removed — project creation lives on the Projects list) is extensive, so replace the whole file:

```tsx
import { useState } from 'react'
import { Link, useLocation, useNavigate, matchPath } from 'react-router-dom'
import { LayoutGrid, ScrollText, KeyRound, Users, Settings, Plus } from 'lucide-react'
import { useProjects, useEnvironments, useConfigs } from '../secrets/nav'
import { CreateEnvironmentForm, CreateConfigForm } from '../structure/CreateForms'
import { Config } from '../lib/endpoints'
import { envTone, envDotClass } from '../ui/env'
import { cn } from '../ui/cn'

// Sidebar is a sibling of <Routes>, so useParams() is empty here — derive the
// active ids from the URL via matchPath.
function useActiveIds() {
  const location = useLocation()
  return {
    projectId: matchPath('/projects/:projectId/*', location.pathname)?.params.projectId,
    configId: matchPath('/projects/:projectId/configs/:configId', location.pathname)?.params.configId,
  }
}

type OpenForm = null | 'env' | { config: { eid: string; bases: Config[] } }

function SectionLabel({ children, action }: { children: React.ReactNode; action?: React.ReactNode }) {
  return (
    <div className="mb-1 mt-4 flex items-center justify-between px-2 text-[10.5px] font-bold uppercase tracking-[.12em] text-faint">
      <span className="truncate">{children}</span>
      {action}
    </div>
  )
}

function IconAdd({ label, onClick }: { label: string; onClick: () => void }) {
  return (
    <button
      type="button"
      onClick={onClick}
      aria-label={label}
      className="flex h-5 w-5 items-center justify-center rounded text-faint hover:bg-brand-soft hover:text-brand-text"
    >
      <Plus size={13} strokeWidth={1.7} />
    </button>
  )
}

function EnvConfigs({ pid, eid, name, activeConfigId, onAddConfig }: {
  pid: string
  eid: string
  name: string
  activeConfigId?: string
  onAddConfig: (eid: string, bases: Config[]) => void
}) {
  const configs = useConfigs(pid, eid)
  return (
    <li className="mx-1 mt-2">
      <div className="flex items-center justify-between px-2 text-[12px] font-semibold text-muted">
        <span className="flex items-center gap-2">
          <span className={cn('h-[7px] w-[7px] rounded-[2px]', envDotClass[envTone(name)])} />
          {name}
        </span>
        <IconAdd label={`add config to ${name}`} onClick={() => onAddConfig(eid, configs.data ?? [])} />
      </div>
      <ul className="mt-0.5">
        {configs.data?.map((c) => {
          const active = c.id === activeConfigId
          return (
            <li key={c.id} className="relative ml-3.5">
              {active && <span className="absolute -left-3.5 bottom-[5px] top-[5px] w-[3px] rounded-full bg-brand" />}
              <Link
                to={`/projects/${pid}/configs/${c.id}`}
                aria-current={active ? 'page' : undefined}
                className={cn(
                  'block rounded px-2 py-1 text-[12.5px] text-muted hover:bg-line-soft',
                  active && 'bg-brand-soft font-semibold text-brand-text hover:bg-brand-soft',
                )}
              >
                {c.name}
                {c.inherits_from && <span className="ml-1 text-[11px] text-info">↳</span>}
              </Link>
            </li>
          )
        })}
      </ul>
    </li>
  )
}

const PRIMARY = [
  { to: '/', label: 'Projects', Icon: LayoutGrid, match: (p: string) => p === '/' || p.startsWith('/projects') },
  { to: '/audit', label: 'Activity', Icon: ScrollText, match: (p: string) => p === '/audit' },
  { to: '/members', label: 'Members', Icon: Users, match: (p: string) => p === '/members' },
  { to: '/tokens', label: 'Tokens', Icon: KeyRound, match: (p: string) => p === '/tokens' },
  { to: '/settings', label: 'Settings', Icon: Settings, match: (p: string) => p === '/settings' },
]

const primaryItem =
  'mx-1 flex items-center gap-2.5 rounded px-2 py-1.5 text-[12.5px] font-medium text-muted hover:bg-line-soft hover:text-ink'
const primaryActive = 'bg-brand-soft font-semibold text-brand-text hover:bg-brand-soft'

export function Sidebar() {
  const { projectId, configId } = useActiveIds()
  const location = useLocation()
  const navigate = useNavigate()
  const projects = useProjects()
  const envs = useEnvironments(projectId)
  const [open, setOpen] = useState<OpenForm>(null)
  const projectName = projects.data?.find((p) => p.id === projectId)?.name

  return (
    <nav className="text-sm">
      <div className="mb-2 flex flex-col gap-0.5">
        {PRIMARY.map(({ to, label, Icon, match }) => {
          const active = match(location.pathname)
          return (
            <Link
              key={to}
              to={to}
              aria-current={active ? 'page' : undefined}
              className={cn(primaryItem, active && primaryActive)}
            >
              <Icon size={15} strokeWidth={1.7} /> {label}
            </Link>
          )
        })}
      </div>

      {projectId && (
        <>
          <SectionLabel action={<IconAdd label="add environment" onClick={() => setOpen('env')} />}>
            {projectName ?? 'Project'}
          </SectionLabel>
          <ul>
            {envs.data?.map((e) => (
              <EnvConfigs
                key={e.id}
                pid={projectId}
                eid={e.id}
                name={e.name}
                activeConfigId={configId}
                onAddConfig={(eid, bases) => setOpen({ config: { eid, bases } })}
              />
            ))}
          </ul>
        </>
      )}

      {open === 'env' && projectId && (
        <CreateEnvironmentForm pid={projectId} onCreated={() => setOpen(null)} onClose={() => setOpen(null)} />
      )}
      {open && typeof open === 'object' && projectId && (
        <CreateConfigForm
          pid={projectId}
          eid={open.config.eid}
          bases={open.config.bases}
          onCreated={(c) => { setOpen(null); navigate('/projects/' + projectId + '/configs/' + c.id) }}
          onClose={() => setOpen(null)}
        />
      )}
    </nav>
  )
}
```
Notes: `text-brand-text` (active nav text) = the dark-legible accent `#A79CFF` in dark / `#5546E0` in light, so this new nav is AA-correct in dark from the start (unlike the old `text-brand-deep` usages R4 will fix). `CreateProjectForm` is intentionally no longer imported here (project creation is on the Projects list). `navigate` is still used by the config-create success handler; `location` drives primary active state.

- [ ] **Step 3: run the Sidebar tests → PASS.** `npx vitest run src/shell/Sidebar.test.tsx` — both the new primary-nav test and the existing env/config-tree test pass. If the existing test regresses, do NOT weaken it — reconcile the structure (the project name must still render exactly once and the config link must keep its `href` + `aria-current`).

- [ ] **Step 4: full suite + commit.**
Run (from `web/`): `npx vitest run && npm run typecheck`. Expected: green.
```bash
git add web/src/shell/Sidebar.tsx web/src/shell/Sidebar.test.tsx
git commit -m "feat(web): sidebar primary nav (Doppler) with contextual project tree"
```

---

### Task 7: Gates, tracker, smoke

**Files:** Modify `fe-improvements.md`.

- [ ] **Step 1: full gate battery.** Rebuild the dev container so the served bundle includes R2, then run all gates:
```bash
docker compose up -d --build && ./scripts/dev-unseal.sh
cd web && npm run typecheck && npx vitest run && npm run build && npm run smoke
cd .. && go build ./...
```
Expected: typecheck clean; all vitest green (incl. the 4 new palette test files); build OK; `npm run smoke` reports BOTH themes render (the shell now includes the palette trigger + restyled sidebar — confirm the root still renders and no pageerror). Go builds.

- [ ] **Step 2: manual ⌘K sanity (via smoke or a quick note).** The smoke script boots the authed shell; the palette mounts closed so it won't affect the existing assertion. (Optional: if you extend smoke to press Ctrl+K and assert the combobox appears, keep it resilient — do not fail the whole gate on a timing flake.) Not required to pass R2.

- [ ] **Step 3: check off `fe-improvements.md`.** In the "Dark redesign" section, mark R2 done:
```markdown
- [x] **R2** shell & ⌘K command palette
```

- [ ] **Step 4: commit.**
```bash
git add fe-improvements.md
git commit -m "docs(fe): check off R2 shell & command palette"
```

- [ ] **Step 5 (controller):** final whole-branch review → PR → merge per standing orders → rebuild container → update memory.

---

## Final gate (after all tasks)

`npm run typecheck && npx vitest run && npm run build && npm run smoke` (web/) + `go build ./...` (root). Then hand off to `superpowers:finishing-a-development-branch`.

**Exit criteria:** ⌘K (and the topbar search button) opens a palette that fuzzy-searches projects (always) + the active project's configs and secret KEY NAMES + nav actions, navigates on Enter/click, and closes on Esc; the sidebar shows the Doppler primary nav with the project/env/config tree preserved contextually; a standalone theme toggle sits in the topbar; both themes still pass smoke; NO secret value is ever indexed, displayed, requested, or logged by the palette.
