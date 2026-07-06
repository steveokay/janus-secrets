# UI Slice 2 — Landing, EmptyState, Project Overview Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Kill the two remaining blank pages — a branded landing at `/` and a per-project overview dashboard at `/projects/:projectId` — plus a reusable `<EmptyState>` applied to the dashboard and the secret editor.

**Architecture:** Pure front-end slice on the merged Slice 1 token system. Dashboard data is client-side aggregation over existing endpoints using cache-shared TanStack Query keys (`['configs', pid, eid]` with Sidebar, `['config', cid, 'masked']` with SecretEditor) — masked metadata only, never audited reveals. New `web/src/home/` folder holds the two pages; `EmptyState` joins the `web/src/ui/` primitives.

**Tech Stack:** React 18 + TS + Tailwind tokens + TanStack Query v5 + react-router v6 + vitest/msw (all existing; zero new dependencies).

**Authority documents:** spec `docs/superpowers/specs/2026-07-06-ui-slice2-landing-emptystates-design.md`; visual rules `docs/superpowers/specs/2026-07-06-ui-visual-design.md`. The palette gate (`web/src/test/no-raw-palette.test.ts`) will fail the build on any raw palette class or hex literal — token classes only.

**All commands run from `web/` unless stated otherwise.**

---

### Task 0: Branch

- [ ] **Step 1** (repo root): `git checkout -b milestone-14-ui-slice2` from up-to-date `main`; verify `git status` clean.

---

### Task 1: EmptyState primitive

**Files:**
- Create: `web/src/ui/EmptyState.tsx`
- Test: `web/src/ui/EmptyState.test.tsx`

- [ ] **Step 1: Write the failing test** — `web/src/ui/EmptyState.test.tsx`

```tsx
import { render, screen } from '@testing-library/react'
import { EmptyState } from './EmptyState'

test('renders title, hint, icon and action', () => {
  render(
    <EmptyState
      icon={<svg data-testid="ico" />}
      title="Nothing here"
      hint="Try adding one."
      action={<button>Add</button>}
    />,
  )
  expect(screen.getByText('Nothing here')).toBeInTheDocument()
  expect(screen.getByText('Try adding one.')).toBeInTheDocument()
  expect(screen.getByTestId('ico')).toBeInTheDocument()
  expect(screen.getByRole('button', { name: 'Add' })).toBeInTheDocument()
})

test('omits icon wrap and hint when not provided', () => {
  const { container } = render(<EmptyState title="Bare" />)
  expect(screen.getByText('Bare')).toBeInTheDocument()
  expect(container.querySelector('.bg-brand-soft')).toBeNull()
})
```

Run: `npx vitest run src/ui/EmptyState.test.tsx` — FAIL (module missing).

- [ ] **Step 2: Implement** — `web/src/ui/EmptyState.tsx`

```tsx
import { ReactNode } from 'react'

// Shared empty-list treatment (spec slice-2 §1). Dumb and presentational —
// callers own the action button and any navigation.
export function EmptyState({ icon, title, hint, action }: {
  icon?: ReactNode
  title: string
  hint?: string
  action?: ReactNode
}) {
  return (
    <div className="mx-auto mt-16 flex max-w-sm flex-col items-center gap-3 text-center">
      {icon && (
        <div className="flex h-12 w-12 items-center justify-center rounded-full bg-brand-soft text-brand-deep">
          {icon}
        </div>
      )}
      <p className="text-[15px] font-semibold text-ink">{title}</p>
      {hint && <p className="text-[12.5px] text-muted">{hint}</p>}
      {action}
    </div>
  )
}
```

- [ ] **Step 3: Verify** — test PASS; `npm run typecheck` clean.

- [ ] **Step 4: Commit**

```bash
git add src/ui/EmptyState.tsx src/ui/EmptyState.test.tsx
git commit -m "feat(web): EmptyState primitive"
```

---

### Task 2: timeAgo util

**Files:**
- Create: `web/src/lib/time.ts`
- Test: `web/src/lib/time.test.ts`

- [ ] **Step 1: Write the failing test** — `web/src/lib/time.test.ts`

