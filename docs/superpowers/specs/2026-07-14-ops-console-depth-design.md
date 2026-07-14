# Operations console depth — design

**Date:** 2026-07-14
**Source item:** `gaps.md` §2.2 (operations console — MED).
**Scope areas:** backend `internal/rotation/`, `internal/secretsync/`, `internal/store/`, `internal/api/`, `migrations/`; frontend `web/src/operations/`.
**Status:** Approved (brainstormed + accepted 2026-07-14).

## Goal

Turn the `/operations` console from a flat manage-and-act surface into a control plane: **column sorting**, **multi-row select + bulk operations**, a **health overview strip**, and a **per-policy/target run-history timeline**. Run-history is backed by durable per-run tables (richer than the current last-run-only state); everything else is derived client-side from data already aggregated.

## Delivery: two sequenced PRs

- **PR 1 — Backend run-history** (Go + SQL). Must land first — the frontend drill-in consumes its endpoints. Independently green and mergeable.
- **PR 2 — Frontend depth** (React). Sorting, bulk ops, health strip, run-history Sheet. Consumes PR 1.

## Non-goals

- No success-rate-over-time metric (deferred; the health strip shows current-state counts only).
- No new dynamic run-history table or endpoint — the existing `dynamic_leases` table + `GET /v1/dynamic/leases?role_id=` already record per-role issuance history and are reused.
- No bulk run-now beyond selected rows; no cross-engine "bulk everything" — bulk acts within one engine tab.
- No change to the existing create flows, per-row actions, 403-tolerant fan-out, or the once-issued dynamic-password path.

## Current-state facts (verified in code/recon)

- **Frontend** (`web/src/operations/`): `OperationsPage` = 3 URL-driven tabs (rotation/sync/dynamic) + project filter, 15s refetch. `useAggregated.ts` builds `EngineRow<T>[]` via a 403-tolerant fan-out (`useProjectScoped` for rotation/sync by project, `useDynamicRoles` by config). `ops-ui.tsx` primitives: `StatusPill`, `RelTime`, `LastError` (inline-expandable, value-free), `OpsTable` (dumb: `columns: string[]` + pre-rendered row `children`; loading/forbidden/error/empty states; **not sortable, no selection**). Panels: `RotationPanel`, `SyncPanel`, `DynamicPanel`, plus `LeasesSheet` (dynamic lease drill-in) and `IssuedCredsModal` (once-issued password).
- **`useRowSelection`** already exists at `web/src/secrets/useRowSelection.ts` (Set-based: `selected/count/toggle/clear/setAll/prune/isSelected`) — generic, to be lifted to a shared module.
- **Rotation** (`internal/rotation/`, `internal/store/rotation.go`, `migrations/000010`): `rotation_policies` stores only latest state (`last_rotated_at`, `last_error`, `failure_count`, `status ∈ {active,failed,paused}`, `last_config_version`, `next_rotation_at`). `MarkRotated`/`MarkFailure` update that row. **No per-run table.** Endpoints (`internal/api/server.go` ~242): `POST/GET /v1/rotation/policies`, `GET/PATCH/DELETE /v1/rotation/policies/{id}`, `POST /v1/rotation/policies/{id}/rotate`. PATCH accepts `status` (pause/resume). `sanitize()` (`execute.go:138`) maps errors to fixed value-free categories.
- **Sync** (`internal/secretsync/`, `internal/store/sync.go`, `migrations/000011`): `sync_targets` stores `last_synced_at`, `last_error`, `failure_count`, `status`, `synced_config_version`, `managed_keys`. `MarkSynced`/`MarkFailure`. **No per-run table.** Endpoints (~253): create/list/get/patch/delete + `POST /v1/sync/targets/{id}/sync`. `sanitize()` (`reconcile.go:168`).
- **Dynamic** (`internal/dynamic/`, `migrations/000012`): roles are stateless templates; `dynamic_leases` records each issuance (`status ∈ {creating,active,revoked,expired,revoke_failed}`, `issued_at/expires_at/renewed_at/revoked_at/last_error`). Endpoints (~264): roles create/list/get/patch/delete + `POST …/creds` (issue); leases `GET /v1/dynamic/leases?role_id=`, `POST …/renew`, `POST …/revoke`. **Reuse leases as dynamic run-history.**
- **Audit**: every engine run is audited (`rotation.rotate`/`sync.reconcile`/`dynamic.creds.issue`/`dynamic.lease.renew`) under a system actor `rotation:{id}`/`sync:{id}`/`dynamic:{id}`. (Not used as the run-history source in this design — the user chose durable tables — but confirms runs are observable.)
- **Failure threshold**: 5 consecutive failures → `status='failed'` (rotation + sync).

