# Usage Metrics ("Reads 24h") Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Surface how often secrets are read — an instance-wide "Reads 24h" strip on the Projects list and a project-scoped row on the project board — derived on-demand from `audit_events`.

**Architecture:** No new persisted state beyond one index. A `MetricsRepo.Reads24h(projectID *string)` aggregates successful `secret.reveal` events over the trailing 24h; two API routes (instance + project-scoped) expose it; a React `ReadsStrip` renders it and hides itself on error/403. Frontend is built first against msw mocks that mirror the fixed Go wire shape, then the backend makes it real.

**Tech Stack:** Go + pgx (`internal/store`, `internal/api`), chi routing, `internal/authz`; React + TS + TanStack Query + Tailwind token classes; vitest + msw; testcontainers for store/api tests.

**Spec:** `docs/superpowers/specs/2026-07-07-usage-metrics-design.md`

---

## File Structure

- `web/src/lib/endpoints.ts` — MODIFY: add `Reads24h`/`ConfigReads`/`TokenReads` types + two endpoint fns.
- `web/src/metrics/hooks.ts` — CREATE: `useReads24h`, `useProjectReads24h` (retry: false).
- `web/src/metrics/ReadsStrip.tsx` — CREATE: `InstanceReadsStrip`, `ProjectReadsStrip`, shared internal `StripView`.
- `web/src/metrics/ReadsStrip.test.tsx` — CREATE: data / loading / error-hides / empty.
- `web/src/home/ProjectsList.tsx` — MODIFY: mount `<InstanceReadsStrip />`.
- `web/src/home/ProjectsList.test.tsx` — MODIFY: assert strip renders with metrics mocked.
- `web/src/home/ProjectBoard.tsx` — MODIFY: mount `<ProjectReadsStrip pid={pid} />`.
- `web/src/home/ProjectBoard.test.tsx` — MODIFY: assert project strip renders.
- `migrations/000008_metrics_index.up.sql` / `.down.sql` — CREATE: composite index.
- `internal/store/metrics.go` — CREATE: `MetricsRepo` + types + `Reads24h`.
- `internal/store/metrics_test.go` — CREATE: aggregation + window + project-filter tests.
- `internal/api/metrics_handlers.go` — CREATE: two handlers + wire shape + `toReads24hResponse`.
- `internal/api/metrics_e2e_test.go` — CREATE: shape / authz-denial / project-isolation / not-self-audited.
- `internal/api/server.go` — MODIFY: register the two routes.

---

## Task 1: Frontend endpoints + query hooks

**Files:**
- Modify: `web/src/lib/endpoints.ts`
- Create: `web/src/metrics/hooks.ts`

- [ ] **Step 1: Add the wire types and endpoint functions to `endpoints.ts`**

Add these interfaces near the other `export interface` blocks (after `VerifyResult`, say):

```ts
// usage metrics (D) — on-demand read counts from audit_events (secret.reveal).
export interface ConfigReads { config_id: string; config_name: string; project_name?: string; reads: number }
export interface TokenReads { token_id: string; token_name: string; reads: number }
export interface Reads24h { reads_24h: number; top_configs: ConfigReads[]; top_tokens: TokenReads[] }
```

Add to the `endpoints` object (after the audit block):

```ts
  // usage metrics (D). Metadata reads (no secret values); NOT self-audited.
  metricsReads24h: () => api.get<Reads24h>('/v1/metrics/reads-24h'),
  projectMetricsReads24h: (pid: string) =>
    api.get<Reads24h>(`/v1/projects/${pid}/metrics/reads-24h`),
```

- [ ] **Step 2: Create the hooks**

`web/src/metrics/hooks.ts`:

```ts
import { useQuery } from '@tanstack/react-query'
import { endpoints } from '../lib/endpoints'

// retry: false — a viewer without AuditRead gets a 403; fail fast so the strip
// can hide itself immediately instead of retrying.
export const useReads24h = () =>
  useQuery({ queryKey: ['metrics', 'reads-24h'], queryFn: endpoints.metricsReads24h, retry: false })

export const useProjectReads24h = (pid: string) =>
  useQuery({
    queryKey: ['metrics', 'reads-24h', pid],
    queryFn: () => endpoints.projectMetricsReads24h(pid),
    retry: false,
  })
```

