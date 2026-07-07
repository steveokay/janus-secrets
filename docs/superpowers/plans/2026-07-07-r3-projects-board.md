# R3 — Projects List + Env-Columns Board + Create-Project Modal Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the plain Landing + ProjectOverview screens with the Doppler-style projects list (searchable/sortable card grid) and the signature env-columns project board (config cards, add-config, inheritance nesting), plus a polished create-project modal.

**Architecture:** Two new page components under `web/src/home/` — `ProjectsList.tsx` (route `/`) and `ProjectBoard.tsx` (route `/projects/:projectId`) — replace `Landing.tsx` + `ProjectOverview.tsx`. Both compose from existing tokens + kit (`Pill`, `EmptyState`, `envTone`, `useProjects`/`useEnvironments`/`useConfigs`, `CreateProjectForm`/`CreateEnvironmentForm`/`CreateConfigForm`). Config counts are aggregated client-side from cached queries (single-tenant scale). `CreateProjectForm` gets a light restyle to the mockup (helper text, full-width primary).

**Tech Stack:** React 18 + TS + Vite + Tailwind (R1 CSS-var tokens) + TanStack Query v5 + React Router v6 + lucide-react + Vitest/MSW.

**Spec:** `docs/superpowers/specs/2026-07-07-dark-redesign-design.md` (§Screen treatments 2/4/5). **Mockup:** `docs/design/ui-redesign-mockup.html` (sections 2, 4, 5).

**Data note (important):** `Project` is `{ id, slug, name }` — NO description field; `endpoints.createProject(slug, name)`. Where the mockup shows a project description, use the **slug**. The create modal keeps slug+name (no description). This is intentional — backend changes are out of R3 scope.

**Branch:** create `milestone-20-r3-projects-board` off `main` before Task 1.

---

## File structure

- Modify `web/src/structure/CreateForms.tsx` — light restyle of `CreateProjectForm` (helper text, full-width primary, name-first). Keep the test-facing API: `aria-label="slug"`, `aria-label="name"`, a submit button named `Create`, `role="alert"` error. Env/config forms untouched (R4 polishes them).
- Create `web/src/home/ProjectsList.tsx` (+ `.test.tsx`) — route `/`. Search + sort + grid/list toggle + project cards (name, slug, config-count pill, env dots) + create-project trigger + empty state.
- Create `web/src/home/ProjectBoard.tsx` (+ `.test.tsx`) — route `/projects/:projectId`. Breadcrumb + `janus run` hint; horizontal env columns (accent bar + count pill + add-config); config cards with inheritance nesting; add-environment.
- Modify `web/src/App.tsx` — route `/` → `ProjectsList`, `/projects/:projectId` → `ProjectBoard`; drop the `Landing`/`ProjectOverview` imports.
- Delete `web/src/home/Landing.tsx`, `web/src/home/Landing.test.tsx`, `web/src/home/ProjectOverview.tsx`, `web/src/home/ProjectOverview.test.tsx` (replaced).
- Modify `fe-improvements.md` — check off R3.

---

### Task 1: Restyle the create-project modal

Light polish of `CreateProjectForm` to the mockup: a helper paragraph and a full-width primary button, name field first. Keep the shared `Dialog` wrapper and the existing field API so `CreateForms.test.tsx` still passes.

**Files:** Modify `web/src/structure/CreateForms.tsx`; test `web/src/structure/CreateForms.test.tsx` (verify still green; add one assertion).

- [ ] **Step 1: read `web/src/structure/CreateForms.test.tsx`** to see what it asserts about `CreateProjectForm` (field aria-labels, the Create button, error handling). Note them so the restyle preserves them.

- [ ] **Step 2: replace ONLY the `CreateProjectForm` function** in `web/src/structure/CreateForms.tsx` with this (leave `Dialog`, `useSubmit`, `CreateEnvironmentForm`, `CreateConfigForm` unchanged):