---

## PR 1 — Backend run-history

### Data model

**`migrations/000013_rotation_runs.up.sql`** (+ `.down.sql`):
```sql
CREATE TABLE rotation_runs (
  id              BIGSERIAL PRIMARY KEY,
  policy_id       UUID NOT NULL REFERENCES rotation_policies(id) ON DELETE CASCADE,
  started_at      TIMESTAMPTZ NOT NULL,
  ended_at        TIMESTAMPTZ NOT NULL,
  status          TEXT NOT NULL CHECK (status IN ('success','failure')),
  error           TEXT,                 -- sanitized category, NULL on success
  config_version  INTEGER,              -- version produced, NULL on failure
  attempt_num     INTEGER NOT NULL,     -- failure_count at the time (0 = first try)
  created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_rotation_runs_policy ON rotation_runs (policy_id, id DESC);
```

**`migrations/000014_sync_runs.up.sql`** (+ `.down.sql`): identical shape keyed by `target_id UUID … REFERENCES sync_targets(id) ON DELETE CASCADE`, plus `keys_count INTEGER NOT NULL DEFAULT 0` (managed keys touched that run); `config_version` = the synced config version. Index `(target_id, id DESC)`.

Migration numbers assume 000012 is the highest existing; the implementer verifies and bumps if needed.

### Recording (same transaction as the state mark)

The run row is written in the **same DB transaction** as the existing state update, so a policy/target's latest state and its newest run row are always consistent.

- Executor passes a `startedAt time.Time` into the store mark call (captured before the external apply).
- `internal/store/rotation.go`:
  - `MarkRotated(ctx, policyID, startedAt, configVersion, …)` — within its tx, after updating `rotation_policies`, `INSERT INTO rotation_runs (…) VALUES (…, 'success', NULL, configVersion, <failure_count-before-reset>)` then **prune** (below).
  - `MarkFailure(ctx, policyID, startedAt, sanitizedErr, …)` — insert `(…, 'failure', sanitizedErr, NULL, failure_count)`.
- `internal/store/sync.go`: `MarkSynced`/`MarkFailure` analogously into `sync_runs` (with `keys_count`).
- **Signature change**: the Mark methods gain a `startedAt` param (and rotation `MarkRotated` already has the config version; sync `MarkSynced` has the synced version). Callers in `execute.go`/`reconcile.go` capture `startedAt` at run start and pass it. Dynamic is untouched.
- A run-insert failure rolls back the whole mark tx (state + history move together). This is acceptable: the run has already been audited independently; the tables are observability derived from audited work, not a new must-audit invariant.

### Retention (bounded growth)

Immediately after each insert, in the same tx, prune to the newest `RunHistoryCap = 100` rows for that policy/target:
```sql
DELETE FROM rotation_runs
 WHERE policy_id = $1
   AND id NOT IN (SELECT id FROM rotation_runs WHERE policy_id = $1 ORDER BY id DESC LIMIT 100);
```
`RunHistoryCap` is a Go const, not a configurable knob. Single-node friendly; no background job.

### Repository read methods

- `RotationRepo.ListRuns(ctx, policyID, cursor int64, limit int) ([]RotationRun, error)` — keyset by `id` descending (`WHERE policy_id=$1 AND ($cursor=0 OR id < $cursor) ORDER BY id DESC LIMIT $limit`).
- `SyncTargetRepo.ListRuns(ctx, targetID, cursor, limit)` similarly.
- View structs `RotationRun`/`SyncRun` mirror the columns (no wrapped-key/ciphertext fields — runs hold none).

