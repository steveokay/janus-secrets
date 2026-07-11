# Operations Console (rotation · sync · dynamic) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a top-level `/operations` web console that lists the three Phase-3 engines' resources cross-project and offers their operational actions (rotate-now, sync-now, pause/resume, edit interval, delete; dynamic issue/renew/revoke, delete-role) — no resource creation.

**Architecture:** Pure-frontend React/TypeScript slice over existing `/v1/rotation`, `/v1/sync`, `/v1/dynamic` endpoints. A single `/operations` page owns a Project filter + tab bar (Rotation/Sync/Dynamic); a shared fan-out hook lists each engine per project (tolerating per-project 403), shared presentational primitives render tables/status/errors, and each engine has its own panel. The one plaintext secret (dynamic issued password) is shown once in an ephemeral modal, never cached.

**Tech Stack:** React 18, TypeScript, react-router-dom v6, @tanstack/react-query v5 (`useQueries`), Tailwind (theme tokens only), lucide-react, Vitest + Testing Library + MSW v2.

---

## Reference: exact existing signatures (ground truth)

**`web/src/lib/api.ts`** — `api = { get, post, put, del }` (each `<T>(path, body?) => Promise<T>`), `request<T>(method, path, body?)` accepts any method string. `ApiError { status, code, message, name:'ApiError' }`. `apiErrorTitle(e)` → server message for 403/409 else `'Request failed.'`. This plan adds `api.patch` in Task 1.

**`web/src/lib/endpoints.ts`** — `endpoints.listProjects(): Promise<Project[]>`, `endpoints.listEnvironments(pid): Promise<Environment[]>`, `endpoints.listConfigs(pid, eid): Promise<Config[]>`. Types: `Project { id; slug; name }`, `Environment { id; slug; name }`, `Config { id; environment_id; name; inherits_from; created_at }`.

**`web/src/ui/`** props:
- `Button({ variant?: 'primary'|'secondary'|'ghost'|'danger'; size?: 'md'|'sm'; block?; loading? } & button attrs)`
- `Modal({ open, onClose, label, className?, children })`
- `Sheet({ open, onOpenChange, title, children })`
- `ConfirmDialog({ open, onOpenChange, title, body, confirmLabel, tone?: 'brand'|'danger', onConfirm })`
- `Pill({ tone: 'success'|'warning'|'danger'|'info'|'brand'|'muted', dot?, className?, children })`
- `EmptyState({ icon?, title, hint?, action?, className? })`
- `useToast()` → `(t: { title: string; tone?: 'success'|'danger' }) => void`
- `Input({ label?, error? } & input attrs)`, `Select({ label?, error?, children } & select attrs)`
- `Skeleton({ className? })`, `Tooltip({ content, children, delay? })`
- `cn(...)` from `web/src/ui/cn.ts`

**`web/src/test/render.tsx`** — `renderApp(ui, { route?, withAuth? })`. Unknown routes fall through to a `*` route that renders `ui`, so `/operations` needs no render.tsx change. Wraps QueryClient (retry:false), Theme, MemoryRouter, Palette.

**`web/src/test/msw.ts`** — `export const server` (msw/node). Tests: `import { http, HttpResponse } from 'msw'` and `server.use(http.get('/path', () => HttpResponse.json(body)))`.

**Test run command (single file):** `cd web && npx vitest run src/operations/<file>`
**Full web tests:** `cd web && npm test -- run`
**Dual-theme smoke:** `cd web && npm run smoke`

---

### Task 1: `api.patch` + ops wire types + endpoints

**Files:**
- Modify: `web/src/lib/api.ts` (add `patch` to the `api` object)
- Create: `web/src/operations/endpoints.ts`
- Test: `web/src/operations/endpoints.test.ts`

- [ ] **Step 1: Write the failing test**

```ts
// web/src/operations/endpoints.test.ts
import { http, HttpResponse } from 'msw'
import { server } from '../test/msw'
import { opsEndpoints } from './endpoints'

test('rotation.list unwraps {policies}', async () => {
  server.use(
    http.get('/v1/rotation/policies', ({ request }) => {
      expect(new URL(request.url).searchParams.get('project_id')).toBe('p1')
      return HttpResponse.json({ policies: [{ id: 'r1', config_id: 'c1', secret_key: 'DB', type: 'postgres', status: 'active' }] })
    }),
  )
  const rows = await opsEndpoints.rotation.list('p1')
  expect(rows).toHaveLength(1)
  expect(rows[0].id).toBe('r1')
})

test('sync.list unwraps {targets}; dynamic.listLeases unwraps {leases}', async () => {
  server.use(http.get('/v1/sync/targets', () => HttpResponse.json({ targets: [{ id: 's1' }] })))
  server.use(http.get('/v1/dynamic/leases', () => HttpResponse.json({ leases: [{ id: 'l1' }] })))
  expect(await opsEndpoints.sync.list('p1')).toHaveLength(1)
  expect(await opsEndpoints.dynamic.listLeases('r1')).toHaveLength(1)
})

test('rotation.setInterval issues a PATCH with interval_seconds', async () => {
  let method = '', body: any
  server.use(
    http.patch('/v1/rotation/policies/r1', async ({ request }) => {
      method = request.method
      body = await request.json()
      return HttpResponse.json({ id: 'r1', interval_seconds: 900 })
    }),
  )
  await opsEndpoints.rotation.setInterval('r1', 900)
  expect(method).toBe('PATCH')
  expect(body).toEqual({ interval_seconds: 900 })
})
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `cd web && npx vitest run src/operations/endpoints.test.ts`
Expected: FAIL — `Cannot find module './endpoints'` and `opsEndpoints` undefined.

- [ ] **Step 3: Add `patch` to `web/src/lib/api.ts`**

In the exported `api` object, add a `patch` method next to `put`:

```ts
export const api = {
  get: <T>(path: string) => request<T>('GET', path),
  post: <T>(path: string, body?: unknown) => request<T>('POST', path, body),
  put: <T>(path: string, body?: unknown) => request<T>('PUT', path, body),
  patch: <T>(path: string, body?: unknown) => request<T>('PATCH', path, body),
  del: <T>(path: string) => request<T>('DELETE', path),
}
```

- [ ] **Step 4: Create `web/src/operations/endpoints.ts`**

```ts
import { api } from '../lib/api'

// --- Wire types (mirror the masked Go view shapes; NO secrets) ---

export interface RotationView {
  id: string
  project_id: string
  config_id: string
  secret_key: string
  type: 'postgres' | 'webhook'
  interval_seconds: number
  status: 'active' | 'paused' | 'failed'
  failure_count: number
  last_error?: string | null
  next_rotation_at: string
  last_rotated_at?: string | null
  last_config_version?: number | null
  created_at: string
}

export interface SyncAddr {
  owner?: string
  repo?: string
  environment?: string
  namespace?: string
  secret_name?: string
}

export interface SyncView {
  id: string
  project_id: string
  config_id: string
  provider: 'github' | 'k8s'
  prune: boolean
  interval_seconds: number
  addr: SyncAddr
  status: 'active' | 'paused' | 'failed'
  failure_count: number
  last_error?: string | null
  next_sync_at: string
  last_synced_at?: string | null
  managed_keys: string[]
  created_at: string
}

export interface DynamicRoleView {
  id: string
  project_id: string
  config_id: string
  name: string
  default_ttl_seconds: number
  max_ttl_seconds: number
  created_at: string
}

export interface DynamicLeaseView {
  id: string
  role_id: string
  status: 'creating' | 'active' | 'expired' | 'revoked' | 'revoke_failed'
  db_username: string
  expires_at: string
  max_expires_at: string
  renewed_at?: string | null
  created_at: string
}

// The ONLY response that carries a plaintext secret (shown once, never cached).
export interface IssuedCreds {
  lease_id: string
  username: string
  password: string
  expires_at: string
}

