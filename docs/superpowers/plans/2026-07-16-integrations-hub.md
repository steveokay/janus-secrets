# Integrations Hub Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a frontend-only `/integrations` catalog page: connector cards (GitHub, Kubernetes, OIDC) that show best-effort, 403-tolerant status and deep-link into the existing Operations/Settings config surfaces.

**Architecture:** Three new files under `web/src/integrations/` — a presentational `ConnectorCard` (+ `StatusLine`), a thin `useIntegrationStatus` hook that composes the **existing** `useSync('all')` aggregator and two TanStack queries (`getFederationConfig`, `oidcLoginStatus`), and an `IntegrationsPage` that renders the fixed three-card grid. Then three one-line wiring edits (route, sidebar, palette). No backend, no moved code.

**Tech Stack:** React + TypeScript + Vite + Tailwind (Nocturne tokens) + TanStack Query + react-router-dom; tests with Vitest + Testing Library + msw.

**Spec:** `docs/superpowers/specs/2026-07-16-integrations-hub-design.md` (closes `gaps.md` §1.15).

---

## File Structure

- Create `web/src/integrations/ConnectorCard.tsx` — presentational card + `StatusLine` tri-state renderer. No data fetching.
- Create `web/src/integrations/ConnectorCard.test.tsx` — StatusLine tri-state + action-link tests.
- Create `web/src/integrations/useIntegrationStatus.ts` — composes `useSync` + two queries into a neutralised status object.
- Create `web/src/integrations/IntegrationsPage.tsx` — the `/integrations` route; renders the three cards from the hook.
- Create `web/src/integrations/IntegrationsPage.test.tsx` — populated + forbidden (403-tolerance) integration tests.
- Modify `web/src/App.tsx` — add the `/integrations` route.
- Modify `web/src/shell/Sidebar.tsx` — add the nav item (above Operations).
- Modify `web/src/palette/usePaletteItems.ts` — add the "Go to Integrations" nav action.

All commands run from `web/`. Run a single test file with:
`npm test -- src/integrations/<file>` (Vitest matches by path substring).

---

### Task 1: ConnectorCard + StatusLine (presentational)

**Files:**
- Create: `web/src/integrations/ConnectorCard.tsx`
- Test: `web/src/integrations/ConnectorCard.test.tsx`

- [ ] **Step 1: Write the failing test**

Create `web/src/integrations/ConnectorCard.test.tsx`:

```tsx
import { screen } from '@testing-library/react'
import { GitBranch } from 'lucide-react'
import { renderApp } from '../test/render'
import { ConnectorCard, StatusLine } from './ConnectorCard'

test('StatusLine renders a numeric count as text', () => {
  renderApp(<StatusLine label="Actions sync" value={2} />, { withAuth: false })
  expect(screen.getByText('Actions sync')).toBeInTheDocument()
  expect(screen.getByText('2')).toBeInTheDocument()
})

test('StatusLine renders null as an em-dash (neutral)', () => {
  renderApp(<StatusLine label="Actions sync" value={null} />, { withAuth: false })
  expect(screen.getByText('—')).toBeInTheDocument()
})

test('StatusLine renders booleans as enabled/disabled', () => {
  const { unmount } = renderApp(<StatusLine label="Login" value={true} />, { withAuth: false })
  expect(screen.getByText('enabled')).toBeInTheDocument()
  unmount()
  renderApp(<StatusLine label="Login" value={false} />, { withAuth: false })
  expect(screen.getByText('disabled')).toBeInTheDocument()
})

test('StatusLine shows neither dash nor value while loading (undefined)', () => {
  renderApp(<StatusLine label="Actions sync" value={undefined} />, { withAuth: false })
  expect(screen.queryByText('—')).toBeNull()
  expect(screen.queryByText('enabled')).toBeNull()
})

test('ConnectorCard renders title, description and action links with hrefs', () => {
  renderApp(
    <ConnectorCard
      icon={<GitBranch size={18} />}
      title="GitHub"
      description="Sync secrets to GitHub Actions."
      statuses={<StatusLine label="Actions sync" value={2} />}
      actions={[{ label: 'Sync →', to: '/operations?tab=sync' }]}
    />,
    { withAuth: false },
  )
  expect(screen.getByRole('heading', { name: 'GitHub' })).toBeInTheDocument()
  expect(screen.getByText('Sync secrets to GitHub Actions.')).toBeInTheDocument()
  expect(screen.getByRole('link', { name: 'Sync →' })).toHaveAttribute('href', '/operations?tab=sync')
})
```