### Endpoints

- `GET /v1/rotation/policies/{id}/runs?limit=&cursor=` → `handleRotationRuns`. AuthZ: the **same read/manage permission the caller already needs to GET that policy** (reuse the policy's scope check; a 403 if unauthorized — the frontend tolerates it). Response mirrors the audit-events pager: `{"runs": [...], "next_cursor": <int|null>}`. `limit` default 50, clamp 1–100.
- `GET /v1/sync/targets/{id}/runs?limit=&cursor=` → `handleSyncRuns`, same shape.
- Register in `internal/api/server.go` alongside the existing rotation/sync routes.
- Dynamic: **none** — reuse `GET /v1/dynamic/leases?role_id=`.

### Security (backend)

- Run rows contain only: timestamps, `status`, the **sanitized** error category (already value-free), an integer `config_version`, `attempt_num`, and (sync) `keys_count`. **No secret values, DSNs, PATs, kubeconfig, or config content** ever enter these tables — they are not in scope of the insert.
- Extend the existing grep-based leak test to assert no seeded secret value appears in `rotation_runs`/`sync_runs` after a rotation/sync run.
- `govulncheck` + `gosec` clean; parameterized SQL only; migrations reversible.

---

## PR 2 — Frontend depth

### Shared hook lift

Move `web/src/secrets/useRowSelection.ts` → `web/src/lib/useRowSelection.ts` (framework-agnostic selection hook), update the editor import, and consume it in operations. No behavior change; its existing tests move with it.

### Sorting

Extend `OpsTable` (`ops-ui.tsx`) from `columns: string[]` to sortable columns: accept a `sort` state + `onSort` and a per-column `sortable` flag; render sortable headers as buttons with an asc/desc caret (mirroring the editor's `HeaderCell`). Each panel owns a `sort` state and sorts its `EngineRow<T>[]` client-side before rendering rows. Per-engine sort keys:
- Rotation/Sync: Status, Project/Config, Next run, Last run, Failures.
- Dynamic: Role name, Project/Config, TTL, (leases in the Sheet: Status, Issued, Expires).
Default sort: **failing first** (status rank) so problems surface. Session-only.

### Multi-select + bulk operations

- Add a leading checkbox column to `OpsTable` (header select-all over visible rows) + per-row checkboxes, driven by the lifted `useRowSelection`.
- A `SelectionBar` (new, `web/src/operations/OpsSelectionBar.tsx`) appears above the table when `count>0`. Per engine:
  - **Rotation / Sync**: `Pause` (`PATCH {status:'paused'}`), `Resume` (`PATCH {status:'active'}`), `Rotate now`/`Sync now` (`POST …/rotate|sync`), `Delete` (`DELETE`, **ConfirmDialog**-gated).
  - **Dynamic roles**: `Delete role` (`DELETE`, ConfirmDialog-gated). Roles are stateless — no pause/resume.
  - **Dynamic leases** (inside the existing `LeasesSheet`): bulk `Revoke` (`POST …/revoke`), ConfirmDialog-gated.
- Bulk = **client-side fan-out** of the existing per-id endpoints (`Promise.allSettled`), then invalidate the engine query and toast a summary: `Paused 4` or `Paused 3 · 1 failed`. Partial failures are reported, never silent. Selection prunes to visible rows and clears after a completed action.
- **Pause/Resume are status-aware**: applying Pause to an already-paused row is a no-op success; the summary counts only rows actually changed. Rotate-now on a paused row follows the existing per-row semantics.

### Health strip

New `web/src/operations/HealthStrip.tsx` above the tabs. Derives counts from the aggregated rows of **all three engines** (it calls the same `useRotation`/`useSync`/`useDynamicRoles` aggregators, which are already cached/deduped by TanStack Query, so no extra network cost):
- Rotation / Sync: `N active · N failing · N paused`, with failing rendered in the danger token and a warning glyph when `>0`.
- Dynamic: `N roles` from the already-loaded role rows (the cheap, always-available count). **No per-role lease fan-out** — lease-level health (expiring/failed) would require querying `listLeases` for every role on every page load, which the strip must not do. Lease detail stays in the drill-in `LeasesSheet` where the fan-out is already scoped to one role. (If a lightweight lease-health count is wanted later, add a dedicated aggregate endpoint — out of scope here.)
- Clicking a Rotation/Sync segment switches to that tab and applies the matching sort/filter (e.g. "failing" → Status sort); the Dynamic segment just switches tabs. Frontend-only, no extra queries beyond the aggregators the tabs already run.

### Run-history drill-in

- Rotation/Sync rows get a "Runs" action (icon button) opening a `Sheet` (`RunHistorySheet.tsx`, mirroring `LeasesSheet`): a table of When (`RelTime`) / Status (`StatusPill`) / Duration (`ended_at − started_at`) / Cfg (`v{config_version}`) / Attempt, paginated via the endpoint's cursor ("Load more"). Uses the same 403 tolerance — if the caller lacks the read perm, the Sheet shows an access hint instead of erroring the page.
- Dynamic rows reuse the **existing `LeasesSheet`** (already the per-role issuance history) — no new Sheet.
- New endpoint wrappers in `web/src/operations/endpoints.ts`: `rotation.runs(policyId, cursor?)`, `sync.runs(targetId, cursor?)` returning `{runs, next_cursor}`; view types mirror the Go wire shapes.

### Security (frontend, unchanged posture)

- The console's only rendered plaintext stays the ephemeral once-issued dynamic password (`IssuedCredsModal`, cleared on close). Run-history is value-free (status/time/category/version). Masked/list/health views carry no secrets.
- Bulk ops invoke the same per-id endpoints, each individually audited server-side. No new plaintext surface; no bulk endpoint that could under-audit.

## Testing

**PR 1 (backend):**
- Store tests (testcontainers Postgres): run recorded on success (config_version set, error null) and failure (error = sanitized category, config_version null); 100-cap prune keeps newest; `ON DELETE CASCADE` removes runs with the policy/target; keyset pagination.
- Handler tests: `{runs, next_cursor}` shape, limit clamp, cursor, RBAC 403 for an unauthorized caller.
- Leak test extended: no seeded secret value in `rotation_runs`/`sync_runs`.
- `make test`, `govulncheck`, `gosec` green.

**PR 2 (frontend):**
- `OpsTable` sort (header click reorders, default failing-first).
- Selection + bulk: N per-id calls issued, summary toast with partial-failure count, ConfirmDialog on delete/revoke, selection clears after.
- Health strip counts from mocked aggregates; click routes to tab + sort.
- `RunHistorySheet` renders rows from a mocked endpoint, "Load more" paginates, 403 → access hint (page not errored).
- MSW mocks mirror the Go wire shapes (repo mock-drift rule).
- Guards (`no-raw-palette`/`dark-aa`/`no-legacy-alias`), tsc, build, dual-theme smoke green.

## Rough decomposition (for the plan)

**PR 1:** (1) migrations 000013/000014 + down. (2) `RotationRun`/`SyncRun` structs + `ListRuns` repo methods. (3) run recording + prune in `MarkRotated/MarkFailure` (rotation) with `startedAt` threaded from `execute.go`. (4) same for sync (`MarkSynced/MarkFailure` + `reconcile.go`, incl. `keys_count`). (5) `handleRotationRuns`/`handleSyncRuns` + routes + RBAC. (6) leak-test extension + gosec/govulncheck.

**PR 2:** (7) lift `useRowSelection` to `web/src/lib/`. (8) `OpsTable` sortable headers + per-panel sort state. (9) checkbox column + `OpsSelectionBar` + bulk fan-out (rotation/sync) with confirm-gated delete. (10) dynamic roles bulk-delete + lease bulk-revoke in `LeasesSheet`. (11) `HealthStrip`. (12) `RunHistorySheet` + endpoint wrappers + dynamic reusing `LeasesSheet`. (13) verification sweep + gaps.md §2.2.