export const opsEndpoints = {
  rotation: {
    list: (pid: string) =>
      api.get<{ policies: RotationView[] }>(`/v1/rotation/policies?project_id=${encodeURIComponent(pid)}`).then((r) => r.policies ?? []),
    rotateNow: (id: string) => api.post<{ rotated: boolean; config_version: number }>(`/v1/rotation/policies/${id}/rotate`),
    setStatus: (id: string, status: 'active' | 'paused') => api.patch<RotationView>(`/v1/rotation/policies/${id}`, { status }),
    setInterval: (id: string, interval_seconds: number) => api.patch<RotationView>(`/v1/rotation/policies/${id}`, { interval_seconds }),
    remove: (id: string) => api.del<void>(`/v1/rotation/policies/${id}`),
  },
  sync: {
    list: (pid: string) =>
      api.get<{ targets: SyncView[] }>(`/v1/sync/targets?project_id=${encodeURIComponent(pid)}`).then((r) => r.targets ?? []),
    syncNow: (id: string) => api.post<{ synced: boolean }>(`/v1/sync/targets/${id}/sync`),
    setStatus: (id: string, status: 'active' | 'paused') => api.patch<SyncView>(`/v1/sync/targets/${id}`, { status }),
    setInterval: (id: string, interval_seconds: number) => api.patch<SyncView>(`/v1/sync/targets/${id}`, { interval_seconds }),
    remove: (id: string) => api.del<void>(`/v1/sync/targets/${id}`),
  },
  dynamic: {
    listRoles: (cid: string) =>
      api.get<{ roles: DynamicRoleView[] }>(`/v1/dynamic/roles?config_id=${encodeURIComponent(cid)}`).then((r) => r.roles ?? []),
    deleteRole: (id: string) => api.del<void>(`/v1/dynamic/roles/${id}`),
    issue: (roleId: string) => api.post<IssuedCreds>(`/v1/dynamic/roles/${roleId}/creds`),
    listLeases: (roleId: string) =>
      api.get<{ leases: DynamicLeaseView[] }>(`/v1/dynamic/leases?role_id=${encodeURIComponent(roleId)}`).then((r) => r.leases ?? []),
    renew: (leaseId: string) => api.post<DynamicLeaseView>(`/v1/dynamic/leases/${leaseId}/renew`),
    revoke: (leaseId: string) => api.post<{ revoked: boolean }>(`/v1/dynamic/leases/${leaseId}/revoke`),
  },
}
```

- [ ] **Step 5: Run the test to verify it passes**

Run: `cd web && npx vitest run src/operations/endpoints.test.ts`
Expected: PASS (3 tests).

- [ ] **Step 6: Commit**

```bash
git add web/src/lib/api.ts web/src/operations/endpoints.ts web/src/operations/endpoints.test.ts
git commit -m "feat(web/ops): api.patch + ops endpoints & wire types"
```

---

### Task 2: `useProjectConfigMap` + `useFanOut`

**Files:**
- Create: `web/src/operations/useAggregated.ts`
- Test: `web/src/operations/useAggregated.test.tsx`

Builds the shared fan-out core: a `config_id → {names}` map (projects→envs→configs) used to render Project/Config columns, and a generic `useFanOut` that lists per scope and drops 403s.

- [ ] **Step 1: Write the failing test**

```tsx
// web/src/operations/useAggregated.test.tsx
import { http, HttpResponse } from 'msw'
import { screen } from '@testing-library/react'
import { server } from '../test/msw'
import { renderApp } from '../test/render'
import { useProjectConfigMap, useFanOut } from './useAggregated'
import { ApiError } from '../lib/api'

function mockTopology() {
  server.use(http.get('/v1/projects', () => HttpResponse.json({ projects: [
    { id: 'p1', slug: 'acme', name: 'Acme' },
    { id: 'p2', slug: 'billing', name: 'Billing' },
  ] })))
  server.use(http.get('/v1/projects/:pid/environments', ({ params }) =>
    HttpResponse.json({ environments: [{ id: `${params.pid}-e`, slug: 'prod', name: 'prod' }] })))
  server.use(http.get('/v1/projects/:pid/environments/:eid/configs', ({ params }) =>
    HttpResponse.json({ configs: [{ id: `${params.pid}-cfg`, environment_id: params.eid, name: 'prod', inherits_from: null, created_at: 'x' }] })))
}

function MapProbe() {
  const { map, isLoading } = useProjectConfigMap('all')
  if (isLoading) return <div>loading</div>
  const info = map.get('p1-cfg')
  return <div>{info ? `${info.projectName}/${info.envName}/${info.configName}` : 'none'}</div>
}

test('useProjectConfigMap resolves config_id → project/env/config names', async () => {
  mockTopology()
  renderApp(<MapProbe />, { route: '/operations', withAuth: false })
  expect(await screen.findByText('Acme/prod/prod')).toBeInTheDocument()
})

function FanProbe() {
  const scopes = [{ id: 'p1' }, { id: 'p2' }]
  const { perScope, someForbidden, isLoading } = useFanOut(scopes, ['t', 'x'], async (id) => {
    if (id === 'p2') throw new ApiError(403, 'forbidden', 'nope')
    return [{ id: 'a' }, { id: 'b' }]
  })
  if (isLoading) return <div>loading</div>
  return <div>rows={perScope.flatMap((s) => s.data).length} forbidden={String(someForbidden)}</div>
}

test('useFanOut drops a 403 scope and flags someForbidden', async () => {
  renderApp(<FanProbe />, { route: '/operations', withAuth: false })
  expect(await screen.findByText('rows=2 forbidden=true')).toBeInTheDocument()
})
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `cd web && npx vitest run src/operations/useAggregated.test.tsx`
Expected: FAIL — `Cannot find module './useAggregated'`.

- [ ] **Step 3: Create `web/src/operations/useAggregated.ts` (map + fan-out core)**

```ts
import { useQueries, useQuery } from '@tanstack/react-query'
import { ApiError } from '../lib/api'
import { endpoints, type Project } from '../lib/endpoints'

export type ProjectFilter = string | 'all'

export interface ConfigInfo {
  configId: string
  configName: string
  envName: string
  projectId: string
  projectName: string
}

const REFETCH_MS = 15_000

/**
 * Enumerates projects → environments → configs to build a
 * config_id → {names} map used to render the Project/Config columns.
 * A 403 on any sub-list just leaves those entries out of the map (rows
 * fall back to a truncated id); it is never surfaced as an error.
 */
export function useProjectConfigMap(filter: ProjectFilter): {
  map: Map<string, ConfigInfo>
  projects: Project[]
  isLoading: boolean
} {
  const projectsQ = useQuery({ queryKey: ['projects'], queryFn: endpoints.listProjects })
  const all = projectsQ.data ?? []
  const projects = filter === 'all' ? all : all.filter((p) => p.id === filter)

  const envQs = useQueries({
    queries: projects.map((p) => ({
      queryKey: ['ops', 'envs', p.id],
      queryFn: () => endpoints.listEnvironments(p.id),
    })),
  })

  const pairs: { p: Project; eid: string; envName: string }[] = []
  projects.forEach((p, i) => {
    for (const e of envQs[i]?.data ?? []) pairs.push({ p, eid: e.id, envName: e.name })
  })

  const cfgQs = useQueries({
    queries: pairs.map(({ p, eid }) => ({
      queryKey: ['ops', 'configs', p.id, eid],
      queryFn: () => endpoints.listConfigs(p.id, eid),
    })),
  })

  const map = new Map<string, ConfigInfo>()
  pairs.forEach(({ p, envName }, i) => {
    for (const c of cfgQs[i]?.data ?? []) {
      map.set(c.id, { configId: c.id, configName: c.name, envName, projectId: p.id, projectName: p.name })
    }
  })

  const isLoading =
    projectsQ.isLoading || envQs.some((q) => q.isLoading) || cfgQs.some((q) => q.isLoading)
  return { map, projects, isLoading }
}

export interface ScopeResult<T> {
  id: string
  data: T[]
}

/**
 * Runs listFn once per scope in parallel. A 403 result is dropped (empty
 * data + someForbidden=true); any other error sets isError. Single shared
 * shape for rotation (scope=project), sync (scope=project), and dynamic
 * roles (scope=config).
 */
export function useFanOut<T>(
  scopes: { id: string }[],
  keyPrefix: readonly unknown[],
  listFn: (id: string) => Promise<T[]>,
): { perScope: ScopeResult<T>[]; isLoading: boolean; isError: boolean; someForbidden: boolean } {
  const qs = useQueries({
    queries: scopes.map((s) => ({
      queryKey: [...keyPrefix, s.id],
      queryFn: () => listFn(s.id),
      refetchInterval: REFETCH_MS,
    })),
  })

  let someForbidden = false
  let isError = false
  const perScope: ScopeResult<T>[] = scopes.map((s, i) => {
    const q = qs[i]
    if (q?.error) {
      if (q.error instanceof ApiError && q.error.status === 403) someForbidden = true
      else isError = true
      return { id: s.id, data: [] }
    }
    return { id: s.id, data: (q?.data ?? []) as T[] }
  })

  const isLoading = qs.some((q) => q?.isLoading)
  return { perScope, isLoading, isError, someForbidden }
}
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `cd web && npx vitest run src/operations/useAggregated.test.tsx`
Expected: PASS (2 tests).

- [ ] **Step 5: Commit**

```bash
git add web/src/operations/useAggregated.ts web/src/operations/useAggregated.test.tsx
git commit -m "feat(web/ops): project→config map + 403-tolerant fan-out hook"
```

---

### Task 3: Engine aggregators (`useRotation`, `useSync`, `useDynamicRoles`)

**Files:**
- Modify: `web/src/operations/useAggregated.ts` (append aggregators)
- Test: `web/src/operations/aggregators.test.tsx`

Each aggregator combines the config map + fan-out into display rows joined with project/config names.

- [ ] **Step 1: Write the failing test**

```tsx
// web/src/operations/aggregators.test.tsx
import { http, HttpResponse } from 'msw'
import { screen } from '@testing-library/react'
import { server } from '../test/msw'
import { renderApp } from '../test/render'
import { useRotation } from './useAggregated'

function topology() {
  server.use(http.get('/v1/projects', () => HttpResponse.json({ projects: [
    { id: 'p1', slug: 'acme', name: 'Acme' },
    { id: 'p2', slug: 'billing', name: 'Billing' },
  ] })))
  server.use(http.get('/v1/projects/:pid/environments', ({ params }) =>
    HttpResponse.json({ environments: [{ id: `${params.pid}-e`, slug: 'prod', name: 'prod' }] })))
  server.use(http.get('/v1/projects/:pid/environments/:eid/configs', ({ params }) =>
    HttpResponse.json({ configs: [{ id: `${params.pid}-cfg`, environment_id: params.eid, name: 'prod', inherits_from: null, created_at: 'x' }] })))
}