```ts
import { timeAgo } from './time'

const now = new Date('2026-07-06T12:00:00Z')

test.each([
  ['2026-07-06T11:59:30Z', 'just now'],
  ['2026-07-06T11:59:00Z', '1m ago'],
  ['2026-07-06T11:01:00Z', '59m ago'],
  ['2026-07-06T11:00:00Z', '1h ago'],
  ['2026-07-05T13:00:00Z', '23h ago'],
  ['2026-07-05T12:00:00Z', '1d ago'],
  ['2026-06-06T12:00:00Z', '30d ago'],
])('timeAgo(%s) → %s', (iso, expected) => {
  expect(timeAgo(iso, now)).toBe(expected)
})

test('older than 30 days falls back to locale date', () => {
  expect(timeAgo('2026-06-05T12:00:00Z', now)).toBe(new Date('2026-06-05T12:00:00Z').toLocaleDateString())
})

test('future timestamps clamp to just now', () => {
  expect(timeAgo('2026-07-06T12:05:00Z', now)).toBe('just now')
})
```

Run: `npx vitest run src/lib/time.test.ts` — FAIL.

- [ ] **Step 2: Implement** — `web/src/lib/time.ts`

```ts
// Coarse relative time for dashboard rows. `now` is injectable for tests.
export function timeAgo(iso: string, now: Date = new Date()): string {
  const s = Math.max(0, Math.floor((now.getTime() - new Date(iso).getTime()) / 1000))
  if (s < 60) return 'just now'
  const m = Math.floor(s / 60)
  if (m < 60) return `${m}m ago`
  const h = Math.floor(m / 60)
  if (h < 24) return `${h}h ago`
  const d = Math.floor(h / 24)
  if (d <= 30) return `${d}d ago`
  return new Date(iso).toLocaleDateString()
}
```

- [ ] **Step 3: Verify** — test PASS; typecheck clean.

- [ ] **Step 4: Commit**

```bash
git add src/lib/time.ts src/lib/time.test.ts
git commit -m "feat(web): timeAgo util for dashboard rows"
```

---

### Task 3: Landing page + route

**Files:**
- Create: `web/src/home/Landing.tsx`
- Test: `web/src/home/Landing.test.tsx`
- Modify: `web/src/App.tsx` (route `/` only)

- [ ] **Step 1: Write the failing test** — `web/src/home/Landing.test.tsx`

```tsx
import { http, HttpResponse } from 'msw'
import { screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { server } from '../test/msw'
import { renderApp } from '../test/render'
import { Landing } from './Landing'

test('no projects: hero with CTA that opens the create dialog', async () => {
  server.use(http.get('/v1/projects', () => HttpResponse.json({ projects: [] })))
  renderApp(<Landing />, { withAuth: false })
  expect(await screen.findByText('Your secrets, sealed and audited')).toBeInTheDocument()
  await userEvent.click(screen.getByRole('button', { name: /create your first project/i }))
  expect(await screen.findByRole('heading', { name: /new project/i })).toBeInTheDocument()
})

test('with projects: link cards + new-project button', async () => {
  server.use(
    http.get('/v1/projects', () =>
      HttpResponse.json({ projects: [
        { id: 'p1', slug: 'acme', name: 'acme-api' },
        { id: 'p2', slug: 'web', name: 'storefront' },
      ] }),
    ),
  )
  renderApp(<Landing />, { withAuth: false })
  expect(await screen.findByRole('link', { name: /acme-api/ })).toHaveAttribute('href', '/projects/p1')
  expect(screen.getByRole('link', { name: /storefront/ })).toHaveAttribute('href', '/projects/p2')
  expect(screen.getByRole('button', { name: /new project/i })).toBeInTheDocument()
})
```

Run: `npx vitest run src/home/Landing.test.tsx` — FAIL (module missing).

- [ ] **Step 2: Implement** — `web/src/home/Landing.tsx`