- [ ] **Step 3: Typecheck**

Run: `cd web && npm run typecheck`
Expected: PASS (no usages yet; types compile).

- [ ] **Step 4: Commit**

```bash
git add web/src/lib/endpoints.ts web/src/metrics/hooks.ts
git commit -m "feat(web): metrics endpoints + reads-24h query hooks"
```

---

## Task 2: `ReadsStrip` component

**Files:**
- Create: `web/src/metrics/ReadsStrip.tsx`
- Test: `web/src/metrics/ReadsStrip.test.tsx`

- [ ] **Step 1: Write the failing test**

`web/src/metrics/ReadsStrip.test.tsx`:

```tsx
import { http, HttpResponse } from 'msw'
import { screen, waitFor } from '@testing-library/react'
import { server } from '../test/msw'
import { renderApp } from '../test/render'
import { InstanceReadsStrip } from './ReadsStrip'

const DATA = {
  reads_24h: 1284,
  top_configs: [{ config_id: 'c1', config_name: 'prod', project_name: 'api', reads: 210 }],
  top_tokens: [{ token_id: 't1', token_name: 'ci-deploy', reads: 88 }],
}

test('renders the total, top configs, and top tokens', async () => {
  server.use(http.get('/v1/metrics/reads-24h', () => HttpResponse.json(DATA)))
  renderApp(<InstanceReadsStrip />, { withAuth: false })
  expect(await screen.findByText('1,284')).toBeInTheDocument()
  expect(screen.getByText('prod')).toBeInTheDocument()
  expect(screen.getByText('ci-deploy')).toBeInTheDocument()
})

test('renders a zero state when there are no reads', async () => {
  server.use(http.get('/v1/metrics/reads-24h', () =>
    HttpResponse.json({ reads_24h: 0, top_configs: [], top_tokens: [] })))
  renderApp(<InstanceReadsStrip />, { withAuth: false })
  expect(await screen.findByText('No reads yet')).toBeInTheDocument()
})

test('hides itself entirely on a 403 (viewer without audit read)', async () => {
  server.use(http.get('/v1/metrics/reads-24h', () =>
    HttpResponse.json({ error: { code: 'forbidden', message: 'denied' } }, { status: 403 })))
  const { container } = renderApp(<InstanceReadsStrip />, { withAuth: false })
  // Nothing user-visible; wait a tick for the query to settle, then assert empty.
  await waitFor(() => expect(screen.queryByText(/reads 24h/i)).toBeNull())
  expect(container.querySelector('[data-metrics-strip]')).toBeNull()
})
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd web && npx vitest run src/metrics/ReadsStrip.test.tsx`
Expected: FAIL — `./ReadsStrip` has no export `InstanceReadsStrip`.

- [ ] **Step 3: Implement the component**

`web/src/metrics/ReadsStrip.tsx`:

```tsx
import type { UseQueryResult } from '@tanstack/react-query'
import { Activity } from 'lucide-react'
import type { Reads24h } from '../lib/endpoints'
import { useReads24h, useProjectReads24h } from './hooks'

function TopList({ title, items }: { title: string; items: { id: string; name: string; reads: number }[] }) {
  if (items.length === 0) return null
  return (
    <div className="min-w-0 flex-1">
      <div className="mb-1.5 text-[11px] font-semibold uppercase tracking-[.08em] text-faint">{title}</div>
      <ul className="flex flex-col gap-1">
        {items.map((it) => (
          <li key={it.id} className="flex items-center justify-between gap-3">
            <span className="truncate font-mono text-[12px] text-muted">{it.name}</span>
            <span className="shrink-0 tabular-nums text-[12px] text-faint">{it.reads.toLocaleString()}</span>
          </li>
        ))}
      </ul>
    </div>
  )
}

function StripBody({ data }: { data: Reads24h }) {
  const configs = data.top_configs.map((c) => ({ id: c.config_id, name: c.config_name, reads: c.reads }))
  const tokens = data.top_tokens.map((t) => ({ id: t.token_id, name: t.token_name, reads: t.reads }))
  return (
    <div data-metrics-strip className="mb-5 rounded-card border border-line bg-card p-4">
      <div className="flex flex-wrap items-start gap-x-8 gap-y-4">
        <div className="shrink-0">
          <div className="flex items-center gap-1.5 text-[11px] font-semibold uppercase tracking-[.08em] text-faint">
            <Activity size={13} strokeWidth={1.7} /> Reads 24h
          </div>
          <div className="mt-1 text-[28px] font-semibold tabular-nums text-ink">{data.reads_24h.toLocaleString()}</div>
          {data.reads_24h === 0 && <div className="text-[12px] text-faint">No reads yet</div>}
        </div>
        <TopList title="Top configs" items={configs} />
        <TopList title="Top tokens" items={tokens} />
      </div>
    </div>
  )
}

// Supplementary UI: it must never block or error the surrounding page.
// Hide on error/403; show a skeleton while loading.
function StripView({ q }: { q: UseQueryResult<Reads24h> }) {
  if (q.isError) return null
  if (q.isLoading) return <div aria-hidden className="mb-5 h-24 rounded-card bg-line-soft" />
  if (!q.data) return null
  return <StripBody data={q.data} />
}

// Two thin wrappers so each calls exactly one hook (rules-of-hooks safe).
export function InstanceReadsStrip() {
  return <StripView q={useReads24h()} />
}
export function ProjectReadsStrip({ pid }: { pid: string }) {
  return <StripView q={useProjectReads24h(pid)} />
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd web && npx vitest run src/metrics/ReadsStrip.test.tsx`
Expected: PASS (3/3).

- [ ] **Step 5: Run the style + type gates**

Run: `cd web && npm run typecheck && npx vitest run src/test/no-raw-palette.test.ts`
Expected: PASS — the component uses only token classes (`text-ink`, `text-faint`, `text-muted`, `border-line`, `bg-card`, `bg-line-soft`, `rounded-card`); no raw palette, no hex.

- [ ] **Step 6: Commit**

```bash
git add web/src/metrics/ReadsStrip.tsx web/src/metrics/ReadsStrip.test.tsx
git commit -m "feat(web): ReadsStrip metric card (data/loading/empty/error-hides)"
```

---

## Task 3: Mount the instance strip on the Projects list

**Files:**
- Modify: `web/src/home/ProjectsList.tsx`
- Test: `web/src/home/ProjectsList.test.tsx`

- [ ] **Step 1: Write/extend the failing test**

Add to `web/src/home/ProjectsList.test.tsx` (keep existing tests; ensure existing `mockProjects`-style handlers remain). Add a metrics handler in the seed and a new assertion:

```tsx
import { http, HttpResponse } from 'msw'
// ...existing imports...

test('shows the instance Reads 24h strip above the projects', async () => {
  server.use(
    http.get('/v1/projects', () => HttpResponse.json({ projects: [{ id: 'p1', slug: 'api', name: 'api' }] })),
    http.get('/v1/projects/:pid/environments', () => HttpResponse.json({ environments: [] })),
    http.get('/v1/metrics/reads-24h', () =>
      HttpResponse.json({ reads_24h: 42, top_configs: [], top_tokens: [] })),
  )
  renderApp(<ProjectsList />, { route: '/', withAuth: false })
  expect(await screen.findByText('42')).toBeInTheDocument()
})
```

- [ ] **Step 2: Run to verify it fails**

Run: `cd web && npx vitest run src/home/ProjectsList.test.tsx`
Expected: FAIL — "42" not found (strip not mounted).

- [ ] **Step 3: Mount the strip**

In `web/src/home/ProjectsList.tsx`, add the import:

```tsx
import { InstanceReadsStrip } from '../metrics/ReadsStrip'
```

In the main `return (` (the branch that renders the projects list, starting `return ( <div>`), insert `<InstanceReadsStrip />` as the first child of the outer `<div>`, before the header row `<div className="mb-4 flex items-center justify-between gap-3">`:

```tsx
  return (
    <div>
      <InstanceReadsStrip />
      <div className="mb-4 flex items-center justify-between gap-3">
```

(Leave the loading/error/empty early-returns unchanged — the strip only shows once there are projects, which is the primary dashboard.)

- [ ] **Step 4: Run to verify it passes**

Run: `cd web && npx vitest run src/home/ProjectsList.test.tsx`
Expected: PASS (all, including the new case).

- [ ] **Step 5: Commit**