function RotProbe() {
  const { rows, isLoading } = useRotation('all')
  if (isLoading) return <div>loading</div>
  return (
    <ul>
      {rows.map((r) => (
        <li key={r.data.id}>{r.projectName}:{r.cfg?.configName ?? '?'}:{r.data.secret_key}</li>
      ))}
    </ul>
  )
}

test('useRotation merges policies across projects and joins config names', async () => {
  topology()
  server.use(http.get('/v1/rotation/policies', ({ request }) => {
    const pid = new URL(request.url).searchParams.get('project_id')
    if (pid === 'p1') return HttpResponse.json({ policies: [{ id: 'r1', project_id: 'p1', config_id: 'p1-cfg', secret_key: 'DB', type: 'postgres', status: 'active', failure_count: 0, next_rotation_at: 'x', created_at: 'x', interval_seconds: 3600 }] })
    return HttpResponse.json({ policies: [] })
  }))
  renderApp(<RotProbe />, { route: '/operations', withAuth: false })
  expect(await screen.findByText('Acme:prod:DB')).toBeInTheDocument()
})
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `cd web && npx vitest run src/operations/aggregators.test.tsx`
Expected: FAIL — `useRotation` is not exported.

- [ ] **Step 3: Append aggregators to `web/src/operations/useAggregated.ts`**

```ts
import { opsEndpoints, type RotationView, type SyncView, type DynamicRoleView } from './endpoints'

export interface EngineRow<T> {
  data: T
  projectId: string
  projectName: string
  cfg?: ConfigInfo
}

export interface Aggregated<T> {
  rows: EngineRow<T>[]
  isLoading: boolean
  isError: boolean
  someForbidden: boolean
}

// Named use* because it composes hooks (rules-of-hooks): call it only at
// the top level of another hook, never conditionally.
function useProjectScoped<T extends { config_id: string }>(
  filter: ProjectFilter,
  keyPrefix: readonly unknown[],
  listFn: (pid: string) => Promise<T[]>,
): Aggregated<T> {
  const { map, projects, isLoading: mapLoading } = useProjectConfigMap(filter)
  const { perScope, isLoading, isError, someForbidden } = useFanOut(projects, keyPrefix, listFn)
  const byId = new Map(projects.map((p) => [p.id, p]))
  const rows: EngineRow<T>[] = perScope.flatMap(({ id, data }) => {
    const p = byId.get(id)
    return data.map((d) => ({ data: d, projectId: id, projectName: p?.name ?? id, cfg: map.get(d.config_id) }))
  })
  return { rows, isLoading: mapLoading || isLoading, isError, someForbidden }
}

export function useRotation(filter: ProjectFilter): Aggregated<RotationView> {
  return useProjectScoped(filter, ['ops', 'rotation'], opsEndpoints.rotation.list)
}

export function useSync(filter: ProjectFilter): Aggregated<SyncView> {
  return useProjectScoped(filter, ['ops', 'sync'], opsEndpoints.sync.list)
}

/**
 * Dynamic roles are scoped by CONFIG, so the fan-out iterates the config
 * map's configs (not projects). Same 403 tolerance.
 */
export function useDynamicRoles(filter: ProjectFilter): Aggregated<DynamicRoleView> {
  const { map, isLoading: mapLoading } = useProjectConfigMap(filter)
  const configs = [...map.values()]
  const { perScope, isLoading, isError, someForbidden } = useFanOut(
    configs.map((c) => ({ id: c.configId })),
    ['ops', 'dynamic', 'roles'],
    opsEndpoints.dynamic.listRoles,
  )
  const byCfg = new Map(configs.map((c) => [c.configId, c]))
  const rows: EngineRow<DynamicRoleView>[] = perScope.flatMap(({ id, data }) => {
    const cfg = byCfg.get(id)
    return data.map((d) => ({ data: d, projectId: cfg?.projectId ?? '', projectName: cfg?.projectName ?? '', cfg }))
  })
  return { rows, isLoading: mapLoading || isLoading, isError, someForbidden }
}
```

Note: `useProjectScoped` and `useDynamicRoles` call hooks unconditionally at the top level (they ARE hooks composed of hooks) — do not wrap them in conditionals.

- [ ] **Step 4: Run the test to verify it passes**

Run: `cd web && npx vitest run src/operations/aggregators.test.tsx`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add web/src/operations/useAggregated.ts web/src/operations/aggregators.test.tsx
git commit -m "feat(web/ops): rotation/sync/dynamic-roles aggregators"
```

---

### Task 4: Shared presentational primitives (`ops-ui.tsx`)

**Files:**
- Create: `web/src/operations/ops-ui.tsx`
- Test: `web/src/operations/ops-ui.test.tsx`

- [ ] **Step 1: Write the failing test**

```tsx
// web/src/operations/ops-ui.test.tsx
import { render, screen } from '@testing-library/react'
import { StatusPill, RelTime, LastError, OpsTable } from './ops-ui'

test('StatusPill maps engine states to tones/text', () => {
  render(<StatusPill status="failed" />)
  expect(screen.getByText('failed')).toBeInTheDocument()
})

test('RelTime renders a relative string for a recent time', () => {
  const iso = new Date(Date.now() - 3 * 60_000).toISOString()
  render(<RelTime iso={iso} />)
  expect(screen.getByText(/3m ago|just now|2m ago/)).toBeInTheDocument()
})

test('LastError shows a warning marker only when text present', () => {
  const { rerender } = render(<LastError text={null} />)
  expect(screen.queryByLabelText('last error')).toBeNull()
  rerender(<LastError text="apply failed" />)
  expect(screen.getByLabelText('last error')).toBeInTheDocument()
})

test('OpsTable renders forbidden EmptyState when allForbidden', () => {
  render(
    <OpsTable columns={['A']} isLoading={false} isError={false} allForbidden isEmpty={false} forbiddenHint="ask an admin">
      <tr><td>x</td></tr>
    </OpsTable>,
  )
  expect(screen.getByText(/access required/i)).toBeInTheDocument()
})
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `cd web && npx vitest run src/operations/ops-ui.test.tsx`
Expected: FAIL — `Cannot find module './ops-ui'`.

- [ ] **Step 3: Create `web/src/operations/ops-ui.tsx`**

