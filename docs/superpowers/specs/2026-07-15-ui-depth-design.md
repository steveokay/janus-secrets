# UI Depth — Trash/Restore + Per-Key History + Audit Detail (gaps.md #8)

**Date:** 2026-07-15
**Status:** Approved (design)
**Closes:** gaps.md §1.10 (soft-delete restore/undelete), §1.11 (per-key value history), §2.3 (audit viewer expand/timeline)

## Goal

Surface backend capabilities the UI cannot reach today, in three parts on one branch:

- **1.10 — Trash & lifecycle:** in-app soft-delete for projects/environments/configs, a single instance Trash surface to restore or permanently destroy them.
- **1.11 — Per-key value history:** make the secret editor's per-key version chip open a history Sheet with audited historical-value reveal.
- **2.3 — Audit detail:** expandable audit rows (full event incl. hash chain) + client-side date grouping.

Frontend-heavy on the locked **Nocturne** design system (tokens-only, no raw palette/hex, no `dark:` variants; dual light+dark; compose from the existing kit). One small backend addition (a Trash-list endpoint). **Value-free throughout:** no secret value appears in any list; every historical value reveal emits an audit event.

## Non-goals

- No per-key rollback or per-key diff (only config-version rollback exists; out of scope).
- No backend audit aggregation/timeline endpoint (grouping is client-side over the existing flat event list).
- No cursor pagination on the Trash list (deleted items are rare; unbounded is fine, revisit later).
- No new RBAC permission (Trash reuses existing per-entity delete authorization).

---

## Part A — Trash & delete/restore lifecycle (1.10)

### A1. Backend — Trash list endpoint (the only backend work)

**Route:** `GET /v1/trash` (instance-scoped; mounted alongside other authenticated routes, requires `RequireAuth`).