```tsx
import { useState } from 'react'
import { Link, useNavigate } from 'react-router-dom'
import { Plus } from 'lucide-react'
import { Brand } from '../ui/Brand'
import { useProjects } from '../secrets/nav'
import { CreateProjectForm } from '../structure/CreateForms'

export function Landing() {
  const projects = useProjects()
  const navigate = useNavigate()
  const [creating, setCreating] = useState(false)

  const createDialog = creating && (
    <CreateProjectForm
      onCreated={(p) => { setCreating(false); navigate('/projects/' + p.id) }}
      onClose={() => setCreating(false)}
    />
  )

  if (projects.isLoading) {
    return (
      <div className="mx-auto mt-20 flex max-w-md flex-col gap-2">
        {[0, 1, 2].map((i) => <div key={i} className="h-12 rounded-card bg-line-soft" />)}
      </div>
    )
  }
  if (projects.isError) {
    return <p role="alert" className="mt-20 text-center text-danger">Could not load projects.</p>
  }

  if (!projects.data?.length) {
    return (
      <div className="mx-auto mt-20 flex max-w-md flex-col items-center gap-4 text-center">
        <Brand markOnly size={40} />
        <h1 className="text-[22px] font-semibold tracking-tight">Your secrets, sealed and audited</h1>
        <p className="text-[13.5px] text-muted">
          Projects, environments and configs — encrypted end-to-end, every reveal audited.
        </p>
        <button
          onClick={() => setCreating(true)}
          className="rounded bg-brand px-4 py-2 text-[13px] font-semibold text-white shadow-card"
        >
          Create your first project
        </button>
        {createDialog}
      </div>
    )
  }

  return (
    <div className="mx-auto mt-20 flex w-full max-w-md flex-col gap-2">
      <h1 className="mb-1 text-[17px] font-semibold tracking-tight">Open a project</h1>
      {projects.data.map((p) => (
        <Link
          key={p.id}
          to={`/projects/${p.id}`}
          className="flex items-center justify-between rounded-card border border-line bg-card px-4 py-3 shadow-card hover:border-brand-line"
        >
          <span className="text-[13.5px] font-semibold">{p.name}</span>
          <span className="text-[11.5px] text-faint">{p.slug}</span>
        </Link>
      ))}
      <button
        onClick={() => setCreating(true)}
        className="mt-1 flex items-center justify-center gap-1.5 rounded border border-line bg-card px-4 py-2 text-[13px] font-semibold"
      >
        <Plus size={14} strokeWidth={1.7} /> New project
      </button>
      {createDialog}
    </div>
  )
}
```

- [ ] **Step 3: Wire the route** — in `web/src/App.tsx`, add `import { Landing } from './home/Landing'` and change ONLY the `/` route:

```tsx
        <Route path="/" element={<Landing />} />
```

(The old landing div disappears. All other routes untouched.)