```tsx
import type { ReactNode } from 'react'
import { AlertTriangle } from 'lucide-react'
import { Pill, type Tone } from '../ui/Pill'
import { Tooltip } from '../ui/Tooltip'
import { Skeleton } from '../ui/Skeleton'
import { EmptyState } from '../ui/EmptyState'
import { cn } from '../ui/cn'

const STATUS_TONE: Record<string, Tone> = {
  active: 'success',
  paused: 'muted',
  failed: 'danger',
  creating: 'info',
  expired: 'muted',
  revoked: 'muted',
  revoke_failed: 'danger',
}

export function StatusPill({ status }: { status: string }) {
  return <Pill tone={STATUS_TONE[status] ?? 'muted'} dot>{status}</Pill>
}

export function RelTime({ iso }: { iso?: string | null }) {
  if (!iso) return <span className="text-faint">—</span>
  const t = new Date(iso).getTime()
  if (Number.isNaN(t)) return <span className="text-faint">—</span>
  return (
    <Tooltip content={new Date(iso).toLocaleString()}>
      <span className="text-muted">{relative(t)}</span>
    </Tooltip>
  )
}

function relative(t: number): string {
  const diff = t - Date.now()
  const abs = Math.abs(diff)
  const mins = Math.round(abs / 60_000)
  if (mins < 1) return 'just now'
  const unit = mins < 60 ? `${mins}m` : mins < 1440 ? `${Math.round(mins / 60)}h` : `${Math.round(mins / 1440)}d`
  return diff >= 0 ? `in ${unit}` : `${unit} ago`
}

export function LastError({ text }: { text?: string | null }) {
  if (!text) return null
  return (
    <Tooltip content={text}>
      <span aria-label="last error" className="inline-flex text-danger">
        <AlertTriangle size={14} />
      </span>
    </Tooltip>
  )
}

export function OpsTable({
  columns,
  isLoading,
  isError,
  allForbidden,
  isEmpty,
  forbiddenHint,
  someForbidden,
  emptyTitle = 'Nothing here yet',
  emptyHint,
  children,
}: {
  columns: string[]
  isLoading: boolean
  isError: boolean
  allForbidden: boolean
  isEmpty: boolean
  forbiddenHint?: string
  someForbidden?: boolean
  emptyTitle?: string
  emptyHint?: string
  children: ReactNode
}) {
  if (isLoading) {
    return (
      <div className="space-y-2">
        {[0, 1, 2].map((i) => (
          <Skeleton key={i} className="h-9 w-full" />
        ))}
      </div>
    )
  }
  if (allForbidden) return <EmptyState title="Access required" hint={forbiddenHint} />
  if (isError) return <p role="alert" className="text-danger">Couldn't load. Try again shortly.</p>
  if (isEmpty) return <EmptyState title={emptyTitle} hint={emptyHint} />
  return (
    <div className="overflow-x-auto">
      <table className="w-full min-w-[720px] text-[12.5px]">
        <thead>
          <tr className="border-b border-line text-left text-faint">
            {columns.map((c) => (
              <th key={c} className="px-2 py-1.5 font-medium">{c}</th>
            ))}
          </tr>
        </thead>
        <tbody>{children}</tbody>
      </table>
      {someForbidden && (
        <p className={cn('mt-2 text-[11px] text-faint')}>Some projects are hidden — you don't manage this here.</p>
      )}
    </div>
  )
}
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `cd web && npx vitest run src/operations/ops-ui.test.tsx`
Expected: PASS (4 tests).

- [ ] **Step 5: Commit**

```bash
git add web/src/operations/ops-ui.tsx web/src/operations/ops-ui.test.tsx
git commit -m "feat(web/ops): shared OpsTable/StatusPill/RelTime/LastError primitives"
```

---

### Task 5: Rotation panel

**Files:**
- Create: `web/src/operations/RotationPanel.tsx`
- Test: `web/src/operations/RotationPanel.test.tsx`

- [ ] **Step 1: Write the failing test**

```tsx
// web/src/operations/RotationPanel.test.tsx
import { http, HttpResponse } from 'msw'
import { screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { server } from '../test/msw'
import { renderApp } from '../test/render'
import { RotationPanel } from './RotationPanel'

function topo() {
  server.use(http.get('/v1/projects', () => HttpResponse.json({ projects: [{ id: 'p1', slug: 'acme', name: 'Acme' }] })))
  server.use(http.get('/v1/projects/:pid/environments', () => HttpResponse.json({ environments: [{ id: 'e1', slug: 'prod', name: 'prod' }] })))
  server.use(http.get('/v1/projects/:pid/environments/:eid/configs', () => HttpResponse.json({ configs: [{ id: 'c1', environment_id: 'e1', name: 'prod', inherits_from: null, created_at: 'x' }] })))
}
const POLICY = { id: 'r1', project_id: 'p1', config_id: 'c1', secret_key: 'DB_PASSWORD', type: 'postgres', interval_seconds: 3600, status: 'active', failure_count: 0, last_error: null, next_rotation_at: new Date(Date.now() + 7200_000).toISOString(), last_rotated_at: null, created_at: 'x' }
function mockList(policies = [POLICY]) {
  server.use(http.get('/v1/rotation/policies', () => HttpResponse.json({ policies })))
}

test('lists a policy with its config + status', async () => {
  topo(); mockList()
  renderApp(<RotationPanel filter="all" />, { route: '/operations', withAuth: false })
  expect(await screen.findByText('DB_PASSWORD')).toBeInTheDocument()
  expect(screen.getByText('active')).toBeInTheDocument()
})

test('rotate-now posts to /rotate', async () => {
  topo(); mockList()
  let hit = false
  server.use(http.post('/v1/rotation/policies/r1/rotate', () => { hit = true; return HttpResponse.json({ rotated: true, config_version: 5 }) }))
  renderApp(<RotationPanel filter="all" />, { route: '/operations', withAuth: false })
  await userEvent.click(await screen.findByRole('button', { name: /rotate now/i }))
  expect(hit).toBe(true)
})

test('pause patches status=paused', async () => {
  topo(); mockList()
  let body: any
  server.use(http.patch('/v1/rotation/policies/r1', async ({ request }) => { body = await request.json(); return HttpResponse.json({ ...POLICY, status: 'paused' }) }))
  renderApp(<RotationPanel filter="all" />, { route: '/operations', withAuth: false })
  await userEvent.click(await screen.findByRole('button', { name: /pause/i }))
  expect(body).toEqual({ status: 'paused' })
})

test('all-403 renders access-required', async () => {
  topo()
  server.use(http.get('/v1/rotation/policies', () => HttpResponse.json({ error: { code: 'forbidden', message: 'no' } }, { status: 403 })))
  renderApp(<RotationPanel filter="all" />, { route: '/operations', withAuth: false })
  expect(await screen.findByText(/access required/i)).toBeInTheDocument()
})
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `cd web && npx vitest run src/operations/RotationPanel.test.tsx`
Expected: FAIL — no `RotationPanel`.

- [ ] **Step 3: Create `web/src/operations/RotationPanel.tsx`**

```tsx
import { useState } from 'react'
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { Button } from '../ui/Button'
import { Pill } from '../ui/Pill'
import { Modal } from '../ui/Modal'
import { Input } from '../ui/Input'
import { ConfirmDialog } from '../ui/ConfirmDialog'
import { useToast } from '../ui/Toast'
import { apiErrorTitle } from '../lib/api'
import { opsEndpoints, type RotationView } from './endpoints'
import { useRotation, type EngineRow, type ProjectFilter } from './useAggregated'
import { OpsTable, StatusPill, RelTime, LastError } from './ops-ui'

export function RotationPanel({ filter }: { filter: ProjectFilter }) {
  const { rows, isLoading, isError, someForbidden } = useRotation(filter)
  return (
    <OpsTable
      columns={['Project', 'Config', 'Secret key', 'Type', 'Status', 'Next', 'Last', 'Fails', '']}
      isLoading={isLoading}
      isError={isError}
      allForbidden={someForbidden && rows.length === 0}
      isEmpty={rows.length === 0}
      someForbidden={someForbidden}
      forbiddenHint="Ask a project admin for the rotation role."
      emptyHint="No rotation policies. Create one with `janus rotation create`."
    >
      {rows.map((r) => (
        <RotationRow key={r.data.id} row={r} />
      ))}
    </OpsTable>
  )
}

function RotationRow({ row }: { row: EngineRow<RotationView> }) {
  const qc = useQueryClient()
  const toast = useToast()
  const p = row.data
  const [editing, setEditing] = useState(false)
  const [confirmDel, setConfirmDel] = useState(false)

  const invalidate = () => qc.invalidateQueries({ queryKey: ['ops', 'rotation'] })
  const onErr = (e: unknown) => toast({ title: apiErrorTitle(e), tone: 'danger' })

  const rotate = useMutation({
    mutationFn: () => opsEndpoints.rotation.rotateNow(p.id),
    onSuccess: (r) => { toast({ title: `Rotated → v${r.config_version}`, tone: 'success' }); invalidate() },
    onError: onErr,
  })
  const toggle = useMutation({
    mutationFn: () => opsEndpoints.rotation.setStatus(p.id, p.status === 'paused' ? 'active' : 'paused'),
    onSuccess: () => { invalidate() },
    onError: onErr,
  })
  const del = useMutation({
    mutationFn: () => opsEndpoints.rotation.remove(p.id),
    onSuccess: () => { toast({ title: 'Policy deleted', tone: 'success' }); invalidate() },
    onError: onErr,
  })

  return (
    <tr className="border-b border-line-soft">
      <td className="px-2 py-1.5">{row.projectName}</td>
      <td className="px-2 py-1.5">{row.cfg ? `${row.cfg.envName}/${row.cfg.configName}` : short(p.config_id)}</td>
      <td className="px-2 py-1.5 font-mono">{p.secret_key}</td>
      <td className="px-2 py-1.5"><Pill tone="muted">{p.type}</Pill></td>
      <td className="px-2 py-1.5"><span className="inline-flex items-center gap-1"><StatusPill status={p.status} /><LastError text={p.last_error} /></span></td>
      <td className="px-2 py-1.5"><RelTime iso={p.next_rotation_at} /></td>
      <td className="px-2 py-1.5"><RelTime iso={p.last_rotated_at} /></td>
      <td className="px-2 py-1.5">{p.failure_count}</td>
      <td className="px-2 py-1.5">
        <div className="flex justify-end gap-1">
          <Button size="sm" variant="secondary" loading={rotate.isPending} onClick={() => rotate.mutate()}>Rotate now</Button>
          <Button size="sm" variant="ghost" loading={toggle.isPending} onClick={() => toggle.mutate()}>{p.status === 'paused' ? 'Resume' : 'Pause'}</Button>
          <Button size="sm" variant="ghost" onClick={() => setEditing(true)}>Interval</Button>
          <Button size="sm" variant="ghost" onClick={() => setConfirmDel(true)}>Delete</Button>
        </div>
      </td>
      <IntervalModal
        open={editing}
        onClose={() => setEditing(false)}
        current={p.interval_seconds}
        onSave={(n) => opsEndpoints.rotation.setInterval(p.id, n)}
        afterSave={() => { setEditing(false); invalidate() }}
        onError={onErr}
      />
      <ConfirmDialog
        open={confirmDel}
        onOpenChange={setConfirmDel}
        title="Delete rotation policy?"
        body={<span>This stops scheduled rotation of <b>{p.secret_key}</b>. The current secret value is unchanged.</span>}
        confirmLabel="Delete"
        tone="danger"
        onConfirm={() => del.mutate()}
      />
    </tr>
  )
}

export function IntervalModal({
  open, onClose, current, onSave, afterSave, onError,
}: {
  open: boolean
  onClose: () => void
  current: number
  onSave: (n: number) => Promise<unknown>
  afterSave: () => void
  onError: (e: unknown) => void
}) {
  const [val, setVal] = useState(String(current))
  const save = useMutation({ mutationFn: () => onSave(Number(val)), onSuccess: afterSave, onError })
  return (
    <Modal open={open} onClose={onClose} label="Edit interval">
      <div className="space-y-3">
        <h2 className="text-sm font-semibold text-ink">Rotation interval</h2>
        <Input label="Seconds" type="number" min={1} value={val} onChange={(e) => setVal(e.target.value)} />
        <div className="flex justify-end gap-2">
          <Button variant="ghost" size="sm" onClick={onClose}>Cancel</Button>
          <Button size="sm" loading={save.isPending} disabled={!val || Number(val) < 1} onClick={() => save.mutate()}>Save</Button>
        </div>
      </div>
    </Modal>
  )
}

function short(id: string): string {
  return id.length > 8 ? `${id.slice(0, 8)}…` : id
}
```

Note: `IntervalModal` is exported so `SyncPanel` (Task 6) reuses it — do not duplicate it there.

- [ ] **Step 4: Run the test to verify it passes**

Run: `cd web && npx vitest run src/operations/RotationPanel.test.tsx`
Expected: PASS (4 tests).

- [ ] **Step 5: Commit**

```bash
git add web/src/operations/RotationPanel.tsx web/src/operations/RotationPanel.test.tsx
git commit -m "feat(web/ops): rotation panel (rotate-now/pause/interval/delete)"
```

---

### Task 6: Sync panel

**Files:**
- Create: `web/src/operations/SyncPanel.tsx`
- Test: `web/src/operations/SyncPanel.test.tsx`

Reuses `IntervalModal` from `RotationPanel.tsx`.

- [ ] **Step 1: Write the failing test**

```tsx
// web/src/operations/SyncPanel.test.tsx
import { http, HttpResponse } from 'msw'
import { screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { server } from '../test/msw'
import { renderApp } from '../test/render'
import { SyncPanel } from './SyncPanel'

function topo() {
  server.use(http.get('/v1/projects', () => HttpResponse.json({ projects: [{ id: 'p1', slug: 'acme', name: 'Acme' }] })))
  server.use(http.get('/v1/projects/:pid/environments', () => HttpResponse.json({ environments: [{ id: 'e1', slug: 'prod', name: 'prod' }] })))
  server.use(http.get('/v1/projects/:pid/environments/:eid/configs', () => HttpResponse.json({ configs: [{ id: 'c1', environment_id: 'e1', name: 'prod', inherits_from: null, created_at: 'x' }] })))
}
const GH = { id: 's1', project_id: 'p1', config_id: 'c1', provider: 'github', prune: true, interval_seconds: 3600, addr: { owner: 'acme', repo: 'widgets', environment: 'production' }, status: 'failed', failure_count: 3, last_error: 'apply failed', next_sync_at: new Date().toISOString(), last_synced_at: null, managed_keys: ['A'], created_at: 'x' }

test('renders provider + destination + failed status with last-error marker', async () => {
  topo()
  server.use(http.get('/v1/sync/targets', () => HttpResponse.json({ targets: [GH] })))
  renderApp(<SyncPanel filter="all" />, { route: '/operations', withAuth: false })
  expect(await screen.findByText('acme/widgets:production')).toBeInTheDocument()
  expect(screen.getByText('failed')).toBeInTheDocument()
  expect(screen.getByLabelText('last error')).toBeInTheDocument()
})

test('sync-now posts to /sync', async () => {
  topo()
  server.use(http.get('/v1/sync/targets', () => HttpResponse.json({ targets: [GH] })))
  let hit = false
  server.use(http.post('/v1/sync/targets/s1/sync', () => { hit = true; return HttpResponse.json({ synced: true }) }))
  renderApp(<SyncPanel filter="all" />, { route: '/operations', withAuth: false })
  await userEvent.click(await screen.findByRole('button', { name: /sync now/i }))
  expect(hit).toBe(true)
})
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `cd web && npx vitest run src/operations/SyncPanel.test.tsx`
Expected: FAIL — no `SyncPanel`.

- [ ] **Step 3: Create `web/src/operations/SyncPanel.tsx`**

```tsx
import { useState } from 'react'
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { Button } from '../ui/Button'
import { Pill } from '../ui/Pill'
import { ConfirmDialog } from '../ui/ConfirmDialog'
import { useToast } from '../ui/Toast'
import { apiErrorTitle } from '../lib/api'
import { opsEndpoints, type SyncView, type SyncAddr } from './endpoints'
import { useSync, type EngineRow, type ProjectFilter } from './useAggregated'
import { OpsTable, StatusPill, RelTime, LastError } from './ops-ui'
import { IntervalModal } from './RotationPanel'

export function SyncPanel({ filter }: { filter: ProjectFilter }) {
  const { rows, isLoading, isError, someForbidden } = useSync(filter)
  return (
    <OpsTable
      columns={['Project', 'Config', 'Provider', 'Destination', 'Prune', 'Status', 'Next', 'Last', 'Fails', '']}
      isLoading={isLoading}
      isError={isError}
      allForbidden={someForbidden && rows.length === 0}
      isEmpty={rows.length === 0}
      someForbidden={someForbidden}
      forbiddenHint="Ask a project admin for the sync role."
      emptyHint="No sync targets. Create one with `janus sync create`."
    >
      {rows.map((r) => (
        <SyncRow key={r.data.id} row={r} />
      ))}
    </OpsTable>
  )
}

function destination(addr: SyncAddr): string {
  if (addr.owner || addr.repo) return `${addr.owner ?? '?'}/${addr.repo ?? '?'}${addr.environment ? `:${addr.environment}` : ''}`
  if (addr.namespace || addr.secret_name) return `${addr.namespace ?? '?'}/${addr.secret_name ?? '?'}`
  return '—'
}

function SyncRow({ row }: { row: EngineRow<SyncView> }) {
  const qc = useQueryClient()
  const toast = useToast()
  const t = row.data
  const [editing, setEditing] = useState(false)
  const [confirmDel, setConfirmDel] = useState(false)
  const invalidate = () => qc.invalidateQueries({ queryKey: ['ops', 'sync'] })
  const onErr = (e: unknown) => toast({ title: apiErrorTitle(e), tone: 'danger' })

  const syncNow = useMutation({ mutationFn: () => opsEndpoints.sync.syncNow(t.id), onSuccess: () => { toast({ title: 'Synced', tone: 'success' }); invalidate() }, onError: onErr })
  const toggle = useMutation({ mutationFn: () => opsEndpoints.sync.setStatus(t.id, t.status === 'paused' ? 'active' : 'paused'), onSuccess: invalidate, onError: onErr })
  const del = useMutation({ mutationFn: () => opsEndpoints.sync.remove(t.id), onSuccess: () => { toast({ title: 'Target deleted', tone: 'success' }); invalidate() }, onError: onErr })

  return (
    <tr className="border-b border-line-soft">
      <td className="px-2 py-1.5">{row.projectName}</td>
      <td className="px-2 py-1.5">{row.cfg ? `${row.cfg.envName}/${row.cfg.configName}` : '—'}</td>
      <td className="px-2 py-1.5"><Pill tone="muted">{t.provider}</Pill></td>
      <td className="px-2 py-1.5 font-mono">{destination(t.addr)}</td>
      <td className="px-2 py-1.5">{t.prune ? 'on' : 'off'}</td>
      <td className="px-2 py-1.5"><span className="inline-flex items-center gap-1"><StatusPill status={t.status} /><LastError text={t.last_error} /></span></td>
      <td className="px-2 py-1.5"><RelTime iso={t.next_sync_at} /></td>
      <td className="px-2 py-1.5"><RelTime iso={t.last_synced_at} /></td>
      <td className="px-2 py-1.5">{t.failure_count}</td>
      <td className="px-2 py-1.5">
        <div className="flex justify-end gap-1">
          <Button size="sm" variant="secondary" loading={syncNow.isPending} onClick={() => syncNow.mutate()}>Sync now</Button>
          <Button size="sm" variant="ghost" loading={toggle.isPending} onClick={() => toggle.mutate()}>{t.status === 'paused' ? 'Resume' : 'Pause'}</Button>
          <Button size="sm" variant="ghost" onClick={() => setEditing(true)}>Interval</Button>
          <Button size="sm" variant="ghost" onClick={() => setConfirmDel(true)}>Delete</Button>
        </div>
      </td>
      <IntervalModal open={editing} onClose={() => setEditing(false)} current={t.interval_seconds} onSave={(n) => opsEndpoints.sync.setInterval(t.id, n)} afterSave={() => { setEditing(false); invalidate() }} onError={onErr} />
      <ConfirmDialog open={confirmDel} onOpenChange={setConfirmDel} title="Delete sync target?" body={<span>This stops replicating this config to <b>{destination(t.addr)}</b>. The destination is left as-is.</span>} confirmLabel="Delete" tone="danger" onConfirm={() => del.mutate()} />
    </tr>
  )
}
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `cd web && npx vitest run src/operations/SyncPanel.test.tsx`
Expected: PASS (2 tests).

- [ ] **Step 5: Commit**

```bash
git add web/src/operations/SyncPanel.tsx web/src/operations/SyncPanel.test.tsx
git commit -m "feat(web/ops): sync panel (sync-now/pause/interval/delete)"
```

---

### Task 7: Issued-credentials modal (ephemeral, once-only)

**Files:**
- Create: `web/src/operations/IssuedCredsModal.tsx`
- Test: `web/src/operations/IssuedCredsModal.test.tsx`

The one plaintext-secret surface. It receives already-issued creds as a prop, shows them once, and clears on close. It performs NO query caching.

- [ ] **Step 1: Write the failing test**

```tsx
// web/src/operations/IssuedCredsModal.test.tsx
import { useState } from 'react'
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { IssuedCredsModal } from './IssuedCredsModal'
import type { IssuedCreds } from './endpoints'

const CREDS: IssuedCreds = { lease_id: 'l1', username: 'janus_ro_abc', password: 'p@ss-SHOWN-ONCE', expires_at: new Date().toISOString() }

function Harness() {
  const [creds, setCreds] = useState<IssuedCreds | null>(CREDS)
  return <IssuedCredsModal creds={creds} onClose={() => setCreds(null)} />
}

test('shows the password once, then wipes it from the DOM on close', async () => {
  render(<Harness />)
  expect(screen.getByText('p@ss-SHOWN-ONCE')).toBeInTheDocument()
  expect(screen.getByText(/shown once/i)).toBeInTheDocument()
  await userEvent.click(screen.getByRole('button', { name: /close|done/i }))
  expect(screen.queryByText('p@ss-SHOWN-ONCE')).toBeNull()
})

test('renders nothing when creds is null', () => {
  render(<IssuedCredsModal creds={null} onClose={() => {}} />)
  expect(screen.queryByText(/shown once/i)).toBeNull()
})
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `cd web && npx vitest run src/operations/IssuedCredsModal.test.tsx`
Expected: FAIL — no `IssuedCredsModal`.

- [ ] **Step 3: Create `web/src/operations/IssuedCredsModal.tsx`**

```tsx
import { Copy } from 'lucide-react'
import { Modal } from '../ui/Modal'
import { Button } from '../ui/Button'
import { useToast } from '../ui/Toast'
import type { IssuedCreds } from './endpoints'

/**
 * Ephemeral display of a freshly-issued dynamic credential. The password
 * exists only in the `creds` prop held by the parent's local state; this
 * component never writes it to any cache or log, and the parent clears it
 * on close. There is no re-open.
 */
export function IssuedCredsModal({ creds, onClose }: { creds: IssuedCreds | null; onClose: () => void }) {
  const toast = useToast()
  if (!creds) return null
  const copy = async (label: string, value: string) => {
    try {
      await navigator.clipboard.writeText(value)
      toast({ title: `${label} copied`, tone: 'success' })
    } catch {
      toast({ title: 'Copy failed', tone: 'danger' })
    }
  }
  return (
    <Modal open onClose={onClose} label="Issued credentials">
      <div className="space-y-3">
        <h2 className="text-sm font-semibold text-ink">Dynamic credentials issued</h2>
        <p className="text-[12px] text-danger">Shown once — copy the password now. It will not be shown again.</p>
        <Row label="Username" value={creds.username} onCopy={() => copy('Username', creds.username)} />
        <Row label="Password" value={creds.password} onCopy={() => copy('Password', creds.password)} mono />
        <p className="text-[11px] text-faint">Expires {new Date(creds.expires_at).toLocaleString()}</p>
        <div className="flex justify-end">
          <Button size="sm" onClick={onClose}>Done</Button>
        </div>
      </div>
    </Modal>
  )
}

function Row({ label, value, onCopy, mono }: { label: string; value: string; onCopy: () => void; mono?: boolean }) {
  return (
    <div className="flex items-center justify-between gap-2 rounded border border-line bg-card px-2 py-1.5">
      <div className="min-w-0">
        <div className="text-[10px] uppercase tracking-wide text-faint">{label}</div>
        <div className={mono ? 'truncate font-mono text-[12.5px] text-ink' : 'truncate text-[12.5px] text-ink'}>{value}</div>
      </div>
      <button type="button" aria-label={`copy ${label}`} className="text-muted hover:text-ink" onClick={onCopy}>
        <Copy size={14} />
      </button>
    </div>
  )
}
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `cd web && npx vitest run src/operations/IssuedCredsModal.test.tsx`
Expected: PASS (2 tests).

- [ ] **Step 5: Commit**

```bash
git add web/src/operations/IssuedCredsModal.tsx web/src/operations/IssuedCredsModal.test.tsx
git commit -m "feat(web/ops): ephemeral issued-credentials modal (shown once)"
```

---

### Task 8: Leases sheet (list + renew + revoke)

**Files:**
- Create: `web/src/operations/LeasesSheet.tsx`
- Test: `web/src/operations/LeasesSheet.test.tsx`

- [ ] **Step 1: Write the failing test**

```tsx
// web/src/operations/LeasesSheet.test.tsx
import { http, HttpResponse } from 'msw'
import { screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { server } from '../test/msw'
import { renderApp } from '../test/render'
import { LeasesSheet } from './LeasesSheet'

const LEASE = { id: 'l1', role_id: 'role1', status: 'active', db_username: 'janus_ro_x', expires_at: new Date(Date.now() + 3600_000).toISOString(), max_expires_at: new Date(Date.now() + 7200_000).toISOString(), renewed_at: null, created_at: 'x' }

test('lists a role\'s leases and revokes one', async () => {
  server.use(http.get('/v1/dynamic/leases', ({ request }) => {
    expect(new URL(request.url).searchParams.get('role_id')).toBe('role1')
    return HttpResponse.json({ leases: [LEASE] })
  }))
  let revoked = false
  server.use(http.post('/v1/dynamic/leases/l1/revoke', () => { revoked = true; return HttpResponse.json({ revoked: true }) }))
  renderApp(<LeasesSheet roleId="role1" roleName="readonly" onClose={() => {}} />, { route: '/operations', withAuth: false })
  expect(await screen.findByText('janus_ro_x')).toBeInTheDocument()
  await userEvent.click(screen.getByRole('button', { name: /revoke/i }))
  expect(revoked).toBe(true)
})

test('renew posts to /renew', async () => {
  server.use(http.get('/v1/dynamic/leases', () => HttpResponse.json({ leases: [LEASE] })))
  let hit = false
  server.use(http.post('/v1/dynamic/leases/l1/renew', () => { hit = true; return HttpResponse.json({ ...LEASE, renewed_at: new Date().toISOString() }) }))
  renderApp(<LeasesSheet roleId="role1" roleName="readonly" onClose={() => {}} />, { route: '/operations', withAuth: false })
  await userEvent.click(await screen.findByRole('button', { name: /renew/i }))
  expect(hit).toBe(true)
})
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `cd web && npx vitest run src/operations/LeasesSheet.test.tsx`
Expected: FAIL — no `LeasesSheet`.

- [ ] **Step 3: Create `web/src/operations/LeasesSheet.tsx`**

```tsx
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Sheet } from '../ui/Sheet'
import { Button } from '../ui/Button'
import { Skeleton } from '../ui/Skeleton'
import { EmptyState } from '../ui/EmptyState'
import { useToast } from '../ui/Toast'
import { apiErrorTitle } from '../lib/api'
import { opsEndpoints, type DynamicLeaseView } from './endpoints'
import { StatusPill, RelTime } from './ops-ui'