```bash
git add web/src/home/ProjectsList.tsx web/src/home/ProjectsList.test.tsx
git commit -m "feat(web): mount instance Reads 24h strip on Projects list"
```

---

## Task 4: Mount the project-scoped row on the board

**Files:**
- Modify: `web/src/home/ProjectBoard.tsx`
- Test: `web/src/home/ProjectBoard.test.tsx`

- [ ] **Step 1: Write/extend the failing test**

Add to `web/src/home/ProjectBoard.test.tsx` (mirror the existing seed pattern; the board route is `/projects/p1`):

```tsx
test('shows the project-scoped Reads 24h row', async () => {
  server.use(
    http.get('/v1/projects', () => HttpResponse.json({ projects: [{ id: 'p1', slug: 'api', name: 'api' }] })),
    http.get('/v1/projects/:pid/environments', () => HttpResponse.json({ environments: [] })),
    http.get('/v1/projects/:pid/metrics/reads-24h', () =>
      HttpResponse.json({ reads_24h: 7, top_configs: [], top_tokens: [] })),
  )
  renderApp(<ProjectBoard />, { route: '/projects/p1', withAuth: false })
  expect(await screen.findByText('7')).toBeInTheDocument()
})
```

- [ ] **Step 2: Run to verify it fails**

Run: `cd web && npx vitest run src/home/ProjectBoard.test.tsx`
Expected: FAIL — "7" not found.

- [ ] **Step 3: Mount the row**

In `web/src/home/ProjectBoard.tsx`, add the import:

```tsx
import { ProjectReadsStrip } from '../metrics/ReadsStrip'
```

Insert `<ProjectReadsStrip pid={pid} />` after the intro `<p>` (the "Inject secrets with the Janus CLI" paragraph, ending at its `</p>`) and before the `{envs.isPending ? (` block:

```tsx
      </p>
      <ProjectReadsStrip pid={pid} />

      {envs.isPending ? (
```

- [ ] **Step 4: Run to verify it passes**

Run: `cd web && npx vitest run src/home/ProjectBoard.test.tsx`
Expected: PASS.

- [ ] **Step 5: Full web gate**

Run: `cd web && npm run typecheck && npx vitest run && npm run build`
Expected: PASS — whole suite green, production bundle builds.

- [ ] **Step 6: Commit**

```bash
git add web/src/home/ProjectBoard.tsx web/src/home/ProjectBoard.test.tsx
git commit -m "feat(web): mount project-scoped Reads 24h row on board"
```

---

## Task 5: Index migration `000008`

**Files:**
- Create: `migrations/000008_metrics_index.up.sql`
- Create: `migrations/000008_metrics_index.down.sql`

> If OIDC PR #34 (which holds `000007`) has NOT merged when you run `make migrate`, `000008` is free and correct. If a different `000008` has appeared on `main`, bump this pair to the next free number — it is an additive index with no ordering dependency.

- [ ] **Step 1: Write the up migration**

`migrations/000008_metrics_index.up.sql`:

```sql
-- Speeds the trailing-24h scan on action='secret.reveal' for usage metrics.
CREATE INDEX IF NOT EXISTS audit_events_action_time_idx
  ON audit_events (action, occurred_at);
```

- [ ] **Step 2: Write the down migration**

`migrations/000008_metrics_index.down.sql`:

```sql
DROP INDEX IF EXISTS audit_events_action_time_idx;
```

- [ ] **Step 3: Apply and verify**

Run: `make migrate`
Expected: applies `000008` with no error. Verify the index exists:
`psql "$DATABASE_URL" -c "\di audit_events_action_time_idx"` → one row.

- [ ] **Step 4: Commit**

```bash
git add migrations/000008_metrics_index.up.sql migrations/000008_metrics_index.down.sql
git commit -m "feat(store): index audit_events(action, occurred_at) for metrics"
```

---

## Task 6: `MetricsRepo.Reads24h`

**Files:**
- Create: `internal/store/metrics.go`
- Test: `internal/store/metrics_test.go`

Follow the store-test harness in `internal/store/audit_test.go` (same testcontainer setup / `*Store` fixture). Its `appendConst` helper shows how to insert `audit_events` rows through the chain; you will insert reveal rows directly with explicit `occurred_at`, `actor_kind`, `actor_id`, `resource`, and `result` so you control the 24h window and attribution. Seed real `projects`/`environments`/`configs`/`service_tokens` rows so the joins resolve.

