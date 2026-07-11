# Operations Console (rotation · sync · dynamic) — Web UI Design

**Date:** 2026-07-11
**Status:** Approved (design); pending spec review → implementation plan
**Phase:** Phase 3 follow-up (web console for the three Phase-3 engines)

## Goal

Add a single top-level **`/operations`** console to the Janus SPA that
surfaces the three Phase-3 engines — **static rotation**, **sync
integrations**, and **dynamic Postgres credentials** — which today are
reachable only via REST + the `janus` CLI. The console is **manage + act,
not create**: it lists every resource cross-project and offers the
operational actions (rotate-now, sync-now, pause/resume, edit interval,
delete; dynamic issue/renew/revoke, delete-role). Resource *creation*
(which requires entering admin DSNs, PATs, k8s tokens, and multi-statement
SQL templates) stays in the CLI.

**No backend changes.** This is a pure-frontend slice consuming the
existing `/v1/rotation`, `/v1/sync`, and `/v1/dynamic` routes. It follows
the locked web design system (`docs/design/ui-redesign-mockup.html` +
`web/src/theme.css` tokens) and reuses the established `/transit`
admin-console pattern.

## Non-goals (this slice)

- **No resource creation UI** for any engine (rotation policies, sync
  targets, dynamic roles all created via CLI).
- **No credential / destination / prune / SQL editing** — the only
  editable field exposed is a policy/target's rotation/sync **interval**,
  plus **pause/resume** (a non-secret status flip).
- **No web dynamic-role authoring** (admin DSN + SQL templates stay CLI).
- **No new backend endpoints, migrations, or wire-shape changes.**
- **No charts / history graphs** — last-error text + failure counters +
  timestamps only.
- **No cross-row bulk actions.**

## Decisions locked in brainstorming

1. **Scope:** one combined slice covering all three engines (shared
   console pattern), not three separate slices.
2. **Context model:** **cross-project aggregate** — the page fans out over
   all projects client-side and shows unified tables with a Project
   column and a Project filter, rather than a single-project picker.
3. **Write depth:** **manage + act, no create** (see Goal / Non-goals).
4. **Architecture:** approach #1 — a three-tab page with a shared
   aggregation hook and shared presentational primitives, but each engine
   keeps its own panel component (the engines diverge enough — Dynamic has
   nested leases + a once-only issued password — that a fully generic
   table engine would be leaky).

## Navigation & shell

- **Route:** add `<Route path="/operations" element={<OperationsPage />} />`
  to `web/src/App.tsx`, inside the same `Gate` (auth/seal) wrapper as the
  other authenticated routes.
- **Sidebar:** add a `PRIMARY` entry in `web/src/shell/Sidebar.tsx` —
  label **"Operations"**, lucide icon **`RefreshCw`**, active match
  `p === '/operations'`. Placed after `/transit`.
- **Command palette:** add one `NAV_ACTION` in
  `web/src/palette/usePaletteItems.ts`: `{ label: 'Go to Operations',
  to: '/operations', keywords: 'operations ops rotation sync dynamic
  leases credentials' }`.
- **Page layout:** header (title + one-line description) → **Project
  filter** control (`All ▾` plus one entry per project) → **tab bar**
  (Rotation / Sync / Dynamic) → active panel. Tab selection is stored in
  the URL query (`?tab=sync`, default `rotation`) so it is linkable and
  refresh-stable; the project filter is local component state (default
  `All`).

## File structure

```
web/src/operations/
  OperationsPage.tsx     # route entry: header, ProjectFilter, tab bar, renders active panel
  useAggregated.ts       # cross-project fan-out hook (tolerates per-project 403)
  ops-ui.tsx             # shared presentational: OpsTable, StatusPill, LastError, RelTime
  endpoints.ts           # typed api.ts wrappers + wire-shape TypeScript types for all 3 engines
  RotationPanel.tsx      # rotation policy columns + row actions
  SyncPanel.tsx          # sync target columns + row actions
  DynamicPanel.tsx       # dynamic roles table + lease drill-in Sheet + issue-password modal
```

Tests live beside them under `web/src/operations/*.test.tsx` plus any
shared fixtures, following the existing co-located test convention.

## Architecture