export function LeasesSheet({ roleId, roleName, onClose }: { roleId: string | null; roleName: string; onClose: () => void }) {
  const q = useQuery({
    queryKey: ['ops', 'dynamic', 'leases', roleId],
    queryFn: () => opsEndpoints.dynamic.listLeases(roleId as string),
    enabled: !!roleId,
    refetchInterval: 15_000,
  })
  return (
    <Sheet open={!!roleId} onOpenChange={(o) => { if (!o) onClose() }} title={`Leases · ${roleName}`}>
      {!roleId ? null : q.isLoading ? (
        <div className="space-y-2">{[0, 1, 2].map((i) => <Skeleton key={i} className="h-12 w-full" />)}</div>
      ) : (q.data ?? []).length === 0 ? (
        <EmptyState title="No leases" hint="Issue credentials to create one." />
      ) : (
        <ul className="space-y-2">
          {(q.data ?? []).map((l) => <LeaseCard key={l.id} lease={l} roleId={roleId} />)}
        </ul>
      )}
    </Sheet>
  )
}

function LeaseCard({ lease, roleId }: { lease: DynamicLeaseView; roleId: string }) {
  const qc = useQueryClient()
  const toast = useToast()
  const invalidate = () => qc.invalidateQueries({ queryKey: ['ops', 'dynamic', 'leases', roleId] })
  const onErr = (e: unknown) => toast({ title: apiErrorTitle(e), tone: 'danger' })
  const renew = useMutation({ mutationFn: () => opsEndpoints.dynamic.renew(lease.id), onSuccess: () => { toast({ title: 'Lease renewed', tone: 'success' }); invalidate() }, onError: onErr })
  const revoke = useMutation({ mutationFn: () => opsEndpoints.dynamic.revoke(lease.id), onSuccess: () => { toast({ title: 'Lease revoked', tone: 'success' }); invalidate() }, onError: onErr })
  const terminal = lease.status === 'revoked' || lease.status === 'expired'
  return (
    <li className="rounded border border-line bg-card p-2.5">
      <div className="flex items-center justify-between">
        <span className="font-mono text-[12.5px] text-ink">{lease.db_username}</span>
        <StatusPill status={lease.status} />
      </div>
      <div className="mt-1 text-[11px] text-muted">Expires <RelTime iso={lease.expires_at} /> · max <RelTime iso={lease.max_expires_at} /></div>
      <div className="mt-2 flex justify-end gap-1">
        <Button size="sm" variant="ghost" disabled={terminal || lease.status !== 'active'} loading={renew.isPending} onClick={() => renew.mutate()}>Renew</Button>
        <Button size="sm" variant="ghost" disabled={terminal} loading={revoke.isPending} onClick={() => revoke.mutate()}>Revoke</Button>
      </div>
    </li>
  )
}
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `cd web && npx vitest run src/operations/LeasesSheet.test.tsx`
Expected: PASS (2 tests).