```tsx
export function CreateProjectForm({ onCreated, onClose }: { onCreated: (p: Project) => void; onClose: () => void }) {
  const qc = useQueryClient()
  const [slug, setSlug] = useState('')
  const [name, setName] = useState('')
  const { error, submit, busy } = useSubmit(
    () => endpoints.createProject(slug, name),
    (p) => { void qc.invalidateQueries({ queryKey: ['projects'] }); onCreated(p) },
  )
  return (
    <Dialog title="Create project">
      <p className="mb-3 text-[12.5px] leading-relaxed text-muted">
        Group your Development, Staging, and Production secrets. Each project holds
        multiple configs with versioned history and per-environment access.
      </p>
      <form onSubmit={submit} className="flex flex-col gap-2.5">
        <label className="flex flex-col gap-1 text-[12px] font-semibold text-ink">
          Name
          <input
            aria-label="name"
            value={name}
            onChange={(e) => setName(e.target.value)}
            placeholder="e.g. api-gateway"
            required
            className="rounded border border-line bg-card px-3 py-2 text-[13px] font-normal text-ink placeholder:text-faint"
          />
        </label>
        <label className="flex flex-col gap-1 text-[12px] font-semibold text-ink">
          Slug
          <input
            aria-label="slug"
            value={slug}
            onChange={(e) => setSlug(e.target.value)}
            placeholder="api-gateway"
            required
            className="rounded border border-line bg-card px-3 py-2 font-mono text-[12.5px] font-normal text-ink placeholder:text-faint"
          />
        </label>
        {error && <p role="alert" className="text-[12.5px] text-danger">{error}</p>}
        <div className="mt-1 flex justify-end gap-2">
          <button type="button" onClick={onClose} className="rounded border border-line bg-card px-3 py-1.5 text-[13px] font-semibold text-ink">
            Cancel
          </button>
          <button type="submit" disabled={busy} className="rounded bg-brand px-4 py-1.5 text-[13px] font-semibold text-white shadow-card disabled:opacity-50">
            Create project
          </button>
        </div>
      </form>
    </Dialog>
  )
}
```
Notes: the submit button text is now "Create project" (was "Create"). If `CreateForms.test.tsx` matches the button by `/create/i` it still passes; if it matches exactly `Create`, update that assertion to `/create project/i` (do NOT weaken other assertions). Slug uses `font-mono` (it's an identifier). Only token classes.

- [ ] **Step 3: add one test** to `web/src/structure/CreateForms.test.tsx` (reuse its existing render/msw setup — read Step 1):

```tsx
test('create-project modal shows the helper copy and submits name + slug', async () => {
  // model on the file's existing CreateProjectForm test: render it, mock POST /v1/projects
  // fill name + slug, submit, assert onCreated called. Add:
  expect(screen.getByText(/each project holds/i)).toBeInTheDocument()
})
```
If the file already has a CreateProjectForm submit test, just add the helper-copy assertion to it rather than duplicating the whole flow.

- [ ] **Step 4: run + commit.**
Run (from `web/`): `npx vitest run src/structure/CreateForms.test.tsx && npx vitest run && npm run typecheck`. Expected: green (reconcile the Create-button matcher if needed).
```bash
git add web/src/structure/CreateForms.tsx web/src/structure/CreateForms.test.tsx
git commit -m "feat(web): polish create-project modal (helper copy, primary CTA)"
```

---

### Task 2: Projects list page

New `/` page: a searchable, sortable grid (with a list view toggle) of project cards. Each card shows name, slug, a config-count pill, and env-color dots. Empty state + create trigger.

**Files:** Create `web/src/home/ProjectsList.tsx`; test `web/src/home/ProjectsList.test.tsx`.

- [ ] **Step 1 (TDD): write `web/src/home/ProjectsList.test.tsx`:**

```tsx
import { http, HttpResponse } from 'msw'
import { screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { server } from '../test/msw'
import { renderApp } from '../test/render'
import { ProjectsList } from './ProjectsList'

function mockProjects(projects: { id: string; slug: string; name: string }[]) {
  server.use(
    http.get('/v1/projects', () => HttpResponse.json({ projects })),
    // each project's envs (empty is fine for list-render tests)
    http.get('/v1/projects/:pid/environments', () => HttpResponse.json({ environments: [] })),
  )
}

test('renders a card per project with its slug', async () => {
  mockProjects([
    { id: 'p1', slug: 'api-gateway', name: 'api-gateway' },
    { id: 'p2', slug: 'web', name: 'web-frontend' },
  ])
  renderApp(<ProjectsList />, { route: '/', withAuth: false })
  expect(await screen.findByRole('link', { name: /api-gateway/i })).toHaveAttribute('href', '/projects/p1')
  expect(screen.getByRole('link', { name: /web-frontend/i })).toHaveAttribute('href', '/projects/p2')
})

test('search filters the list', async () => {
  mockProjects([
    { id: 'p1', slug: 'api-gateway', name: 'api-gateway' },
    { id: 'p2', slug: 'web', name: 'web-frontend' },
  ])
  renderApp(<ProjectsList />, { route: '/', withAuth: false })
  await screen.findByRole('link', { name: /api-gateway/i })
  await userEvent.type(screen.getByRole('searchbox', { name: /search projects/i }), 'web')
  expect(screen.queryByRole('link', { name: /api-gateway/i })).not.toBeInTheDocument()
  expect(screen.getByRole('link', { name: /web-frontend/i })).toBeInTheDocument()
})

test('sort Z–A reverses order', async () => {
  mockProjects([
    { id: 'p1', slug: 'alpha', name: 'alpha' },
    { id: 'p2', slug: 'zeta', name: 'zeta' },
  ])
  renderApp(<ProjectsList />, { route: '/', withAuth: false })
  await screen.findByRole('link', { name: /alpha/i })
  await userEvent.selectOptions(screen.getByRole('combobox', { name: /sort/i }), 'name-desc')
  const links = screen.getAllByRole('link')
  expect(within(links[0]).getByText('zeta')).toBeInTheDocument()
})

test('empty state offers to create the first project', async () => {
  mockProjects([])
  renderApp(<ProjectsList />, { route: '/', withAuth: false })
  expect(await screen.findByText(/no projects yet/i)).toBeInTheDocument()
  expect(screen.getByRole('button', { name: /create.*project/i })).toBeInTheDocument()
})
```
Run: `npx vitest run src/home/ProjectsList.test.tsx` → FAIL (module missing).

- [ ] **Step 2: implement `web/src/home/ProjectsList.tsx`:**

```tsx
import { useMemo, useState } from 'react'
import { Link } from 'react-router-dom'
import { useQueries } from '@tanstack/react-query'
import { LayoutGrid, List, Plus, FolderGit2 } from 'lucide-react'
import { endpoints, Project } from '../lib/endpoints'
import { useProjects, useEnvironments } from '../secrets/nav'
import { envTone, envDotClass } from '../ui/env'
import { EmptyState } from '../ui/EmptyState'
import { Pill } from '../ui/Pill'
import { cn } from '../ui/cn'
import { useTitle } from '../lib/title'
import { CreateProjectForm } from '../structure/CreateForms'

type Sort = 'name-asc' | 'name-desc'

// Self-fetches the project's envs + per-env config counts (cache-shared with the
// board/sidebar). Single-tenant scale, so per-card aggregation is fine.
function ProjectCard({ project, view }: { project: Project; view: 'grid' | 'list' }) {
  const envs = useEnvironments(project.id)
  const configQueries = useQueries({
    queries: (envs.data ?? []).map((e) => ({
      queryKey: ['configs', project.id, e.id],
      queryFn: () => endpoints.listConfigs(project.id, e.id),
    })),
  })
  const totalConfigs =
    configQueries.length > 0 && configQueries.every((q) => q.data)
      ? configQueries.reduce((n, q) => n + (q.data?.length ?? 0), 0)
      : undefined

  return (
    <Link
      to={`/projects/${project.id}`}
      className={cn(
        'group rounded-card border border-line bg-card p-4 shadow-card hover:border-brand-line',
        view === 'list' && 'flex items-center gap-4',
      )}
    >
      <div className="min-w-0 flex-1">
        <div className="truncate text-[14px] font-semibold text-ink">{project.name}</div>
        <div className="truncate font-mono text-[11.5px] text-faint">{project.slug}</div>
      </div>
      <div className={cn('flex items-center gap-2', view === 'grid' && 'mt-3')}>
        {envs.data && envs.data.length > 0 && (
          <span className="flex items-center gap-1">
            {envs.data.map((e) => (
              <span key={e.id} className={cn('h-[7px] w-[7px] rounded-[2px]', envDotClass[envTone(e.name)])} />
            ))}
          </span>
        )}
        <Pill tone="muted">{totalConfigs === undefined ? '… configs' : `${totalConfigs} configs`}</Pill>
      </div>
    </Link>
  )
}

export function ProjectsList() {
  useTitle('Projects')
  const projects = useProjects()
  const [q, setQ] = useState('')
  const [sort, setSort] = useState<Sort>('name-asc')
  const [view, setView] = useState<'grid' | 'list'>('grid')
  const [creating, setCreating] = useState(false)

  const shown = useMemo(() => {
    const list = (projects.data ?? []).filter(
      (p) => p.name.toLowerCase().includes(q.toLowerCase()) || p.slug.toLowerCase().includes(q.toLowerCase()),
    )
    list.sort((a, b) => (sort === 'name-asc' ? a.name.localeCompare(b.name) : b.name.localeCompare(a.name)))
    return list
  }, [projects.data, q, sort])

  const createDialog = creating && (
    <CreateProjectForm onCreated={() => setCreating(false)} onClose={() => setCreating(false)} />
  )

  if (projects.isLoading) {
    return (
      <div aria-hidden className="grid gap-3 sm:grid-cols-2 lg:grid-cols-3">
        {[0, 1, 2].map((i) => <div key={i} className="h-24 rounded-card bg-line-soft" />)}
      </div>
    )
  }
  if (projects.isError) {
    return <p role="alert" className="mt-16 text-center text-danger">Could not load projects.</p>
  }
  if (!projects.data?.length) {
    return (
      <>
        <EmptyState
          icon={<FolderGit2 size={22} strokeWidth={1.7} />}
          title="No projects yet"
          hint="A project groups your dev, staging and prod secrets."
          action={
            <button
              onClick={() => setCreating(true)}
              className="rounded bg-brand px-4 py-2 text-[13px] font-semibold text-white shadow-card"
            >
              Create your first project
            </button>
          }
        />
        {createDialog}
      </>
    )
  }

  return (
    <div>
      <div className="mb-4 flex items-center justify-between gap-3">
        <h2 className="text-[17px] font-semibold tracking-tight text-ink">Projects</h2>
        <button
          onClick={() => setCreating(true)}
          className="flex items-center gap-1.5 rounded bg-brand px-3 py-1.5 text-[13px] font-semibold text-white shadow-card"
        >
          <Plus size={14} strokeWidth={1.7} /> New project
        </button>
      </div>

      <div className="mb-4 flex flex-wrap items-center gap-2">
        <input
          type="search"
          role="searchbox"
          aria-label="search projects"
          value={q}
          onChange={(e) => setQ(e.target.value)}
          placeholder="Search projects…"
          className="min-w-[200px] flex-1 rounded border border-line bg-card px-3 py-1.5 text-[12.5px] text-ink placeholder:text-faint"
        />
        <select
          aria-label="sort"
          value={sort}
          onChange={(e) => setSort(e.target.value as Sort)}
          className="rounded border border-line bg-card px-2.5 py-1.5 text-[12.5px] text-ink"
        >
          <option value="name-asc">Name A–Z</option>
          <option value="name-desc">Name Z–A</option>
        </select>
        <div className="flex rounded border border-line">
          <button
            aria-label="grid view"
            aria-pressed={view === 'grid'}
            onClick={() => setView('grid')}
            className={cn('flex h-8 w-8 items-center justify-center rounded-l text-muted', view === 'grid' && 'bg-brand-soft text-brand-text')}
          >
            <LayoutGrid size={15} strokeWidth={1.7} />
          </button>
          <button
            aria-label="list view"
            aria-pressed={view === 'list'}
            onClick={() => setView('list')}
            className={cn('flex h-8 w-8 items-center justify-center rounded-r text-muted', view === 'list' && 'bg-brand-soft text-brand-text')}
          >
            <List size={15} strokeWidth={1.7} />
          </button>
        </div>
      </div>

      {shown.length === 0 ? (
        <EmptyState title="No projects match your search." />
      ) : (
        <div className={cn(view === 'grid' ? 'grid gap-3 sm:grid-cols-2 lg:grid-cols-3' : 'flex flex-col gap-2')}>
          {shown.map((p) => <ProjectCard key={p.id} project={p} view={view} />)}
        </div>
      )}
      {createDialog}
    </div>
  )
}
```

- [ ] **Step 3: run → PASS** (4/4). `npx vitest run src/home/ProjectsList.test.tsx`. Then `npx vitest run` (full) + `npm run typecheck`.
- [ ] **Step 4: commit.**
```bash
git add web/src/home/ProjectsList.tsx web/src/home/ProjectsList.test.tsx
git commit -m "feat(web): projects list — searchable card grid with config counts"
```

---

### Task 3: Project board (env columns)

New `/projects/:projectId` page: the signature board. Breadcrumb + `janus run` hint; a horizontal row of environment columns (env-colored accent bar + config-count pill + dashed add-config); config cards with inheritance nesting; add-environment.

**Files:** Create `web/src/home/ProjectBoard.tsx`; test `web/src/home/ProjectBoard.test.tsx`.

- [ ] **Step 1 (TDD): write `web/src/home/ProjectBoard.test.tsx`:**

```tsx
import { http, HttpResponse } from 'msw'
import { screen, within } from '@testing-library/react'
import { server } from '../test/msw'
import { renderApp } from '../test/render'
import { ProjectBoard } from './ProjectBoard'

function mock() {
  server.use(
    http.get('/v1/projects', () => HttpResponse.json({ projects: [{ id: 'p1', slug: 'gw', name: 'api-gateway' }] })),
    http.get('/v1/projects/p1/environments', () =>
      HttpResponse.json({ environments: [
        { id: 'e1', slug: 'dev', name: 'Development' },
        { id: 'e2', slug: 'prod', name: 'Production' },
      ] })),
    http.get('/v1/projects/p1/environments/e1/configs', () =>
      HttpResponse.json({ configs: [
        { id: 'c1', environment_id: 'e1', name: 'dev', inherits_from: null, created_at: '' },
        { id: 'c2', environment_id: 'e1', name: 'dev_personal', inherits_from: 'c1', created_at: '' },
      ] })),
    http.get('/v1/projects/p1/environments/e2/configs', () =>
      HttpResponse.json({ configs: [{ id: 'c3', environment_id: 'e2', name: 'prod', inherits_from: null, created_at: '' }] })),
  )
}

test('renders a column per environment with its configs', async () => {
  mock()
  renderApp(<ProjectBoard />, { route: '/projects/p1', withAuth: false })
  expect(await screen.findByRole('heading', { name: 'Development' })).toBeInTheDocument()
  expect(screen.getByRole('heading', { name: 'Production' })).toBeInTheDocument()
  // config card links to the editor
  expect(await screen.findByRole('link', { name: /^dev$/i })).toHaveAttribute('href', '/projects/p1/configs/c1')
  expect(screen.getByRole('link', { name: /prod/i })).toHaveAttribute('href', '/projects/p1/configs/c3')
})

test('inherited config renders nested under its base', async () => {
  mock()
  renderApp(<ProjectBoard />, { route: '/projects/p1', withAuth: false })
  const branch = await screen.findByRole('link', { name: /dev_personal/i })
  // the branch card is marked as inheriting (aria-label carries the relationship)
  expect(branch).toHaveAttribute('data-inherited', 'true')
})

test('shows the CLI hint and breadcrumb', async () => {
  mock()
  renderApp(<ProjectBoard />, { route: '/projects/p1', withAuth: false })
  expect(await screen.findByText('api-gateway')).toBeInTheDocument()
  expect(screen.getByText(/janus run/i)).toBeInTheDocument()
})
```
Run: `npx vitest run src/home/ProjectBoard.test.tsx` → FAIL (module missing).

- [ ] **Step 2: implement `web/src/home/ProjectBoard.tsx`:**

```tsx
import { useState } from 'react'
import { Link, useParams } from 'react-router-dom'
import { useQueries } from '@tanstack/react-query'
import { Lock, Plus, Layers } from 'lucide-react'
import { endpoints, Config, Environment } from '../lib/endpoints'
import { useProjects, useEnvironments } from '../secrets/nav'
import { envTone, envDotClass } from '../ui/env'
import { EmptyState } from '../ui/EmptyState'
import { Pill } from '../ui/Pill'
import { cn } from '../ui/cn'
import { useTitle } from '../lib/title'
import { CreateEnvironmentForm, CreateConfigForm } from '../structure/CreateForms'

function ConfigCard({ pid, config, depth }: { pid: string; config: Config; depth: number }) {
  return (
    <Link
      to={`/projects/${pid}/configs/${config.id}`}
      data-inherited={config.inherits_from ? 'true' : undefined}
      className={cn(
        'flex items-center gap-2 rounded border border-line bg-card px-3 py-2 hover:border-brand-line',
        depth > 0 && 'ml-4',
      )}
    >
      {depth > 0 && <span className="text-[11px] text-info">↳</span>}
      <Lock size={12} strokeWidth={1.7} className="text-faint" />
      <span className="font-mono text-[12.5px] text-ink">{config.name}</span>
    </Link>
  )
}

// One level of the inheritance tree; recurses for branch configs.
function ConfigNodes({ pid, roots, all, depth }: { pid: string; roots: Config[]; all: Config[]; depth: number }) {
  return (
    <>
      {roots.map((c) => (
        <div key={c.id} className="flex flex-col gap-1.5">
          <ConfigCard pid={pid} config={c} depth={depth} />
          <ConfigNodes pid={pid} roots={all.filter((x) => x.inherits_from === c.id)} all={all} depth={depth + 1} />
        </div>
      ))}
    </>
  )
}

function EnvColumn({ pid, env, configs, loading, onAddConfig }: {
  pid: string
  env: Environment
  configs: Config[]
  loading: boolean
  onAddConfig: (env: Environment, bases: Config[]) => void
}) {
  const tone = envTone(env.name)
  const roots = configs.filter((c) => !c.inherits_from || !configs.some((x) => x.id === c.inherits_from))
  return (
    <section className="w-[260px] shrink-0">
      <div className="mb-2 flex items-center justify-between">
        <h3 className="text-[13px] font-semibold text-ink">{env.name}</h3>
        <Pill tone="muted">{loading ? '…' : `${configs.length} config${configs.length === 1 ? '' : 's'}`}</Pill>
      </div>
      <div className={cn('mb-3 h-[3px] w-10 rounded-full', envDotClass[tone])} />
      <button
        type="button"
        onClick={() => onAddConfig(env, configs)}
        className="mb-2 flex w-full items-center justify-center gap-1.5 rounded border border-dashed border-line py-2 text-[12px] font-semibold text-faint hover:border-brand-line hover:text-brand-text"
      >
        <Plus size={13} strokeWidth={1.7} /> Add config
      </button>
      <div className="flex flex-col gap-1.5">
        {loading && <div aria-hidden className="h-9 rounded bg-line-soft" />}
        {!loading && configs.length === 0 && <p className="px-1 text-[12px] text-faint">No configs yet</p>}
        <ConfigNodes pid={pid} roots={roots} all={configs} depth={0} />
      </div>
    </section>
  )
}

export function ProjectBoard() {
  const { projectId } = useParams()
  const pid = projectId!
  const projects = useProjects()
  const envs = useEnvironments(pid)
  const project = projects.data?.find((p) => p.id === pid)
  useTitle(project?.name)
  const [creatingEnv, setCreatingEnv] = useState(false)
  const [addConfig, setAddConfig] = useState<null | { env: Environment; bases: Config[] }>(null)

  const configLists = useQueries({
    queries: (envs.data ?? []).map((e) => ({
      queryKey: ['configs', pid, e.id],
      queryFn: () => endpoints.listConfigs(pid, e.id),
    })),
  })

  if (envs.isError) {
    return <p role="alert" className="mt-16 text-center text-danger">Could not load environments.</p>
  }

  return (
    <div>
      <div className="mb-1 flex items-center gap-2 text-[13px]">
        <Link to="/" className="text-muted hover:text-ink">Projects</Link>
        <span className="text-faint">/</span>
        <span className="font-semibold text-ink">{project?.name ?? '…'}</span>
      </div>
      <p className="mb-5 text-[12.5px] text-faint">
        Inject secrets with the Janus CLI — <code className="rounded bg-brand-soft px-1.5 py-0.5 font-mono text-[11.5px] text-brand-text">janus run</code>
      </p>

      {envs.data?.length === 0 ? (
        <EmptyState
          icon={<Layers size={22} strokeWidth={1.7} />}
          title="No environments yet"
          hint="Environments hold your configs — dev, staging, prod."
          action={
            <button onClick={() => setCreatingEnv(true)} className="rounded bg-brand px-4 py-2 text-[13px] font-semibold text-white shadow-card">
              Create environment
            </button>
          }
        />
      ) : (
        <div className="flex items-center gap-3">
          <div className="flex gap-5 overflow-x-auto pb-2">
            {envs.data?.map((e, i) => (
              <EnvColumn
                key={e.id}
                pid={pid}
                env={e}
                configs={configLists[i]?.data ?? []}
                loading={!configLists[i]?.data}
                onAddConfig={(env, bases) => setAddConfig({ env, bases })}
              />
            ))}
            <button
              type="button"
              onClick={() => setCreatingEnv(true)}
              className="flex h-9 shrink-0 items-center gap-1.5 self-start rounded border border-dashed border-line px-3 text-[12px] font-semibold text-faint hover:border-brand-line hover:text-brand-text"
            >
              <Plus size={13} strokeWidth={1.7} /> Add environment
            </button>
          </div>
        </div>
      )}

      {creatingEnv && (
        <CreateEnvironmentForm pid={pid} onCreated={() => setCreatingEnv(false)} onClose={() => setCreatingEnv(false)} />
      )}
      {addConfig && (
        <CreateConfigForm
          pid={pid}
          eid={addConfig.env.id}
          bases={addConfig.bases}
          onCreated={() => setAddConfig(null)}
          onClose={() => setAddConfig(null)}
        />
      )}
    </div>
  )
}
```

- [ ] **Step 3: run → PASS** (3/3). `npx vitest run src/home/ProjectBoard.test.tsx`. Then `npx vitest run` (full) + `npm run typecheck`.
- [ ] **Step 4: commit.**
```bash
git add web/src/home/ProjectBoard.tsx web/src/home/ProjectBoard.test.tsx
git commit -m "feat(web): env-columns project board with inheritance nesting"
```

---

### Task 4: Wire routes + remove old pages

**Files:** Modify `web/src/App.tsx`; delete `web/src/home/Landing.tsx`, `web/src/home/Landing.test.tsx`, `web/src/home/ProjectOverview.tsx`, `web/src/home/ProjectOverview.test.tsx`.

- [ ] **Step 1: swap routes in `web/src/App.tsx`.** Replace the imports `import { Landing } from './home/Landing'` and `import { ProjectOverview } from './home/ProjectOverview'` with:
```tsx
import { ProjectsList } from './home/ProjectsList'
import { ProjectBoard } from './home/ProjectBoard'
```
and change the two routes:
```tsx
        <Route path="/" element={<ProjectsList />} />
        <Route path="/projects/:projectId" element={<ProjectBoard />} />
```
(Leave all other routes, `PaletteProvider`, and `AppLayout` unchanged.)

- [ ] **Step 2: delete the replaced files.**
```bash
git rm web/src/home/Landing.tsx web/src/home/Landing.test.tsx web/src/home/ProjectOverview.tsx web/src/home/ProjectOverview.test.tsx
```

- [ ] **Step 3: reconcile references.** Grep for stragglers: `grep -rn "Landing\|ProjectOverview" web/src`. Expected: none after the App.tsx swap. If `App.test.tsx` asserted Landing/ProjectOverview-specific text (e.g. "Open a project", "Your secrets, sealed and audited", "Reads 24h"), update those assertions to the new pages' text (e.g. the ProjectsList "Projects" heading / "No projects yet"; the board's breadcrumb). Do NOT weaken unrelated assertions.