### Shared aggregation hook — `useAggregated`

The engine list endpoints are **scoped** (there is no "list everything"
route):

- rotation: `GET /v1/rotation/policies?project_id=<uuid>`
- sync: `GET /v1/sync/targets?project_id=<uuid>`
- dynamic roles: `GET /v1/dynamic/roles?config_id=<uuid>`
- dynamic leases: `GET /v1/dynamic/leases?role_id=<uuid>`

`useAggregated` centralizes the fan-out:

- Reads the **projects** list via the shared projects query (the same one
  `ProjectsList` uses), fetching it if not already cached — `/operations`
  can be deep-linked without visiting `/` first, so the hook must not
  assume the cache is warm.
- **Config-name resolution (all engines):** rotation/sync/dynamic rows
  carry a `config_id` but no config name, and rows also need a project
  name. So the hook enumerates each in-scope project's environments →
  configs (reusing the per-project env/config queries `ProjectBoard`
  already uses) to build a `config_id → {name, envName, projectName}` map
  used to render the Project/Config columns. If a `config_id` can't be
  resolved (e.g. a config the caller can't see), the row falls back to a
  short truncated id rather than being dropped.
- For **rotation** and **sync**: fires one list query **per project**
  (`?project_id=`) using TanStack parallel queries (`useQueries`), then
  merges the rows and joins them against the config map above.
- For **dynamic**: a deeper variant — for each project, use that same
  config enumeration, then fire one `GET /v1/dynamic/roles?config_id=` per
  config and merge the roles.
- **Per-project (and per-config) 403 is dropped, not surfaced as an
  error.** RBAC is project-scoped, so a user legitimately manages an
  engine on some projects and not others. A forbidden sub-query is
  filtered out of the merged result and flips a `someForbidden` flag.
- When the **Project filter** is set to a specific project, the hook only
  fans out that project (bounding the dynamic tab's config fan-out too).
- Returns `{ rows, isLoading, isError, someForbidden }`. `isError` is true
  only for genuine non-403 failures (e.g. 500), not for permission gaps.

**Query keys:** namespaced per engine + project, e.g.
`['ops','rotation', projectId]`, `['ops','sync', projectId]`,
`['ops','dynamic','roles', configId]`, `['ops','dynamic','leases',
roleId]`. Mutations invalidate the specific key(s) they affect.

**Live refresh:** list queries set `refetchInterval: 15000` and inherit
refetch-on-window-focus, so `failed` policies/targets and expiring leases
update without a manual reload. Relative timestamps ("in 2h", "3m ago")
are computed at render time from the RFC3339 fields.

### Shared presentational primitives — `ops-ui.tsx`

- **`<OpsTable>`** — a thin table shell (header row + body) built from the
  kit's `Card`/tokens, with loading (`Skeleton` rows), empty
  (`EmptyState`), and all-forbidden (`EmptyState` "access required")
  states. Columns and row rendering are passed in by each panel.
- **`<StatusPill status>`** — maps engine status strings to the kit
  `Pill` tones: `active`→success, `paused`→muted, `failed`→danger;
  lease `creating`→info, `active`→success, `expired`/`revoked`→muted,
  `revoke_failed`→danger.
- **`<LastError text>`** — renders a `⚠` icon with the value-free
  `last_error` string inside a `Tooltip` (empty/null → nothing).
- **`<RelTime iso>`** — relative time display with the absolute time in a
  `Tooltip`.

Each panel is a small configuration of columns + row actions over these;
mutations use `useMutation` + `qc.invalidateQueries`, and errors surface
via the existing `Toast` + `apiErrorTitle(e)` (curated 403/409 messages,
generic fallback otherwise) — identical to the `/transit` console.

### Wire types — `endpoints.ts`

Typed wrappers over `web/src/lib/api.ts` (`api.get/post/put/del`, which
already parse the `{error:{code,message}}` envelope into `ApiError`), with
TypeScript interfaces mirroring the masked view shapes exactly (below).

## Wire shapes (existing backend — for reference & mocks)

All list responses are wrapped objects. All timestamps RFC3339 strings.
**None** of these view shapes contain a secret; the sole password-bearing
response is the dynamic issue endpoint.

**Rotation** — `rotationView` (`{ "policies": [...] }` for list):
`id, project_id, config_id, secret_key, type ("postgres"|"webhook"),
interval_seconds, status ("active"|"paused"|"failed"), failure_count,
last_error?, next_rotation_at, last_rotated_at?, last_config_version?,
created_at`.
- `PATCH /v1/rotation/policies/{id}` body: `{ interval_seconds?,
  status?("active"|"paused"), config? }` — UI sends only
  `interval_seconds` or `status`.
- `POST /v1/rotation/policies/{id}/rotate` → `{ rotated: true,
  config_version: N }`.
- `DELETE /v1/rotation/policies/{id}` → 204.
- List filter: `?project_id=` (required). Requires `rotation:manage`
  (admin/owner).

**Sync** — `syncView` (`{ "targets": [...] }` for list):
`id, project_id, config_id, provider ("github"|"k8s"), prune,
interval_seconds, addr { owner?, repo?, environment?, namespace?,
secret_name? }, status, failure_count, last_error?, next_sync_at,
last_synced_at?, managed_keys[], created_at`. The `addr` object is
**not** masked (non-secret destination coordinates) and is used to render
the Destination column (`owner/repo[:environment]` for github,
`namespace/secret_name` for k8s).
- `PATCH /v1/sync/targets/{id}` body: `{ interval_seconds?, prune?,
  status?, addr?, creds? }` — UI sends only `interval_seconds` or
  `status`.
- `POST /v1/sync/targets/{id}/sync` → `{ synced: true }`.
- `DELETE /v1/sync/targets/{id}` → 204.
- List filter: `?project_id=` (required). Requires `sync:manage`
  (admin/owner).

**Dynamic roles** — `roleViewJSON` (`{ "roles": [...] }` for list):
`id, project_id, config_id, name, default_ttl_seconds, max_ttl_seconds,
created_at`. (admin_dsn + all SQL templates masked out.)
- `POST /v1/dynamic/roles/{id}/creds` → **the only password response:**
  `{ lease_id, username, password, expires_at }`.
- `DELETE /v1/dynamic/roles/{id}` → 204 (revokes live leases first,
  server-side).
- List filter: `?config_id=` (required). Requires `dynamic:manage`
  (admin/owner) for list/get/delete.

**Dynamic leases** — `leaseViewJSON` (`{ "leases": [...] }` for list):
`id, role_id, status ("creating"|"active"|"expired"|"revoked"|
"revoke_failed"), db_username, expires_at, max_expires_at, renewed_at?,
created_at`.
- `POST /v1/dynamic/leases/{id}/renew` → updated `leaseViewJSON`.
- `POST /v1/dynamic/leases/{id}/revoke` → `{ revoked: true }`
  (idempotent).
- List filter: `?role_id=` (required). Requires `dynamic:issue`
  (developer+) for list/renew/revoke and for issuing creds.

## The three panels

### Rotation panel

Columns: **Project · Config · Secret key · Type · Status · Next rotation ·
Last rotated · Failures** (`failure_count`, with `<LastError>` when
`last_error` present). Row actions:
- **Rotate now** — `POST …/rotate`; toast the resulting `config_version`;
  also clears a `failed` status. Invalidate the rotation aggregate.
- **Pause / Resume** — `PATCH { status: "paused" | "active" }`.
- **Edit interval** — a small `Modal` with a single seconds `Input`
  (the one editable non-secret field); `PATCH { interval_seconds }`.
- **Delete** — `ConfirmDialog` → `DELETE`.

### Sync panel

Columns: **Project · Config · Provider · Destination · Prune · Status ·
Next sync · Last synced · Failures**. Destination derived from `addr`.
Row actions:
- **Sync now** — `POST …/sync` (forces past change-detection; clears
  `failed`).
- **Pause / Resume** — `PATCH { status }`.
- **Edit interval** — `Modal` → `PATCH { interval_seconds }`.
- **Delete** — `ConfirmDialog` → `DELETE`.

(Prune, credentials, and destination edits are intentionally CLI-only —
they touch credentials or change mirroring semantics.)

### Dynamic panel

A **roles** table: **Project · Config · Role name · Default TTL · Max
TTL**. Row actions:
- **Issue creds** — `POST …/creds`; see §"Issued-password reveal" below.
- **View leases** — opens a right-side `Sheet` for that role.
- **Delete role** — `ConfirmDialog` warning that it revokes the role's
  still-live leases first; `DELETE`.

The **leases `Sheet`** loads `?role_id=` and lists: **Status · DB username
· Expires · Max-expires · Renewed**, each row with **Renew**
(`POST …/renew`) and **Revoke** (`POST …/revoke`, idempotent) actions.
Issuing or acting on a lease invalidates the leases query for that role
(lease metadata is non-secret).

## Issued-password reveal (the one sensitive surface)

`Issue creds` is the only action that returns a plaintext secret, and only
once. It is handled like the codebase's on-demand secret reveal, never
cached:

- The mutation result (`{ username, password, expires_at, lease_id }`) is
  shown in an **ephemeral `Modal`**: username + password with a **Copy**
  button and a prominent "shown once — it will not be shown again"
  warning.
- The password lives **only** in that modal's local React state. It is
  **never** written to the TanStack Query cache, never logged, never put
  in a toast. The modal **clears** the value on close and cannot be
  re-opened for the same issuance.
- This mirrors existing plaintext-ephemeral rules: the transit
  playground's remount-to-clear and the secret editor's on-demand reveal
  (values held in ephemeral component maps, never in cache).
- Issuing does refresh the role's leases list (metadata only).

## Security & RBAC posture

- **Zero secrets rendered** anywhere except the §issued-password modal:
  every list/detail view consumes only the masked view shapes above, which
  contain no DSNs, PATs, tokens, CA certs, HMAC keys, SQL, or passwords.
- **Inline 403 handling** per the house pattern (no central route guard):
  - A whole engine forbidden across all in-scope projects → the panel
    renders an "access required" `EmptyState`.
  - Per-project/per-config 403 during fan-out → silently dropped from the
    merged rows; when `someForbidden`, a quiet footnote reads e.g.
    "Some projects hidden — you don't manage {engine} there."
  - Action-level 403/409 → curated server message via the toast.
- **Dynamic tab is admin/owner-oriented:** listing roles requires
  `dynamic:manage`, so the roles table populates only for admins/owners;
  the issue/renew/revoke actions additionally succeed for `dynamic:issue`
  holders. The client does no role-gating beyond reacting to 403 — the
  server remains the authority.
- **503 while sealed** is handled by the app's global seal handler
  (unseal prompt), unchanged.

## Testing

Vitest + React Testing Library + MSW, using the existing `renderApp`
helper and MSW `server.use(...)` handlers that **mirror the exact wire
shapes above** (`{policies:[…]}`, `{targets:[…]}`, `{roles:[…]}`,
`{leases:[…]}`, issue → `{lease_id,username,password,expires_at}`).
Required coverage:

- **Fan-out merge** across ≥2 projects renders unified rows with the
  Project column populated.
- **Per-project 403 is skipped, not fatal**: one project returns 403,
  another returns rows → only the accessible rows render, `someForbidden`
  footnote shown; a non-403 error (500) does surface an error state.
- **All-forbidden** → "access required" EmptyState per panel.
- **Empty** aggregate → EmptyState.
- Each **row action** fires the correct request (method + path + body) and
  invalidates the right query (assert a refetch happens).
- **Issued-password**: after `Issue creds`, the password is visible in the
  modal; after the modal closes it is gone from the DOM and never appears
  in any subsequent cached query read (assert the leases refetch response
  carries no password field and the value isn't retained).
- Rotate-now / sync-now / pause-resume / renew / revoke happy paths.
- Tab switching via `?tab=` and the Project filter narrowing the fan-out.
- **Dual-theme smoke** (`npm run smoke`) passes and the
  `no-raw-palette` guard test stays green (tokens only, no raw palette
  classes or hex in components).

## Rollout

Single frontend slice, subagent-driven per the usual process. Because it
adds only client code against stable endpoints, it ships behind no flag;
the sidebar entry appears for all authenticated users and each panel
self-gates via 403 as above. Update `docs/web.md` (new console) and the
`fe-improvements.md` tracker on completion.