- [ ] **Step 5: Commit**

```bash
git add web/src/operations/LeasesSheet.tsx web/src/operations/LeasesSheet.test.tsx
git commit -m "feat(web/ops): lease drill-in sheet (renew/revoke)"
```

---

### Task 9: Dynamic panel (roles table + issue + view-leases + delete)

**Files:**
- Create: `web/src/operations/DynamicPanel.tsx`
- Test: `web/src/operations/DynamicPanel.test.tsx`

Imports `IssuedCredsModal` (Task 7) and `LeasesSheet` (Task 8).

- [ ] **Step 1: Write the failing test**

```tsx
// web/src/operations/DynamicPanel.test.tsx
import { http, HttpResponse } from 'msw'
import { screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { server } from '../test/msw'
import { renderApp } from '../test/render'
import { DynamicPanel } from './DynamicPanel'

function topo() {
  server.use(http.get('/v1/projects', () => HttpResponse.json({ projects: [{ id: 'p1', slug: 'acme', name: 'Acme' }] })))
  server.use(http.get('/v1/projects/:pid/environments', () => HttpResponse.json({ environments: [{ id: 'e1', slug: 'prod', name: 'prod' }] })))
  server.use(http.get('/v1/projects/:pid/environments/:eid/configs', () => HttpResponse.json({ configs: [{ id: 'c1', environment_id: 'e1', name: 'prod', inherits_from: null, created_at: 'x' }] })))
}
const ROLE = { id: 'role1', project_id: 'p1', config_id: 'c1', name: 'readonly', default_ttl_seconds: 3600, max_ttl_seconds: 86400, created_at: 'x' }

test('lists roles and issues creds, showing the password once', async () => {
  topo()
  server.use(http.get('/v1/dynamic/roles', () => HttpResponse.json({ roles: [ROLE] })))
  server.use(http.post('/v1/dynamic/roles/role1/creds', () => HttpResponse.json({ lease_id: 'l1', username: 'janus_readonly_x', password: 'ONE-TIME-PW', expires_at: new Date().toISOString() })))
  server.use(http.get('/v1/dynamic/leases', () => HttpResponse.json({ leases: [] })))
  renderApp(<DynamicPanel filter="all" />, { route: '/operations', withAuth: false })
  expect(await screen.findByText('readonly')).toBeInTheDocument()
  await userEvent.click(screen.getByRole('button', { name: /issue/i }))
  expect(await screen.findByText('ONE-TIME-PW')).toBeInTheDocument()
  await userEvent.click(screen.getByRole('button', { name: /done/i }))
  expect(screen.queryByText('ONE-TIME-PW')).toBeNull()
})

test('view leases opens the sheet', async () => {
  topo()
  server.use(http.get('/v1/dynamic/roles', () => HttpResponse.json({ roles: [ROLE] })))
  server.use(http.get('/v1/dynamic/leases', () => HttpResponse.json({ leases: [] })))
  renderApp(<DynamicPanel filter="all" />, { route: '/operations', withAuth: false })
  await userEvent.click(await screen.findByRole('button', { name: /leases/i }))
  expect(await screen.findByText(/Leases · readonly/)).toBeInTheDocument()
})
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `cd web && npx vitest run src/operations/DynamicPanel.test.tsx`
Expected: FAIL — no `DynamicPanel`.

- [ ] **Step 3: Create `web/src/operations/DynamicPanel.tsx`**

```tsx
import { useState } from 'react'
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { Button } from '../ui/Button'
import { ConfirmDialog } from '../ui/ConfirmDialog'
import { useToast } from '../ui/Toast'
import { apiErrorTitle } from '../lib/api'
import { opsEndpoints, type DynamicRoleView, type IssuedCreds } from './endpoints'
import { useDynamicRoles, type EngineRow, type ProjectFilter } from './useAggregated'
import { OpsTable } from './ops-ui'
import { IssuedCredsModal } from './IssuedCredsModal'
import { LeasesSheet } from './LeasesSheet'