- [ ] **Step 2: Run test to verify it fails**

Run: `npm test -- src/integrations/ConnectorCard`
Expected: FAIL — cannot resolve `./ConnectorCard` (module does not exist yet).

- [ ] **Step 3: Write minimal implementation**

Create `web/src/integrations/ConnectorCard.tsx`:

```tsx
import type { ReactNode } from 'react'
import { Link } from 'react-router-dom'
import { Card } from '../ui/Card'
import { Pill } from '../ui/Pill'
import { Skeleton } from '../ui/Skeleton'
import { buttonClasses } from '../ui/Button'

export interface CardAction {
  label: string
  to: string
}

/**
 * Tri-state status renderer:
 *   undefined → loading skeleton
 *   null      → neutral em-dash (no permission / not configured)
 *   number    → count pill
 *   boolean   → enabled/disabled pill
 */
export function StatusLine({ label, value }: { label: string; value: number | boolean | null | undefined }) {
  return (
    <div className="flex items-center justify-between text-[12px]">
      <span className="text-ink-mute">{label}</span>
      {value === undefined ? (
        <Skeleton className="h-4 w-10" />
      ) : value === null ? (
        <span className="text-ink-mute">—</span>
      ) : typeof value === 'number' ? (
        <Pill tone="muted">{value}</Pill>
      ) : (
        <Pill tone={value ? 'success' : 'muted'} dot>
          {value ? 'enabled' : 'disabled'}
        </Pill>
      )}
    </div>
  )
}

export function ConnectorCard({
  icon,
  title,
  description,
  statuses,
  actions,
}: {
  icon: ReactNode
  title: string
  description: string
  statuses: ReactNode
  actions: CardAction[]
}) {
  return (
    <Card className="flex flex-col gap-3 p-4">
      <div className="flex items-center gap-2.5">
        <span className="text-ink-mute">{icon}</span>
        <h3 className="text-[14px] font-semibold text-ink">{title}</h3>
      </div>
      <p className="text-[12.5px] text-ink-mute">{description}</p>
      <div className="flex flex-col gap-1.5">{statuses}</div>
      <div className="mt-auto flex flex-wrap gap-2 pt-1">
        {actions.map((a) => (
          <Link key={a.to + a.label} to={a.to} className={buttonClasses('secondary', 'sm')}>
            {a.label}
          </Link>
        ))}
      </div>
    </Card>
  )
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `npm test -- src/integrations/ConnectorCard`
Expected: PASS (5 tests).

- [ ] **Step 5: Commit**

```bash
git add web/src/integrations/ConnectorCard.tsx web/src/integrations/ConnectorCard.test.tsx
git commit -m "feat(web): ConnectorCard + tri-state StatusLine for integrations hub"
```

---

### Task 2: useIntegrationStatus hook + IntegrationsPage

**Files:**
- Create: `web/src/integrations/useIntegrationStatus.ts`
- Create: `web/src/integrations/IntegrationsPage.tsx`
- Test: `web/src/integrations/IntegrationsPage.test.tsx`

The page test exercises the hook's populated and neutral (403) branches end-to-end; the loading (`undefined`) branch is already covered by Task 1's StatusLine test.

- [ ] **Step 1: Write the failing test**

Create `web/src/integrations/IntegrationsPage.test.tsx`:

```tsx
import { http, HttpResponse } from 'msw'
import { screen } from '@testing-library/react'
import { server } from '../test/msw'
import { renderApp } from '../test/render'
import { IntegrationsPage } from './IntegrationsPage'

// useSync('all') fans out: list projects → per-project envs (for the config
// name map) → per-project sync targets. Empty envs keeps the map empty; the
// target list still drives the provider counts.
function topo() {
  server.use(http.get('/v1/projects', () => HttpResponse.json({ projects: [{ id: 'p1', slug: 'acme', name: 'Acme' }] })))
  server.use(http.get('/v1/projects/:pid/environments', () => HttpResponse.json({ environments: [] })))
}

const T = (provider: 'github' | 'k8s', id: string) => ({
  id, project_id: 'p1', config_id: 'c1', provider, prune: true, interval_seconds: 300,
  addr: {}, status: 'active', failure_count: 0, next_sync_at: 'x', managed_keys: [], created_at: 'x',
})

