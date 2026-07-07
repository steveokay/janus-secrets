# Usage Metrics ("Reads 24h") — Design

**Phase 2, sub-project D.** Lightweight usage metrics derived on-demand from the
existing `audit_events` table. No external metrics stack, no background jobs, no
new persisted state beyond an index.

## Goal

Surface how often secrets are being read:

- **Instance-wide** stat strip atop the root Projects list.
- **Project-scoped** row on the project board (that project's configs only).

Each surface shows: a **Reads 24h** total, **top configs by reads**, and **top
tokens (service tokens) by reads**, for the trailing 24 hours.

Source of truth: `secret.reveal` audit events with `result='success'`. Masked
list views are not audited, so they correctly do not count as reads.

## Non-goals

- Historical trends / sparklines / per-day buckets (deliberately deferred — this
  is an on-demand rolling-24h count, not a rollup table).
- Any metrics beyond secret-read counts.
- Any external metrics/telemetry stack.

## Architecture (backend)

### Migration `000008` — index only

`main` currently ends at `000006`; OIDC PR #34 (unmerged) holds `000007`, so this
takes `000008`. If numbering shifts before merge, rebase the number — it is an
additive index with no dependency on other migrations.

```sql
-- up
CREATE INDEX IF NOT EXISTS audit_events_action_time_idx
  ON audit_events (action, occurred_at);
-- down
DROP INDEX IF EXISTS audit_events_action_time_idx;
```

This keeps the trailing-24h scan on `action='secret.reveal'` cheap.

### `internal/store/metrics.go` — `MetricsRepo`

One method:

```go
type ConfigReads struct { ConfigID, ConfigName, ProjectName string; Reads int64 }
type TokenReads  struct { TokenID, TokenName string; Reads int64 }
type Reads24h    struct { Total int64; TopConfigs []ConfigReads; TopTokens []TokenReads }

// projectID == nil  → instance-wide.
// projectID != nil  → only configs belonging to that project.
func (r *MetricsRepo) Reads24h(ctx context.Context, projectID *string) (Reads24h, error)
```

Queries (all filter `action='secret.reveal' AND result='success' AND
occurred_at > now() - interval '24 hours'`):

- **total:** `count(*)`. When `projectID != nil`, join through the config id
  parsed from `resource` to restrict to that project.
- **top configs:** parse config id with
  `substring(resource from 'configs/([^/]+)/secrets')`, group by it, join
  `configs` for `config_name` (+ project name for the instance view), order by
  reads desc, limit 5. Rows whose parsed id no longer resolves to a config are
  dropped (destroyed configs).
- **top tokens:** `where actor_kind='service_token'`, group by `actor_id`, join
  `service_tokens` for the name, order by reads desc, limit 5.

`projectID` filtering falls out of the same joins — no separate code path.

### API — two routes, one shape

Registered under the existing router; auth mirrors `/v1/audit/*`.

- `GET /v1/metrics/reads-24h` — instance-wide; gated on **instance `AuditRead`**.
- `GET /v1/projects/{pid}/metrics/reads-24h` — project-scoped; gated on
  **project-scoped `AuditRead`**.

Neither self-audits (a metadata read, consistent with `/v1/audit/events`);
authorization **denials** are still recorded via the standard `authorize` path.

Response (both routes, identical shape):

```json
{
  "reads_24h": 1284,
  "top_configs": [
    { "config_id": "c1", "config_name": "prod", "project_name": "api", "reads": 210 }
  ],
  "top_tokens": [
    { "token_id": "t1", "token_name": "ci-deploy", "reads": 88 }
  ]
}
```

`project_name` is populated on the instance route; on the project route it is the
(single) project's name and may be omitted by the client.

## Frontend

### `web/src/metrics/ReadsStrip.tsx`

Composed only from existing kit/token classes (`rounded-card`, `border-line`,
`bg-card`, `text-ink`, `text-faint`, `Pill`) — the mockup shows no metrics
treatment, so we compose from primitives rather than inventing a style. No raw
palette classes, no hex (enforced by `no-raw-palette.test.ts`); renders in both
themes.

Layout: a card with a large **Reads 24h** number and two compact top-5 lists
(configs, tokens) showing **names only** — never secret values, consistent with
the palette security rule. Props allow an instance variant and a project-scoped
variant (same component, different data source + heading).

### Data fetching (`web/src/lib/endpoints.ts`)

```ts
metricsReads24h: () => api.get<Reads24h>('/v1/metrics/reads-24h'),
projectMetricsReads24h: (pid: string) =>
  api.get<Reads24h>(`/v1/projects/${pid}/metrics/reads-24h`),
```

TanStack Query hooks, keyed `['metrics','reads-24h']` and
`['metrics','reads-24h', pid]`.

### Mount points

- Instance strip → top of `web/src/home/ProjectsList.tsx`.
- Project row → `web/src/home/ProjectBoard.tsx`, below the intro, above env columns.

### Graceful degradation (required)

Metrics are supplementary, not load-bearing. A viewer **without `AuditRead`**
receives a 403. On **error or 403 the strip hides itself** — it must never break
or block the surrounding page. Loading → skeleton. Zero reads → renders "0" with
a "No reads yet" hint. `retry: false` on the query so a 403 fails fast.

## Testing

- **Store (`internal/store/metrics_test.go`):** seed `secret.reveal` events across
  multiple configs, tokens, and across the 24h boundary; assert total, per-config
  and per-token grouping + ordering + top-N cap, and that `projectID` filtering
  isolates a project's configs. Include a boundary case at exactly the window edge
  and a destroyed-config row that must be dropped.
- **API E2E (`internal/api`):** shape parity with the JSON above; authz denial for
  a caller lacking `AuditRead`; project route isolates other projects' reads;
  confirm the endpoint does **not** self-audit.
- **Web (`web/src/metrics/*.test.tsx`):** msw mocks mirroring the Go wire shape
  exactly (mock-drift rule); ReadsStrip renders total + both lists; loading
  skeleton; **error/403 hides the strip**; empty (0 reads) state.

## Task decomposition (~8 TDD tasks)

1. Migration `000008` (index) + down.
2. `MetricsRepo.Reads24h` + store tests.
3. Instance endpoint `/v1/metrics/reads-24h` + authz + API test.
4. Project endpoint `/v1/projects/{pid}/metrics/reads-24h` + authz + API test.
5. `endpoints.ts` types + query hooks.
6. `ReadsStrip.tsx` component + tests (loading / error-hides / empty / data).
7. Wire instance strip into `ProjectsList.tsx`.
8. Wire project row into `ProjectBoard.tsx`.

Frontend tasks (5–8) build against msw mocks mirroring the fixed wire shape, so
they can proceed independently of the backend tasks.