export function DynamicPanel({ filter }: { filter: ProjectFilter }) {
  const { rows, isLoading, isError, someForbidden } = useDynamicRoles(filter)
  const [issued, setIssued] = useState<IssuedCreds | null>(null)
  const [leasesFor, setLeasesFor] = useState<{ id: string; name: string } | null>(null)

  return (
    <>
      <OpsTable
        columns={['Project', 'Config', 'Role', 'Default TTL', 'Max TTL', '']}
        isLoading={isLoading}
        isError={isError}
        allForbidden={someForbidden && rows.length === 0}
        isEmpty={rows.length === 0}
        someForbidden={someForbidden}
        forbiddenHint="Listing dynamic roles needs the dynamic:manage role (admin/owner)."
        emptyHint="No dynamic roles. Create one with `janus dynamic roles create`."
      >
        {rows.map((r) => (
          <DynamicRow key={r.data.id} row={r} onIssued={setIssued} onViewLeases={(id, name) => setLeasesFor({ id, name })} />
        ))}
      </OpsTable>
      <IssuedCredsModal creds={issued} onClose={() => setIssued(null)} />
      <LeasesSheet roleId={leasesFor?.id ?? null} roleName={leasesFor?.name ?? ''} onClose={() => setLeasesFor(null)} />
    </>
  )
}

function DynamicRow({
  row, onIssued, onViewLeases,
}: {
  row: EngineRow<DynamicRoleView>
  onIssued: (c: IssuedCreds) => void
  onViewLeases: (id: string, name: string) => void
}) {
  const qc = useQueryClient()
  const toast = useToast()
  const r = row.data
  const [confirmDel, setConfirmDel] = useState(false)
  const onErr = (e: unknown) => toast({ title: apiErrorTitle(e), tone: 'danger' })

  const issue = useMutation({
    mutationFn: () => opsEndpoints.dynamic.issue(r.id),
    onSuccess: (creds) => {
      onIssued(creds)
      qc.invalidateQueries({ queryKey: ['ops', 'dynamic', 'leases', r.id] })
    },
    onError: onErr,
  })
  const del = useMutation({
    mutationFn: () => opsEndpoints.dynamic.deleteRole(r.id),
    onSuccess: () => { toast({ title: 'Role deleted', tone: 'success' }); qc.invalidateQueries({ queryKey: ['ops', 'dynamic', 'roles'] }) },
    onError: onErr,
  })

  return (
    <tr className="border-b border-line-soft">
      <td className="px-2 py-1.5">{row.projectName}</td>
      <td className="px-2 py-1.5">{row.cfg ? `${row.cfg.envName}/${row.cfg.configName}` : '—'}</td>
      <td className="px-2 py-1.5 font-mono">{r.name}</td>
      <td className="px-2 py-1.5">{r.default_ttl_seconds}s</td>
      <td className="px-2 py-1.5">{r.max_ttl_seconds}s</td>
      <td className="px-2 py-1.5">
        <div className="flex justify-end gap-1">
          <Button size="sm" variant="secondary" loading={issue.isPending} onClick={() => issue.mutate()}>Issue</Button>
          <Button size="sm" variant="ghost" onClick={() => onViewLeases(r.id, r.name)}>Leases</Button>
          <Button size="sm" variant="ghost" onClick={() => setConfirmDel(true)}>Delete</Button>
        </div>
      </td>
      <ConfirmDialog
        open={confirmDel}
        onOpenChange={setConfirmDel}
        title="Delete dynamic role?"
        body={<span>This revokes every live lease for <b>{r.name}</b> first, then removes the role.</span>}
        confirmLabel="Delete"
        tone="danger"
        onConfirm={() => del.mutate()}
      />
    </tr>
  )
}
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `cd web && npx vitest run src/operations/DynamicPanel.test.tsx`
Expected: PASS (2 tests).

- [ ] **Step 5: Commit**

```bash
git add web/src/operations/DynamicPanel.tsx web/src/operations/DynamicPanel.test.tsx
git commit -m "feat(web/ops): dynamic panel (roles/issue/leases/delete)"
```

---

### Task 10: OperationsPage + route + sidebar + palette

**Files:**
- Create: `web/src/operations/OperationsPage.tsx`
- Modify: `web/src/App.tsx` (import + route)
- Modify: `web/src/shell/Sidebar.tsx` (PRIMARY entry + icon import)
- Modify: `web/src/palette/usePaletteItems.ts` (NAV_ACTIONS entry)
- Test: `web/src/operations/OperationsPage.test.tsx`

- [ ] **Step 1: Write the failing test**