- [ ] **Step 1: Write the failing test**

`internal/store/metrics_test.go` — assert (mirror the existing harness for store setup):

```go
package store

import (
	"context"
	"testing"
	"time"
)

func TestReads24hAggregates(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t) // same fixture used by audit_test.go
	// Seed structure: project -> env -> two configs; one service token.
	pid, eid := seedProjectEnv(t, st, "api")
	c1 := seedConfig(t, st, eid, "prod")
	c2 := seedConfig(t, st, eid, "staging")
	tok := seedServiceToken(t, st, "ci-deploy")

	// Reveal events: c1 x3 (2 fresh, 1 stale >24h), c2 x1, token-actor x2 on c1.
	insertReveal(t, st, revealArg{resource: "configs/" + c1 + "/secrets", when: mins(-10), actorKind: "user"})
	insertReveal(t, st, revealArg{resource: "configs/" + c1 + "/secrets", when: mins(-30), actorKind: "user"})
	insertReveal(t, st, revealArg{resource: "configs/" + c1 + "/secrets", when: hrs(-30), actorKind: "user"}) // stale
	insertReveal(t, st, revealArg{resource: "configs/" + c2 + "/secrets", when: mins(-5), actorKind: "user"})
	insertReveal(t, st, revealArg{resource: "configs/" + c1 + "/secrets", when: mins(-2), actorKind: "service_token", actorID: tok})
	insertReveal(t, st, revealArg{resource: "configs/" + c1 + "/secrets", when: mins(-3), actorKind: "service_token", actorID: tok})
	// A denied reveal must not count.
	insertReveal(t, st, revealArg{resource: "configs/" + c1 + "/secrets", when: mins(-1), actorKind: "user", result: "denied"})

	m, err := NewMetricsRepo(st).Reads24h(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	// 2 (c1 user fresh) + 1 (c2) + 2 (token) = 5 successful in-window reveals.
	if m.Total != 5 {
		t.Fatalf("total = %d, want 5", m.Total)
	}
	if len(m.TopConfigs) == 0 || m.TopConfigs[0].ConfigID != c1 || m.TopConfigs[0].Reads != 4 {
		t.Fatalf("top config = %+v, want c1 with 4 reads", m.TopConfigs)
	}
	if m.TopConfigs[0].ProjectName != "api" {
		t.Fatalf("project name = %q, want api", m.TopConfigs[0].ProjectName)
	}
	if len(m.TopTokens) != 1 || m.TopTokens[0].TokenID != tok || m.TopTokens[0].Reads != 2 {
		t.Fatalf("top token = %+v, want ci-deploy with 2", m.TopTokens)
	}
	_ = pid

	// Project filter: another project's reveals must be excluded.
	pid2, eid2 := seedProjectEnv(t, st, "web")
	cX := seedConfig(t, st, eid2, "prod")
	insertReveal(t, st, revealArg{resource: "configs/" + cX + "/secrets", when: mins(-1), actorKind: "user"})
	scoped, err := NewMetricsRepo(st).Reads24h(ctx, &pid)
	if err != nil {
		t.Fatal(err)
	}
	if scoped.Total != 5 { // unchanged: cX belongs to pid2, not pid
		t.Fatalf("scoped total = %d, want 5", scoped.Total)
	}
	_ = pid2
}
```

Provide the small helpers (`newTestStore`, `seedProjectEnv`, `seedConfig`, `seedServiceToken`, `insertReveal`, `revealArg`, `mins`, `hrs`) in the test file, reusing the store's `pool` via package-internal access exactly as `audit_test.go` does. `insertReveal` computes `seq` as an incrementing counter and fills `prev_hash`/`hash` with fixed dummy bytes (the metrics query ignores the chain), defaulting `result` to `"success"` and `actorKind`'s `actor_id` to NULL when empty.

- [ ] **Step 2: Run to verify it fails**

Run: `cd internal/store && go test -run TestReads24h ./...`
Expected: FAIL — `NewMetricsRepo` / `Reads24h` undefined.

- [ ] **Step 3: Implement `metrics.go`**