- [ ] **Step 4: Verify** — `npx vitest run src/home/Landing.test.tsx src/App.test.tsx` PASS (App.test's third test boots with `projects: []` — the hero renders but its assertions target the top bar, unaffected); full `npx vitest run` green; typecheck clean.

- [ ] **Step 5: Commit**

```bash
git add src/home/Landing.tsx src/home/Landing.test.tsx src/App.tsx
git commit -m "feat(web): branded landing — hero CTA or open-a-project list"
```

---

### Task 4: ProjectOverview dashboard + route

**Files:**
- Create: `web/src/home/ProjectOverview.tsx`
- Test: `web/src/home/ProjectOverview.test.tsx`
- Modify: `web/src/App.tsx` (route `/projects/:projectId` only)

- [ ] **Step 1: Write the failing test** — `web/src/home/ProjectOverview.test.tsx`

```tsx
import { http, HttpResponse } from 'msw'
import { screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { server } from '../test/msw'
import { renderApp } from '../test/render'
import { ProjectOverview } from './ProjectOverview'

test('renders env cards with config rows, counts and empty env', async () => {
  server.use(
    http.get('/v1/projects', () => HttpResponse.json({ projects: [{ id: 'p1', slug: 'acme', name: 'acme-api' }] })),
    http.get('/v1/projects/p1/environments', () =>
      HttpResponse.json({ environments: [
        { id: 'e1', slug: 'prod', name: 'production' },
        { id: 'e2', slug: 'dev', name: 'development' },
      ] }),
    ),
    http.get('/v1/projects/p1/environments/e1/configs', () =>
      HttpResponse.json({ configs: [{ id: 'c1', environment_id: 'e1', name: 'root', inherits_from: null, created_at: '' }] }),
    ),
    http.get('/v1/projects/p1/environments/e2/configs', () => HttpResponse.json({ configs: [] })),
    http.get('/v1/configs/c1/secrets', () =>
      HttpResponse.json({ secrets: {
        DATABASE_URL: { value_version: 3, created_at: '2026-07-06T10:00:00Z', origin: 'own' },
        API_KEY: { value_version: 1, created_at: '2026-07-06T08:00:00Z', origin: 'own' },
      } }),
    ),
  )
  renderApp(<ProjectOverview />, { route: '/projects/p1', withAuth: false })
  expect(await screen.findByRole('heading', { name: 'acme-api' })).toBeInTheDocument()
  expect(await screen.findByText('production')).toBeInTheDocument()
  expect(await screen.findByRole('link', { name: /root/ })).toHaveAttribute('href', '/projects/p1/configs/c1')
  expect(await screen.findByText(/2 keys/)).toBeInTheDocument()
  expect(await screen.findByText('No configs yet')).toBeInTheDocument()
  expect(screen.getByText(/Reads 24h/)).toBeInTheDocument()
})

test('zero environments shows EmptyState with create action', async () => {
  server.use(
    http.get('/v1/projects', () => HttpResponse.json({ projects: [{ id: 'p1', slug: 'acme', name: 'acme-api' }] })),
    http.get('/v1/projects/p1/environments', () => HttpResponse.json({ environments: [] })),
  )
  renderApp(<ProjectOverview />, { route: '/projects/p1', withAuth: false })
  expect(await screen.findByText('No environments yet')).toBeInTheDocument()
  await userEvent.click(screen.getByRole('button', { name: /create environment/i }))
  expect(await screen.findByRole('heading', { name: /new environment/i })).toBeInTheDocument()
})
```

Run: `npx vitest run src/home/ProjectOverview.test.tsx` — FAIL.

- [ ] **Step 2: Implement** — `web/src/home/ProjectOverview.tsx`

```tsx
import { useState } from 'react'
import { Link, useParams } from 'react-router-dom'
import { useQueries } from '@tanstack/react-query'
import { Layers } from 'lucide-react'
import { endpoints, Config, MaskedSecret } from '../lib/endpoints'
import { useProjects, useEnvironments } from '../secrets/nav'
import { envTone, envDotClass } from '../ui/env'
import { EmptyState } from '../ui/EmptyState'
import { Pill } from '../ui/Pill'
import { cn } from '../ui/cn'
import { useTitle } from '../lib/title'
import { timeAgo } from '../lib/time'
import { CreateEnvironmentForm } from '../structure/CreateForms'

function ConfigRow({ pid, config, meta }: { pid: string; config: Config; meta?: Record<string, MaskedSecret> }) {
  const keys = meta ? Object.keys(meta).length : undefined
  const last = meta
    ? Object.values(meta).reduce<string | null>((acc, m) => (!acc || m.created_at > acc ? m.created_at : acc), null)
    : null
  return (
    <Link
      to={`/projects/${pid}/configs/${config.id}`}
      className="flex items-center justify-between border-t border-line-soft px-4 py-2.5 hover:bg-line-soft/50"
    >
      <span className="text-[13px] font-medium text-ink">
        {config.name}
        {config.inherits_from && <span className="ml-1 text-[11px] text-info">↳</span>}
      </span>
      <span className="text-[11.5px] tabular-nums text-faint">
        {keys === undefined ? '— keys' : keys === 0 ? '0 keys' : `${keys} keys · ${timeAgo(last!)}`}
      </span>
    </Link>
  )
}

function EnvCard({ pid, name, configs, error }: { pid: string; name: string; configs?: Config[]; error: boolean }) {
  // Same queryKey as SecretEditor's masked query — cache-shared, unaudited metadata.
  const metas = useQueries({
    queries: (configs ?? []).map((c) => ({
      queryKey: ['config', c.id, 'masked'],
      queryFn: () => endpoints.maskedSecrets(c.id),
    })),
  })
  return (
    <section className="rounded-card border border-line bg-card shadow-card">
      <header className="flex items-center gap-2 px-4 py-2.5">
        <span className={cn('h-[7px] w-[7px] rounded-[2px]', envDotClass[envTone(name)])} />
        <span className="text-[12px] font-semibold uppercase tracking-[.08em] text-muted">{name}</span>
        <span className="ml-auto text-[11px] tabular-nums text-faint">
          {configs ? `${configs.length} configs` : ''}
        </span>
      </header>
      {!configs && !error && <div className="mx-4 mb-3 h-4 rounded bg-line-soft" />}
      {error && <p className="border-t border-line-soft px-4 py-2.5 text-[12.5px] text-danger">Couldn't load configs.</p>}
      {configs?.length === 0 && (
        <p className="border-t border-line-soft px-4 py-2.5 text-[12.5px] text-faint">No configs yet</p>
      )}
      {configs?.map((c, i) => <ConfigRow key={c.id} pid={pid} config={c} meta={metas[i]?.data} />)}
    </section>
  )
}

export function ProjectOverview() {
  const { projectId } = useParams()
  const pid = projectId!
  const projects = useProjects()
  const envs = useEnvironments(pid)
  const [creatingEnv, setCreatingEnv] = useState(false)
  const project = projects.data?.find((p) => p.id === pid)
  useTitle(project?.name)

  // Same queryKey as Sidebar/Breadcrumb — cache-shared.
  const configLists = useQueries({
    queries: (envs.data ?? []).map((e) => ({
      queryKey: ['configs', pid, e.id],
      queryFn: () => endpoints.listConfigs(pid, e.id),
    })),
  })
  const totalConfigs =
    configLists.length > 0 && configLists.every((q) => q.data)
      ? configLists.reduce((n, q) => n + (q.data?.length ?? 0), 0)
      : undefined

  if (envs.isError) {
    return <p role="alert" className="mt-16 text-center text-danger">Could not load environments.</p>
  }

  return (
    <div>
      <div className="mb-4 flex items-center justify-between">
        <div>
          <h3 className="text-[17px] font-semibold tracking-tight">{project?.name ?? '…'}</h3>
          <p className="text-[12.5px] text-faint">
            {envs.data ? `${envs.data.length} environments` : '…'}
            {totalConfigs !== undefined && ` · ${totalConfigs} configs`}
          </p>
        </div>
        {/* Placeholder until Phase-2D usage metrics; becomes a real stat then. */}
        <Pill tone="muted">Reads 24h · soon</Pill>
      </div>
      {envs.data?.length === 0 ? (
        <EmptyState
          icon={<Layers size={22} strokeWidth={1.7} />}
          title="No environments yet"
          hint="Environments hold your configs — dev, staging, prod."
          action={
            <button
              onClick={() => setCreatingEnv(true)}
              className="rounded bg-brand px-4 py-2 text-[13px] font-semibold text-white shadow-card"
            >
              Create environment
            </button>
          }
        />
      ) : (
        <div className="grid gap-4 md:grid-cols-2">
          {envs.data?.map((e, i) => (
            <EnvCard key={e.id} pid={pid} name={e.name} configs={configLists[i]?.data} error={!!configLists[i]?.error} />
          ))}
        </div>
      )}
      {creatingEnv && (
        <CreateEnvironmentForm pid={pid} onCreated={() => setCreatingEnv(false)} onClose={() => setCreatingEnv(false)} />
      )}
    </div>
  )
}
```

- [ ] **Step 3: Wire the route** — in `web/src/App.tsx`, add `import { ProjectOverview } from './home/ProjectOverview'` and change ONLY:

```tsx
        <Route path="/projects/:projectId" element={<ProjectOverview />} />
```

- [ ] **Step 4: Verify** — `npx vitest run src/home` PASS; full suite green; typecheck clean.

- [ ] **Step 5: Commit**

```bash
git add src/home/ProjectOverview.tsx src/home/ProjectOverview.test.tsx src/App.tsx
git commit -m "feat(web): project overview dashboard — env cards, key counts, reads placeholder"
```

---

### Task 5: SecretEditor empty state

**Files:**
- Modify: `web/src/secrets/SecretEditor.tsx`
- Modify: `web/src/secrets/SecretEditor.test.tsx` (APPEND one test; existing tests untouched)

- [ ] **Step 1: Append the failing test** to `web/src/secrets/SecretEditor.test.tsx` — follow the file's EXISTING msw-handler and render pattern (this repo's msw v2 matches on path only, so the file already uses single handlers that branch on `new URL(request.url).searchParams`; reuse its helpers rather than inventing new ones). The test to add, adapted to those helpers:

```tsx
test('empty config shows the empty state', async () => {
  // register this config's handler with zero secrets, both masked and raw branches
  // (use the file's existing handler-builder with `secrets: {}` and version 0)
  renderApp(<SecretEditor />, { route: '/projects/p1/configs/cEmpty' })
  expect(await screen.findByText('No secrets yet')).toBeInTheDocument()
  // AddKeyRow must still be present so the user can add the first key:
  expect(screen.getByLabelText('new key')).toBeInTheDocument()
})
```

Run: `npx vitest run src/secrets/SecretEditor.test.tsx` — the new test FAILS ("No secrets yet" not found).

- [ ] **Step 2: Implement** — in `web/src/secrets/SecretEditor.tsx`:

Add import: `import { EmptyState } from '../ui/EmptyState'`

Wrap the existing `<table>…</table>` block in a conditional (the table code itself is UNCHANGED — only indentation moves):

```tsx
      {rows.length === 0 && addedKeys.length === 0 ? (
        <EmptyState
          title="No secrets yet"
          hint="Add your first key below — it's encrypted before it ever touches the database."
        />
      ) : (
        <table className="w-full overflow-hidden rounded-card border border-line bg-card text-sm shadow-card">
          …existing table content, unchanged…
        </table>
      )}
```

`AddKeyRow` stays after this block in all cases.