```tsx
// web/src/operations/OperationsPage.test.tsx
import { http, HttpResponse } from 'msw'
import { screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { server } from '../test/msw'
import { renderApp } from '../test/render'
import { OperationsPage } from './OperationsPage'

function baseTopo() {
  server.use(http.get('/v1/projects', () => HttpResponse.json({ projects: [{ id: 'p1', slug: 'acme', name: 'Acme' }] })))
  server.use(http.get('/v1/projects/:pid/environments', () => HttpResponse.json({ environments: [{ id: 'e1', slug: 'prod', name: 'prod' }] })))
  server.use(http.get('/v1/projects/:pid/environments/:eid/configs', () => HttpResponse.json({ configs: [{ id: 'c1', environment_id: 'e1', name: 'prod', inherits_from: null, created_at: 'x' }] })))
  server.use(http.get('/v1/rotation/policies', () => HttpResponse.json({ policies: [] })))
  server.use(http.get('/v1/sync/targets', () => HttpResponse.json({ targets: [] })))
  server.use(http.get('/v1/dynamic/roles', () => HttpResponse.json({ roles: [] })))
}

test('defaults to the Rotation tab and switches to Sync', async () => {
  baseTopo()
  renderApp(<OperationsPage />, { route: '/operations', withAuth: false })
  expect(await screen.findByRole('tab', { name: /rotation/i })).toHaveAttribute('aria-selected', 'true')
  await userEvent.click(screen.getByRole('tab', { name: /sync/i }))
  expect(screen.getByRole('tab', { name: /sync/i })).toHaveAttribute('aria-selected', 'true')
})

test('honors ?tab=dynamic from the URL', async () => {
  baseTopo()
  renderApp(<OperationsPage />, { route: '/operations?tab=dynamic', withAuth: false })
  expect(await screen.findByRole('tab', { name: /dynamic/i })).toHaveAttribute('aria-selected', 'true')
})
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `cd web && npx vitest run src/operations/OperationsPage.test.tsx`
Expected: FAIL — no `OperationsPage`.

- [ ] **Step 3: Create `web/src/operations/OperationsPage.tsx`**

```tsx
import { useState } from 'react'
import { useSearchParams } from 'react-router-dom'
import { useQuery } from '@tanstack/react-query'
import { endpoints } from '../lib/endpoints'
import { Select } from '../ui/Select'
import { cn } from '../ui/cn'
import { RotationPanel } from './RotationPanel'
import { SyncPanel } from './SyncPanel'
import { DynamicPanel } from './DynamicPanel'
import type { ProjectFilter } from './useAggregated'

const TABS = [
  { id: 'rotation', label: 'Rotation' },
  { id: 'sync', label: 'Sync' },
  { id: 'dynamic', label: 'Dynamic' },
] as const

type TabId = (typeof TABS)[number]['id']

export function OperationsPage() {
  const [params, setParams] = useSearchParams()
  const raw = params.get('tab')
  const tab: TabId = TABS.some((t) => t.id === raw) ? (raw as TabId) : 'rotation'
  const [filter, setFilter] = useState<ProjectFilter>('all')
  const projectsQ = useQuery({ queryKey: ['projects'], queryFn: endpoints.listProjects })

  return (
    <div className="mx-auto max-w-6xl px-6 py-6">
      <header className="mb-4">
        <h1 className="text-lg font-semibold text-ink">Operations</h1>
        <p className="text-[12.5px] text-muted">Rotation, sync, and dynamic credentials across your projects.</p>
      </header>

      <div className="mb-3 flex items-center justify-between gap-3">
        <div role="tablist" aria-label="Operations engines" className="flex gap-1">
          {TABS.map((t) => (
            <button
              key={t.id}
              role="tab"
              aria-selected={tab === t.id}
              className={cn(
                'rounded px-3 py-1.5 text-[12.5px]',
                tab === t.id ? 'bg-brand-soft text-brand-text' : 'text-muted hover:bg-line-soft',
              )}
              onClick={() => setParams((p) => { p.set('tab', t.id); return p }, { replace: true })}
            >
              {t.label}
            </button>
          ))}
        </div>
        <div className="w-56">
          <Select aria-label="Project filter" value={filter} onChange={(e) => setFilter(e.target.value)}>
            <option value="all">All projects</option>
            {(projectsQ.data ?? []).map((p) => (
              <option key={p.id} value={p.id}>{p.name}</option>
            ))}
          </Select>
        </div>
      </div>

      <div role="tabpanel">
        {tab === 'rotation' && <RotationPanel filter={filter} />}
        {tab === 'sync' && <SyncPanel filter={filter} />}
        {tab === 'dynamic' && <DynamicPanel filter={filter} />}
      </div>
    </div>
  )
}
```

- [ ] **Step 4: Run the OperationsPage test to verify it passes**

Run: `cd web && npx vitest run src/operations/OperationsPage.test.tsx`
Expected: PASS (2 tests).

- [ ] **Step 5: Wire the route in `web/src/App.tsx`**

Add the import alongside the other page imports:

```ts
import { OperationsPage } from './operations/OperationsPage'
```

Add the route inside `<Routes>`, after the `/transit` route:

```tsx
<Route path="/operations" element={<OperationsPage />} />
```

- [ ] **Step 6: Add the sidebar entry in `web/src/shell/Sidebar.tsx`**

Add `RefreshCw` to the lucide import line:

```ts
import { LayoutGrid, ScrollText, KeyRound, Users, Shield, Settings, Plus, RefreshCw } from 'lucide-react'
```

Add to the `PRIMARY` array, after the `/transit` entry:

```ts
{ to: '/operations', label: 'Operations', Icon: RefreshCw, match: (p: string) => p === '/operations' },
```

- [ ] **Step 7: Add the palette action in `web/src/palette/usePaletteItems.ts`**

Add to `NAV_ACTIONS`:

```ts
{ label: 'Go to Operations', to: '/operations', keywords: 'operations ops rotation sync dynamic leases credentials' },
```

- [ ] **Step 8: Run the full web test suite to confirm no regressions**

Run: `cd web && npm test -- run`
Expected: PASS — all existing tests plus the new operations suite. If a Sidebar test asserts an exact nav-item count, update it to include "Operations".

- [ ] **Step 9: Commit**

```bash
git add web/src/operations/OperationsPage.tsx web/src/operations/OperationsPage.test.tsx web/src/App.tsx web/src/shell/Sidebar.tsx web/src/palette/usePaletteItems.ts
git commit -m "feat(web/ops): operations page + route + sidebar + palette"
```

---

### Task 11: Docs + full gate sweep

**Files:**
- Modify: `docs/web.md` (document the console)
- Modify: `fe-improvements.md` (mark the item done)
- Verify: full web gates

- [ ] **Step 1: Add a section to `docs/web.md`**

Append a section describing the console (adjust the surrounding heading style to match the file):

```markdown
## Operations console (`/operations`)

A cross-project console for the three Phase-3 engines — **rotation**,
**sync**, and **dynamic credentials** — that are otherwise API/CLI-only.
The page fans out over every project you can see (silently skipping ones
where you lack the engine's role) and shows unified tables with a Project
filter and three tabs:

- **Rotation** — policies with status/next-run; actions: rotate-now,
  pause/resume, edit interval, delete.
- **Sync** — targets with provider/destination/status; actions: sync-now,
  pause/resume, edit interval, delete.
- **Dynamic** — roles (admin/owner: listing needs `dynamic:manage`);
  actions: issue credentials, view/renew/revoke leases, delete role.

The console **cannot create** resources — creating a policy/target/role
requires entering privileged admin DSNs, PATs, k8s tokens, or SQL
templates, which stays in the CLI (`janus rotation|sync|dynamic … create`).
No secret is ever rendered except a freshly **issued** dynamic password,
which is shown once in an ephemeral dialog and never cached.
```

- [ ] **Step 2: Update `fe-improvements.md`**

Add a line under the appropriate section noting the operations console shipped (match the file's existing checkbox/format), e.g.:

```markdown
- [x] Operations console (`/operations`) — rotation/sync/dynamic monitoring + actions (manage, no create).
```

- [ ] **Step 3: Run the no-raw-palette guard**

Run: `cd web && npx vitest run src/test/no-raw-palette.test.ts`
Expected: PASS — the operations files use only token classes (`text-ink`, `text-muted`, `text-faint`, `bg-card`, `bg-brand-soft`, `text-brand-text`, `border-line`, `border-line-soft`, `text-danger`, `bg-line-soft`), no raw palette or hex.

- [ ] **Step 4: Run the full web test suite**

Run: `cd web && npm test -- run`
Expected: PASS — entire suite green.

- [ ] **Step 5: Run the dual-theme smoke check**

Run: `cd web && npm run smoke`
Expected: PASS in both light and dark themes.

- [ ] **Step 6: Build the web bundle to confirm it compiles + embeds**

Run: `cd web && npm run build`
Expected: `tsc` typecheck + Vite build succeed with no type errors.

- [ ] **Step 7: Commit**

```bash
git add docs/web.md fe-improvements.md
git commit -m "docs(web/ops): document the operations console; mark FE item done"
```

---

## Self-review notes (author)

**Spec coverage:** Navigation/route/sidebar/palette → Task 10. Fan-out +
403 tolerance + config-name resolution → Tasks 2–3. Shared primitives →
Task 4. Rotation/Sync/Dynamic panels + actions → Tasks 5, 6, 9. Lease
drill-in → Task 8. Once-only issued password (ephemeral, never cached) →
Task 7 (+ wired in Task 9). Masked-only reads → enforced by consuming only
the view types (Task 1). Inline 403 EmptyState + someForbidden footnote →
Task 4 `OpsTable`, used by every panel. Testing (fan-out merge, per-project
403 skipped, action requests, password-once) → Tasks 2, 3, 5, 6, 9. Dual
theme + no-raw-palette + build → Task 11. Docs → Task 11.

**Type consistency:** `opsEndpoints`, `RotationView`/`SyncView`/
`DynamicRoleView`/`DynamicLeaseView`/`IssuedCreds`, `EngineRow<T>`,
`ProjectFilter`, `ConfigInfo`, `useRotation/useSync/useDynamicRoles`,
`IntervalModal` (defined in Task 5, reused in Task 6), `IssuedCredsModal`
(Task 7, used Task 9), `LeasesSheet` (Task 8, used Task 9), `OpsTable`/
`StatusPill`/`RelTime`/`LastError` (Task 4) — names are consistent across
tasks.

**Known follow-ups (out of scope):** resource creation UI; credential/
prune/addr/SQL editing; the deferred audit fail-closed backlog item.