test('populated: shows per-provider sync counts, federation and OIDC status, and deep-links', async () => {
  topo()
  server.use(http.get('/v1/sync/targets', () => HttpResponse.json({ targets: [T('github', 's1'), T('github', 's2'), T('k8s', 's3')] })))
  server.use(http.get('/v1/sys/oidc/federation', () => HttpResponse.json({ issuer: 'https://x', audience: 'urn:janus', enabled: true })))
  server.use(http.get('/v1/auth/oidc/status', () => HttpResponse.json({ enabled: true, name: 'GitHub' })))

  renderApp(<IntegrationsPage />, { route: '/integrations', withAuth: false })

  expect(await screen.findByText('2')).toBeInTheDocument() // GitHub Actions sync count
  expect(screen.getByText('1')).toBeInTheDocument() // Kubernetes sync count
  expect(screen.getAllByText('enabled').length).toBeGreaterThanOrEqual(2) // federation + OIDC login
  expect(screen.getByRole('link', { name: /federation/i })).toHaveAttribute('href', '/settings?section=federation')
  expect(screen.getByRole('link', { name: /configure/i })).toHaveAttribute('href', '/settings?section=oidc')
  expect(screen.getAllByRole('link', { name: /sync|manage/i })[0]).toHaveAttribute('href', '/operations?tab=sync')
})

test('403-tolerant: all three cards still render with neutral status and working links', async () => {
  topo()
  const forbid = () => HttpResponse.json({ error: { code: 'forbidden', message: 'no' } }, { status: 403 })
  server.use(http.get('/v1/sync/targets', forbid))
  server.use(http.get('/v1/sys/oidc/federation', forbid))
  server.use(http.get('/v1/auth/oidc/status', () => HttpResponse.json({ enabled: false })))

  renderApp(<IntegrationsPage />, { route: '/integrations', withAuth: false })

  // GitHub sync (—), Kubernetes sync (—), CI federation (—) = 3 neutral lines.
  expect(await screen.findByRole('heading', { name: 'Kubernetes' })).toBeInTheDocument()
  expect(screen.getAllByText('—')).toHaveLength(3)
  expect(screen.getByText('disabled')).toBeInTheDocument() // OIDC status endpoint is public
  expect(screen.getByRole('heading', { name: 'GitHub' })).toBeInTheDocument()
  expect(screen.getByRole('heading', { name: /OIDC/i })).toBeInTheDocument()
  expect(screen.getByRole('link', { name: /configure/i })).toHaveAttribute('href', '/settings?section=oidc')
})
```

- [ ] **Step 2: Run test to verify it fails**

Run: `npm test -- src/integrations/IntegrationsPage`
Expected: FAIL — cannot resolve `./IntegrationsPage`.

- [ ] **Step 3: Write the hook**

Create `web/src/integrations/useIntegrationStatus.ts`:

```ts
import { useQuery } from '@tanstack/react-query'
import { useSync } from '../operations/useAggregated'
import { endpoints } from '../lib/endpoints'

/**
 * Per-field status for the integrations catalog. Tri-state values:
 *   undefined → still loading
 *   null      → neutral (403 no-permission, 404 not-configured, or error)
 *   value     → real count / enabled flag
 */
export interface IntegrationStatus {
  githubSync: number | null | undefined
  k8sSync: number | null | undefined
  federation: boolean | null | undefined
  oidcLogin: boolean | null | undefined
}

export function useIntegrationStatus(): IntegrationStatus {
  const sync = useSync('all')
  const fed = useQuery({ queryKey: ['integrations', 'federation'], queryFn: endpoints.getFederationConfig, retry: false })
  const oidc = useQuery({ queryKey: ['integrations', 'oidc-login'], queryFn: endpoints.oidcLoginStatus, retry: false })

  // Neutralise sync when a non-403 error occurred, or the user is forbidden on
  // every project (someForbidden with zero visible rows) — showing "0" would be
  // misleading. A partial view (some rows visible) shows the real count.
  const syncNeutral = sync.isError || (sync.someForbidden && sync.rows.length === 0)
  const count = (p: 'github' | 'k8s') => sync.rows.filter((r) => r.data.provider === p).length

  return {
    githubSync: sync.isLoading ? undefined : syncNeutral ? null : count('github'),
    k8sSync: sync.isLoading ? undefined : syncNeutral ? null : count('k8s'),
    federation: fed.isLoading ? undefined : fed.data ? fed.data.enabled : null,
    oidcLogin: oidc.isLoading ? undefined : oidc.data ? oidc.data.enabled : null,
  }
}
```

- [ ] **Step 4: Write the page**

Create `web/src/integrations/IntegrationsPage.tsx`:

```tsx
import { GitBranch, Boxes, KeyRound } from 'lucide-react'
import { ConnectorCard, StatusLine } from './ConnectorCard'
import { useIntegrationStatus } from './useIntegrationStatus'