`internal/store/metrics.go`:

```go
package store

import "context"

// ConfigReads / TokenReads / Reads24h are the aggregated read counts for the
// usage dashboard. All counts cover successful secret.reveal events in the
// trailing 24 hours (DB clock).
type ConfigReads struct {
	ConfigID    string
	ConfigName  string
	ProjectName string
	Reads       int64
}
type TokenReads struct {
	TokenID   string
	TokenName string
	Reads     int64
}
type Reads24h struct {
	Total      int64
	TopConfigs []ConfigReads
	TopTokens  []TokenReads
}

// MetricsRepo derives read counts on demand from audit_events. It holds no
// state; construct one per request from the shared *Store.
type MetricsRepo struct{ s *Store }

func NewMetricsRepo(s *Store) *MetricsRepo { return &MetricsRepo{s: s} }

// baseWhere selects successful in-window reveals. Alias the table as `ae`.
const baseWhere = `ae.action = 'secret.reveal' AND ae.result = 'success'
	AND ae.occurred_at > now() - interval '24 hours'`

// Reads24h aggregates the trailing-24h reveal counts. projectID == nil →
// instance-wide; non-nil → restricted to configs belonging to that project.
func (r *MetricsRepo) Reads24h(ctx context.Context, projectID *string) (Reads24h, error) {
	var out Reads24h

	// --- Total ---
	// Instance total counts all reveals (even to since-destroyed configs).
	// Project total must join through the parsed config id to filter by project.
	if projectID == nil {
		totalSQL := `SELECT count(*) FROM audit_events ae WHERE ` + baseWhere
		if err := r.s.pool.QueryRow(ctx, totalSQL).Scan(&out.Total); err != nil {
			return out, mapError(err)
		}
	} else {
		totalSQL := `SELECT count(*) FROM audit_events ae
			JOIN configs c ON c.id::text = substring(ae.resource from 'configs/([^/]+)/secrets')
			JOIN environments e ON e.id = c.environment_id
			WHERE ` + baseWhere + ` AND e.project_id = $1`
		if err := r.s.pool.QueryRow(ctx, totalSQL, *projectID).Scan(&out.Total); err != nil {
			return out, mapError(err)
		}
	}

	// --- Top configs (names required → always join; project filter optional) ---
	cfgSQL := `SELECT c.id::text, c.name, p.name, count(*) AS reads
		FROM audit_events ae
		JOIN configs c ON c.id::text = substring(ae.resource from 'configs/([^/]+)/secrets')
		JOIN environments e ON e.id = c.environment_id
		JOIN projects p ON p.id = e.project_id
		WHERE ` + baseWhere
	cfgArgs := []any{}
	if projectID != nil {
		cfgSQL += ` AND e.project_id = $1`
		cfgArgs = append(cfgArgs, *projectID)
	}
	cfgSQL += ` GROUP BY c.id, c.name, p.name ORDER BY reads DESC, c.name ASC LIMIT 5`
	rows, err := r.s.pool.Query(ctx, cfgSQL, cfgArgs...)
	if err != nil {
		return out, mapError(err)
	}
	for rows.Next() {
		var cr ConfigReads
		if err := rows.Scan(&cr.ConfigID, &cr.ConfigName, &cr.ProjectName, &cr.Reads); err != nil {
			rows.Close()
			return out, mapError(err)
		}
		out.TopConfigs = append(out.TopConfigs, cr)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return out, mapError(err)
	}

	// --- Top tokens (service tokens only; project filter joins config chain) ---
	tokSQL := `SELECT st.id::text, st.name, count(*) AS reads
		FROM audit_events ae
		JOIN service_tokens st ON st.id::text = ae.actor_id`
	if projectID != nil {
		tokSQL += `
		JOIN configs c ON c.id::text = substring(ae.resource from 'configs/([^/]+)/secrets')
		JOIN environments e ON e.id = c.environment_id`
	}
	tokSQL += ` WHERE ` + baseWhere + ` AND ae.actor_kind = 'service_token'`
	tokArgs := []any{}
	if projectID != nil {
		tokSQL += ` AND e.project_id = $1`
		tokArgs = append(tokArgs, *projectID)
	}
	tokSQL += ` GROUP BY st.id, st.name ORDER BY reads DESC, st.name ASC LIMIT 5`
	trows, err := r.s.pool.Query(ctx, tokSQL, tokArgs...)
	if err != nil {
		return out, mapError(err)
	}
	for trows.Next() {
		var tr TokenReads
		if err := trows.Scan(&tr.TokenID, &tr.TokenName, &tr.Reads); err != nil {
			trows.Close()
			return out, mapError(err)
		}
		out.TopTokens = append(out.TopTokens, tr)
	}
	trows.Close()
	if err := trows.Err(); err != nil {
		return out, mapError(err)
	}

	return out, nil
}
```