**Response (value-free):**
```json
{
  "projects":     [{ "id": "...", "slug": "...", "name": "...", "deleted_at": "2026-07-14T10:00:00Z" }],
  "environments": [{ "id": "...", "slug": "...", "name": "...", "project_id": "...", "project_name": "...", "deleted_at": "..." }],
  "configs":      [{ "id": "...", "name": "...", "environment_id": "...", "environment_name": "...", "project_id": "...", "project_name": "...", "deleted_at": "..." }]
}
```
- **Per-item authorization filter:** an item is included ONLY if the caller passes the SAME authz check that gates its restore/delete (`authz.ProjectDelete` on the project resource, `authz.EnvDelete`, `authz.ConfigDelete`). This mirrors `handleTokenList`'s per-item `s.can(...)` filter — no existence leak, no new permission. Items the caller can't restore are omitted (not 403'd).
- Not self-audited (a metadata read, like the audit event list / masked list views).
- Parent-name fields (`project_name`, `environment_name`) are best-effort labels for the UI; if a parent is itself deleted/missing, fall back to the id. These are non-secret names.

**Store methods** (new, one per repo):
- `ProjectRepo.ListDeleted(ctx) ([]*Project, error)` — `SELECT <cols> FROM projects WHERE deleted_at IS NOT NULL ORDER BY deleted_at DESC`.
- `EnvironmentRepo.ListDeleted(ctx) ([]*Environment, error)` — same, table `environments`.
- `ConfigRepo.ListDeleted(ctx) ([]*Config, error)` — same, table `configs`.
No migration (the `deleted_at` columns already exist). No index added (trash is small; note as a future optimization if it ever grows).

**Handler** `handleTrashList` (new, e.g. `internal/api/trash_handlers.go`): loads the three `ListDeleted` sets, resolves parent-name labels (reuse existing repo `Get`/lookup; tolerate missing parents), applies the per-item authz filter, returns the grouped JSON. The resource passed to `s.can` uses the existing resource constructors (`authz.Resource{ProjectID: ...}` etc.).

**Existing endpoints reused unchanged:** `DELETE /v1/projects/{pid}` (+`?destroy=true`), `POST /v1/projects/{pid}/restore`, and the environment/config equivalents (`internal/api/{projects,environments,configs}_handlers.go`).

### A2. Frontend — delete actions

Add a soft-delete action, ConfirmDialog-gated, to each entity's primary view:
- **Project:** in `web/src/home/ProjectsList.tsx` / `ProjectBoard.tsx` (per-project menu/action) → `DELETE /v1/projects/{pid}` (soft).
- **Environment / Config:** in their respective views (the config/env management surfaces) → `DELETE …` (soft).

Each: `ConfirmDialog` (tone `danger`, body states it moves to Trash and can be restored) → mutation → `useToast()` success/danger → invalidate the relevant list query (`['projects']`, env/config keys) so the item disappears from the active view.

### A3. Frontend — Trash page

- **Route** `/trash` in `web/src/App.tsx`; **sidebar** entry in `web/src/shell/Sidebar.tsx` (primary nav, near Settings); **⌘K palette** nav entry in `web/src/palette/usePaletteItems.ts`.
- **Data:** `useQuery(['trash'], endpoints.listTrash)`.
- **Layout:** three sections (Projects / Environments / Configs). Each row: name (+ parent path for env/config), relative `deleted_at`, and two actions:
  - **Restore** → `POST …/restore` → toast + invalidate `['trash']` and the entity's active list.
  - **Destroy permanently** → a **typed-confirm Modal** (`web/src/ui/Modal.tsx`): shows a warning (irreversible; for a project, notes it cascades to its environments/configs), requires typing the exact entity name to enable the confirm button → `DELETE …?destroy=true` → toast + invalidate `['trash']`.
- `EmptyState` ("Trash is empty") when all three sections are empty. Skeleton while loading. Hidden/te­nant-safe: if the trash query 403s (no deletable resources visible), render the empty state rather than an error.
- **Copy note:** a one-line explainer that soft-deleting a project keeps its environments/configs and restoring brings them back; destroying is permanent and cascades.

---

## Part B — Per-key value history (1.11) — frontend only

Backend is ready: `GET /v1/configs/{cid}/secrets/{key}/history` returns value-free `[{ value_version, created_at }]`; `GET /v1/configs/{cid}/secrets/{key}?version=N` reveals a specific historical value (audited `secret.reveal`).

- **Trigger:** the per-key version cell in `web/src/secrets/SecretTable.tsx` (currently a static `v{n}` label) becomes an interactive control (button) that opens the history Sheet for that key.
- **`KeyHistorySheet`** (new component under `web/src/secrets/`, reuses `web/src/ui/Sheet.tsx` — same pattern as `VersionHistory.tsx`):
  - Loads `useQuery(['key-history', cid, key], () => endpoints.keyHistory(cid, key))` → renders each version newest-first: `Pill` with `v{value_version}` + relative `created_at`.
  - Each row has a **Reveal** control → calls the audited `endpoints.revealKeyVersion(cid, key, value_version)` (new endpoints fn wrapping `GET …?version=N`), storing the plaintext in **local component state only** (a `Map<version, plaintext>` in RAW form).
  - Revealed plaintext renders in monospace with a copy affordance; **all revealed values are cleared when the Sheet closes** (reset local state on close — the residual-plaintext precedent from `1ca8787`/master-key work: never leave plaintext in the React Query cache; the reveal query uses `gcTime: 0`/no-cache or the value is read into local state and the query result dropped).
- **Value-free default:** the Sheet shows only metadata until the operator explicitly reveals a version; each reveal is one audited request.

---

## Part C — Audit detail + date grouping (2.3) — frontend only

Backend is ready: `GET /v1/audit/events` (cursor pagination) returns the full event (`seq`, `occurred_at`, `actor_kind`, `actor_id`, `actor_name`, `action`, `resource`, `detail`, `result`, `result_code`, `ip`, `prev_hash`, `hash`) with `next_cursor`.

All changes in `web/src/audit/AuditPage.tsx` (+ small helpers):

- **Row expand:** each event row is clickable/keyboard-actionable to toggle an inline expanded panel showing the full event: `seq`, `actor_kind` + `actor_id`, full `resource` (untruncated), `detail`, `result_code`, `ip`, and the hash chain (`prev_hash` → `hash`, monospace). One row open at a time (track `expandedSeq` in state); `aria-expanded` + Enter/Space toggles; Esc closes.
- **Date grouping:** a client-side helper groups the already-fetched rows by calendar day and renders sticky sub-headers labeled **Today / Yesterday / `YYYY-MM-DD`** (derived from `occurred_at` in local time). Pure presentation over `rows`; no extra fetch, no chart.
- Filters (draft/apply), the chain-verify badge, dual export (JSONL/CSV), and cursor infinite-scroll are unchanged.

---

## Testing

- **Backend (`internal/api`, testcontainers):**
  - `GET /v1/trash` returns soft-deleted projects/envs/configs grouped, newest-first, with parent labels.
  - **Per-item authz filter:** a developer scoped to project A sees only A's trashed env/config and not project B's; an owner sees all. Items the caller can't restore are omitted.
  - **Value-free leak test:** seed a config with secrets, soft-delete it, assert the `/v1/trash` response body contains no secret value (names/paths/timestamps only).
  - Restore + destroy already covered by existing handler tests; add a smoke that a destroyed item no longer appears in trash.
  - Store: `ListDeleted` returns only `deleted_at IS NOT NULL` rows.
- **Frontend (vitest + msw, mocks mirroring Go wire shapes):**
  - Trash page: renders grouped items; Restore calls the restore endpoint + refetches; Destroy requires the typed name before enabling, then calls `?destroy=true`; empty state; 403 → empty state.
  - Delete actions: ConfirmDialog gates the soft-delete; success toast; list invalidation.
  - `KeyHistorySheet`: loads history (metadata only), Reveal calls the versioned reveal and shows plaintext, and **closing the Sheet clears all revealed plaintext** (assert no plaintext remains in state/cache).
  - Audit: row expands to full detail incl. hashes; date-group headers render (Today/Yesterday/date); keyboard toggle works.
- **Dual-theme smoke** (`npm run smoke`) passes for `/trash`, the history Sheet, and the audit page in both themes.
- **No-raw-palette** test stays green (tokens only).

## Security & value-free rules (enforced)

- Trash list and per-key history list carry **names/paths/timestamps only** — never a secret value.
- Trash per-item authz filtering prevents leaking the existence of resources the caller cannot restore.
- Permanent destroy is behind a typed-name confirm.
- Historical value reveal goes through the audited `secret.reveal` path; revealed plaintext is **ephemeral** (local state only, cleared on close) and never persisted in the query cache.

## Files (indicative)

- Backend: `internal/api/trash_handlers.go` (new), route wiring in `internal/api/server.go`, `ListDeleted` in `internal/store/{projects,environments,configs}.go`, tests.
- Frontend: `web/src/trash/TrashPage.tsx` (new) + route/sidebar/palette wiring; delete actions in `web/src/home/ProjectsList.tsx`/`ProjectBoard.tsx` and the env/config views; `web/src/secrets/KeyHistorySheet.tsx` (new) + `SecretTable.tsx` trigger; `web/src/audit/AuditPage.tsx` (expand + grouping) + a date-group helper; `web/src/lib/endpoints.ts` (`listTrash`, `restore*`, `destroy*`, `keyHistory`, `revealKeyVersion`).

## Out of scope (explicit)

- Per-key rollback/diff; backend audit timeline aggregation; Trash pagination; a new trash-admin RBAC permission; audit histogram/chart.