export function IntegrationsPage() {
  const s = useIntegrationStatus()
  return (
    <div className="mx-auto max-w-5xl p-6">
      <header className="mb-5">
        <h1 className="text-[20px] font-semibold text-ink">Integrations</h1>
        <p className="mt-1 text-[13px] text-ink-mute">
          Connect Janus to external systems. Configure each below.
        </p>
      </header>
      <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
        <ConnectorCard
          icon={<GitBranch size={18} />}
          title="GitHub"
          description="Sync secrets to GitHub Actions, and let CI pull secrets keyless via OIDC federation."
          statuses={
            <>
              <StatusLine label="Actions sync" value={s.githubSync} />
              <StatusLine label="CI federation" value={s.federation} />
            </>
          }
          actions={[
            { label: 'Sync →', to: '/operations?tab=sync' },
            { label: 'Federation →', to: '/settings?section=federation' },
          ]}
        />
        <ConnectorCard
          icon={<Boxes size={18} />}
          title="Kubernetes"
          description="Mirror a config's secrets into a namespaced Kubernetes Secret."
          statuses={<StatusLine label="Sync targets" value={s.k8sSync} />}
          actions={[{ label: 'Manage →', to: '/operations?tab=sync' }]}
        />
        <ConnectorCard
          icon={<KeyRound size={18} />}
          title="OIDC (SSO login)"
          description="Let users sign in through your OIDC identity provider."
          statuses={<StatusLine label="Login" value={s.oidcLogin} />}
          actions={[{ label: 'Configure →', to: '/settings?section=oidc' }]}
        />
      </div>
    </div>
  )
}
```

- [ ] **Step 5: Run test to verify it passes**

Run: `npm test -- src/integrations/IntegrationsPage`
Expected: PASS (2 tests).

- [ ] **Step 6: Commit**

```bash
git add web/src/integrations/useIntegrationStatus.ts web/src/integrations/IntegrationsPage.tsx web/src/integrations/IntegrationsPage.test.tsx
git commit -m "feat(web): integrations status hook + catalog page"
```

---

### Task 3: Wire route, sidebar, and command palette

**Files:**
- Modify: `web/src/App.tsx`
- Modify: `web/src/shell/Sidebar.tsx`
- Modify: `web/src/palette/usePaletteItems.ts`
- Test: `web/src/shell/Sidebar.test.tsx` (extend if it exists; otherwise create)

- [ ] **Step 1: Write the failing test**

Create (or add to) `web/src/shell/Sidebar.integrations.test.tsx`:

```tsx
import { screen } from '@testing-library/react'
import { renderApp } from '../test/render'
import { Sidebar } from './Sidebar'

test('sidebar has an Integrations link pointing at /integrations', () => {
  renderApp(<Sidebar />, { route: '/', withAuth: false })
  expect(screen.getByRole('link', { name: /integrations/i })).toHaveAttribute('href', '/integrations')
})
```

Create `web/src/palette/usePaletteItems.integrations.test.tsx`:

```tsx
import { renderHook } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { QueryClientProvider, QueryClient } from '@tanstack/react-query'
import { usePaletteItems } from './usePaletteItems'

test('palette includes a Go to Integrations navigation command', () => {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  const { result } = renderHook(() => usePaletteItems(''), {
    wrapper: ({ children }) => (
      <QueryClientProvider client={qc}>
        <MemoryRouter>{children}</MemoryRouter>
      </QueryClientProvider>
    ),
  })
  expect(result.current.some((i) => i.label === 'Go to Integrations' && i.to === '/integrations')).toBe(true)
})
```

Note: if `usePaletteItems` takes a different argument signature, match the call used in the existing `usePaletteItems.test.tsx` (read it first) rather than the `('')` shown here.

- [ ] **Step 2: Run tests to verify they fail**

Run: `npm test -- src/shell/Sidebar.integrations src/palette/usePaletteItems.integrations`
Expected: FAIL — no Integrations link / command found.

- [ ] **Step 3: Add the route in `web/src/App.tsx`**

Add the import near the other page imports:

```tsx
import { IntegrationsPage } from './integrations/IntegrationsPage'
```

Add the route immediately before the `/operations` route (around `web/src/App.tsx:69`):

```tsx
            <Route path="/integrations" element={<IntegrationsPage />} />
            <Route path="/operations" element={<OperationsPage />} />