- [ ] **Step 4: Run to verify it passes**

Run: `cd internal/store && go test -run TestReads24h ./...`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/store/metrics.go internal/store/metrics_test.go
git commit -m "feat(store): MetricsRepo.Reads24h — on-demand reveal aggregation"
```

---

## Task 7: Instance metrics endpoint

**Files:**
- Create: `internal/api/metrics_handlers.go`
- Modify: `internal/api/server.go`
- Test: `internal/api/metrics_e2e_test.go`

Follow the E2E harness in `internal/api/audit_e2e_test.go` (`authStackFull`, `doAuthed`, `rawGet`). Seed a couple of successful `secret.reveal` audit events (via the real reveal path, or by inserting audit rows the way the audit E2E does) so the counts are non-zero.

- [ ] **Step 1: Write the failing test (instance)**

`internal/api/metrics_e2e_test.go`:

```go
package api

import (
	"net/http"
	"testing"
)

func TestMetricsReadsInstance(t *testing.T) {
	env := authStackFull(t) // same harness as audit_e2e_test.go
	// ... perform (or seed) at least one successful secret.reveal ...

	var body reads24hResponse
	status := env.getJSON(t, env.adminToken, "/v1/metrics/reads-24h", &body)
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200", status)
	}
	if body.Reads24h < 1 {
		t.Fatalf("reads_24h = %d, want >= 1", body.Reads24h)
	}

	// A principal lacking AuditRead is denied.
	status = env.getJSON(t, env.viewerToken, "/v1/metrics/reads-24h", &struct{}{})
	if status != http.StatusForbidden {
		t.Fatalf("viewer status = %d, want 403", status)
	}
}
```

Adapt the helper names (`env.getJSON`, `env.adminToken`, `env.viewerToken`) to whatever `audit_e2e_test.go` actually exposes; the point is: 200 + non-zero for an audit-reader, 403 for a non-audit-reader.

- [ ] **Step 2: Run to verify it fails**

Run: `cd internal/api && go test -run TestMetricsReads ./...`
Expected: FAIL — route/handler/`reads24hResponse` undefined.

- [ ] **Step 3: Implement the handlers file**

`internal/api/metrics_handlers.go`:

```go
package api

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/steveokay/janus-secrets/internal/authz"
	"github.com/steveokay/janus-secrets/internal/store"
)

type configReadsRow struct {
	ConfigID    string `json:"config_id"`
	ConfigName  string `json:"config_name"`
	ProjectName string `json:"project_name,omitempty"`
	Reads       int64  `json:"reads"`
}
type tokenReadsRow struct {
	TokenID   string `json:"token_id"`
	TokenName string `json:"token_name"`
	Reads     int64  `json:"reads"`
}
type reads24hResponse struct {
	Reads24h   int64            `json:"reads_24h"`
	TopConfigs []configReadsRow `json:"top_configs"`
	TopTokens  []tokenReadsRow  `json:"top_tokens"`
}

func toReads24hResponse(m store.Reads24h) reads24hResponse {
	cfgs := make([]configReadsRow, 0, len(m.TopConfigs))
	for _, c := range m.TopConfigs {
		cfgs = append(cfgs, configReadsRow{c.ConfigID, c.ConfigName, c.ProjectName, c.Reads})
	}
	toks := make([]tokenReadsRow, 0, len(m.TopTokens))
	for _, t := range m.TopTokens {
		toks = append(toks, tokenReadsRow{t.TokenID, t.TokenName, t.Reads})
	}
	return reads24hResponse{Reads24h: m.Total, TopConfigs: cfgs, TopTokens: toks}
}