- [ ] **Step 4: run + commit.**
Run (from `web/`): `npx vitest run && npm run typecheck && npm run build`. Expected: green; build OK.
```bash
git add web/src/App.tsx
git commit -m "feat(web): route projects list + board; remove old landing/overview"
```

---

### Task 5: Gates, tracker, final review

**Files:** Modify `fe-improvements.md`.

- [ ] **Step 1: full gate battery.** Rebuild the dev container so the served bundle includes R3, then:
```bash
docker compose up -d --build && ./scripts/dev-unseal.sh
cd web && npm run typecheck && npx vitest run && npm run build && npm run smoke
cd .. && go build ./...
```
Expected: typecheck clean; all vitest green; build OK; `npm run smoke` reports BOTH themes render (the root now shows the projects list — the smoke fixtures return one project `acme-api`, so confirm the shell + list render with no pageerror). Go builds.

- [ ] **Step 2: check off `fe-improvements.md`.** In the "Dark redesign" section:
```markdown
- [x] **R3** projects list & env-columns project board & create-project modal
```

- [ ] **Step 3: commit.**
```bash
git add fe-improvements.md
git commit -m "docs(fe): check off R3 projects list & board"
```

- [ ] **Step 4 (controller):** final whole-branch review → PR → merge per standing orders → rebuild container → update memory.

---

## Final gate (after all tasks)

`npm run typecheck && npx vitest run && npm run build && npm run smoke` (web/) + `go build ./...` (root). Then hand off to `superpowers:finishing-a-development-branch`.

**Exit criteria:** `/` shows the searchable/sortable projects card grid (grid+list toggle) with config counts + env dots and a polished create-project modal; `/projects/:id` shows the env-columns board with config cards, inheritance nesting, add-config/add-environment, breadcrumb + `janus run` hint; both themes pass smoke; the old Landing/ProjectOverview are gone and no test references them.