- [ ] **Step 3: Verify** — `npx vitest run src/secrets` all PASS (existing tests untouched); full suite green; typecheck clean.

- [ ] **Step 4: Commit**

```bash
git add src/secrets/SecretEditor.tsx src/secrets/SecretEditor.test.tsx
git commit -m "feat(web): empty state for configs with no secrets"
```

---

### Task 6: Full gates + tracker check-off

**Files:**
- Modify: `fe-improvements.md`

- [ ] **Step 1: Web gates** (in `web/`): `npm run typecheck && npx vitest run && npm run build` — all green (the palette gate runs as part of the suite and must pass).

- [ ] **Step 2: Go embed** (repo root): `go build ./...` — succeeds.

- [ ] **Step 3: Tracker** — in `fe-improvements.md` §2:
  - Check `[x]` **P0 Real landing state** with note *(Slice 2)*.
  - Check `[x]` **P1 Project overview dashboard** with note *(Slice 2 — key counts + last-change from masked metadata; "Reads 24h" is a placeholder pill until Phase-2D; recent activity stays in B3)*.
  - Check `[x]` **P1 Richer empty states everywhere** with note *(Slice 2 — `<EmptyState>` shipped, applied to overview + secret editor; remaining screens adopt it as they land)*.
  - Under "Suggested rollout", mark Slice 2 **→ SHIPPED** with a link to this plan.

- [ ] **Step 4: Commit**

```bash
git add fe-improvements.md
git commit -m "docs(fe): check off slice-2 landing, dashboard, empty states"
```

- [ ] **Step 5: Manual visual check** (human): `make dev`, open http://127.0.0.1:8210 — landing with/without projects, project overview cards, empty-env state, empty-config editor.

---

## Out of scope (do not build)

Recent-activity feed (B3) · real Reads-24h metrics (Phase-2D) · onboarding checklist (P2) · `GET /v1/projects/:id/summary` endpoint · toasts/shimmer-skeleton component (Slice 3).