// handleMetricsReads serves instance-wide read counts. Instance AuditRead.
// Not self-audited (a metadata read, consistent with /v1/audit/events).
func (s *Server) handleMetricsReads(w http.ResponseWriter, r *http.Request) {
	if !s.authorize(w, r, authz.AuditRead, authz.Instance(), "metrics.reads", "metrics") {
		return
	}
	m, err := store.NewMetricsRepo(s.st).Reads24h(r.Context(), nil)
	if err != nil {
		writeError(w, http.StatusInternalServerError, CodeInternal, "internal error")
		return
	}
	writeJSON(w, http.StatusOK, toReads24hResponse(m))
}

// handleProjectMetricsReads serves per-project read counts. Project AuditRead.
func (s *Server) handleProjectMetricsReads(w http.ResponseWriter, r *http.Request) {
	pid := chi.URLParam(r, "pid")
	if !s.authorize(w, r, authz.AuditRead, authz.Resource{ProjectID: pid}, "metrics.reads", "projects/"+pid+"/metrics") {
		return
	}
	m, err := store.NewMetricsRepo(s.st).Reads24h(r.Context(), &pid)
	if err != nil {
		writeError(w, http.StatusInternalServerError, CodeInternal, "internal error")
		return
	}
	writeJSON(w, http.StatusOK, toReads24hResponse(m))
}
```

- [ ] **Step 4: Register the routes**

In `internal/api/server.go`, inside the `if s.auth != nil && s.authz != nil {` block (alongside the other groups), add:

```go
			r.Group(func(r chi.Router) {
				r.Use(RequireAuth(s.auth))
				r.Get("/v1/metrics/reads-24h", s.handleMetricsReads)
				r.Get("/v1/projects/{pid}/metrics/reads-24h", s.handleProjectMetricsReads)
			})
```

- [ ] **Step 5: Run to verify the instance test passes**

Run: `cd internal/api && go test -run TestMetricsReads ./...`
Expected: PASS (instance case).

- [ ] **Step 6: Commit**

```bash
git add internal/api/metrics_handlers.go internal/api/server.go internal/api/metrics_e2e_test.go
git commit -m "feat(api): /v1/metrics/reads-24h — instance usage metrics"
```

---

## Task 8: Project-scoped endpoint test + isolation

**Files:**
- Modify: `internal/api/metrics_e2e_test.go`

The project route/handler already exist from Task 7. This task proves project scoping and isolation.

- [ ] **Step 1: Write the failing test**

Add to `internal/api/metrics_e2e_test.go`:

```go
func TestMetricsReadsProjectIsolation(t *testing.T) {
	env := authStackFull(t)
	// Two projects, a reveal in each (seed via the harness's reveal path).
	// ... reveal in project A (pidA), reveal in project B (pidB) ...

	var a reads24hResponse
	if st := env.getJSON(t, env.adminToken, "/v1/projects/"+pidA+"/metrics/reads-24h", &a); st != http.StatusOK {
		t.Fatalf("status = %d, want 200", st)
	}
	// Project A's count must not include project B's reveals.
	if a.Reads24h != wantA {
		t.Fatalf("project A reads = %d, want %d (B's reveals excluded)", a.Reads24h, wantA)
	}
}
```

Fill `pidA`, `pidB`, `wantA` from the seeded data.

- [ ] **Step 2: Run to verify it fails, then passes once seeding is correct**

Run: `cd internal/api && go test -run TestMetricsReadsProjectIsolation ./...`
Expected: PASS once the seed + assertion match (the handler/route already exist).

- [ ] **Step 3: Full backend gate**

Run: `go build ./... && go test ./internal/store/... ./internal/api/...`
Expected: PASS.

- [ ] **Step 4: Commit**

```bash
git add internal/api/metrics_e2e_test.go
git commit -m "test(api): project metrics isolation — one project's reads excluded"
```

---

## Final verification (after all tasks)

- [ ] `go build ./... && go test ./...` — all Go tests pass.
- [ ] `cd web && npm run typecheck && npx vitest run && npm run build` — web green.
- [ ] `cd web && npm run smoke` — dual-theme smoke passes with the new strip present.
- [ ] Rebuild dev container and eyeball both surfaces in light + dark:
  `docker compose up -d --build janus && ./scripts/dev-unseal.sh` → http://localhost:8210