```

- [ ] **Step 4: Add the sidebar item in `web/src/shell/Sidebar.tsx`**

Add `Blocks` to the existing lucide import (line 3):

```tsx
import { Home, LayoutGrid, ScrollText, KeyRound, Users, Shield, Settings, Plus, RefreshCw, Trash2, Blocks } from 'lucide-react'
```

Insert the item into the `PRIMARY` array, immediately before the Operations entry:

```tsx
  { to: '/integrations', label: 'Integrations', Icon: Blocks, match: (p: string) => p === '/integrations' },
  { to: '/operations', label: 'Operations', Icon: RefreshCw, match: (p: string) => p === '/operations' },
```

- [ ] **Step 5: Add the palette command in `web/src/palette/usePaletteItems.ts`**

Insert into the `NAV_ACTIONS` array, immediately before the Operations entry (around line 37):

```tsx
  { label: 'Go to Integrations', to: '/integrations', keywords: 'integrations connect github kubernetes oidc sync federation sso external' },
  { label: 'Go to Operations', to: '/operations', keywords: 'operations ops rotation sync dynamic leases credentials' },
```

- [ ] **Step 6: Run tests to verify they pass**

Run: `npm test -- src/shell/Sidebar.integrations src/palette/usePaletteItems.integrations`
Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add web/src/App.tsx web/src/shell/Sidebar.tsx web/src/palette/usePaletteItems.ts web/src/shell/Sidebar.integrations.test.tsx web/src/palette/usePaletteItems.integrations.test.tsx
git commit -m "feat(web): wire /integrations route, sidebar item, and palette command"
```

---

### Task 4: Full-suite + dual-theme verification

**Files:** none (verification only).

- [ ] **Step 1: Run the full web test suite**

Run (from `web/`): `npm test`
Expected: PASS — all pre-existing tests plus the new integrations tests green, zero failures.

- [ ] **Step 2: Type-check and lint**

Run (from `web/`): `npm run build`
Expected: PASS — `tsc` compiles with no errors and Vite builds (the `no-raw-palette` test also enforces token-only styling).

- [ ] **Step 3: Dual-theme smoke check**

Run (from `web/`): `npm run smoke`
Expected: PASS — the smoke script renders routes in both light and dark themes without error; `/integrations` renders in both.

- [ ] **Step 4: Update the tracker**

Mark `gaps.md` §1.15 done. Edit the row to prefix it with the done marker used by sibling rows (e.g. `~~**No Integrations hub.**~~ **[DONE 2026-07-16]** — /integrations catalog with best-effort 403-tolerant status + deep-links; frontend-only.`), matching the strikethrough style of items 1.9–1.11.

- [ ] **Step 5: Commit**

```bash
git add gaps.md
git commit -m "docs(gaps): mark §1.15 integrations hub done"
```

---

## Self-Review

**Spec coverage:**
- Catalog + deep-links, frontend-only → Tasks 1–3. ✓
- Three cards by external system (GitHub two-action, Kubernetes, OIDC) → Task 2 `IntegrationsPage`. ✓
- Best-effort, 403-tolerant status from `useSync`, `getFederationConfig`, `oidcLoginStatus` → Task 2 `useIntegrationStatus`; forbidden case tested. ✓
- Card states (loading/neutral/populated) → Task 1 `StatusLine` tri-state; loading tested in Task 1, neutral+populated in Task 2. ✓
- Verified deep-link contract (`/operations?tab=sync`, `/settings?section=oidc|federation`) → asserted in Task 2 tests. ✓
- Wiring: route, sidebar above Operations (icon `Blocks`), palette command → Task 3. ✓
- Nocturne tokens only / dual-theme → components use kit primitives + tokens; enforced by `no-raw-palette` (Task 4 build) and `npm run smoke` (Task 4). ✓
- Non-goals (no create/edit, no moved config, no new endpoint) → nothing in the plan adds these. ✓

**Placeholder scan:** No TBD/TODO; every code step shows complete code. The only conditional note (Task 3 Step 1) instructs reading the existing `usePaletteItems.test.tsx` to match the hook's argument signature — a deliberate guard, not a placeholder.

**Type consistency:** `IntegrationStatus` fields (`githubSync`, `k8sSync`, `federation`, `oidcLogin`) are produced by the hook (Task 2 Step 3) and consumed by the page (Task 2 Step 4). `StatusLine`'s `value: number | boolean | null | undefined` (Task 1) matches those field types. `ConnectorCard`'s `actions: CardAction[]` (`{label,to}`) matches all call sites. `useSync` returns `Aggregated<SyncView>` with `rows[].data.provider: 'github' | 'k8s'`, `someForbidden`, `isError`, `isLoading` — all used as defined in `web/src/operations/useAggregated.ts`.
