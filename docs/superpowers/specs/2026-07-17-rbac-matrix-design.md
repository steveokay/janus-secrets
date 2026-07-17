# Members RBAC matrix — design

_Date: 2026-07-17. Closes gaps.md §2.6 — adds a read-only users × scopes matrix view to the Members page, plus a searchable add-member picker. Frontend-only; no backend, no migration._

## Problem

The Members page (`web/src/members/MembersPage.tsx`) is a **per-scope list**: you pick a scope (instance / project / environment) and see that one scope's role bindings (editable role + remove), plus a Users table at the instance scope. There is no **at-a-glance grid** of who has what **across** scopes — to answer "which projects can Alice touch?" you switch the scope selector project by project. gaps.md §2.6 calls this out: "no RBAC matrix view (users × scopes grid)" and "no user search in the add-member picker."

## Verified starting facts

- **Roles**: `viewer < developer < admin < owner` (`internal/authz/actions.go` `roleRank` 1–4). Web mirror: `MemberRole` + `ROLES` in `MembersPage.tsx`.
- **Scopes**: bindings are scoped to **instance / project / environment** — `MemberScope = { kind: 'instance' } | { kind: 'project', pid } | { kind: 'environment', pid, eid }` (`web/src/lib/endpoints.ts`). `memberScopePath(scope)` maps to `/v1/instance/members`, `/v1/projects/{pid}/members`, `/v1/projects/{pid}/environments/{eid}/members`.
- **Data**: `Member = { user_id, role }`. `listMembers(scope)` returns members for ONE scope; `putMember`/`deleteMember` mutate one binding. There is NO endpoint returning all bindings at once.
- **Users**: `listUsers()` → `UserInfo[]` (`{ id, email, disabled }`); best-effort (403 for callers without user-manage). `MembersPage` already degrades gracefully: emails fall back to id-prefixes, Users table hides.
- **Precedent for cross-scope fan-out**: the ops console (`web/src/operations/useAggregated.ts`) fans out per-project queries, 403-tolerant, cached — the exact pattern this matrix reuses.
- **Existing niceties on the page**: `useTableControls`/`SortHeader`/`TableSearch` (shared table primitives), `Pill` (role/status chips), `Sheet`, `ConfirmDialog`, `RevealOnce`.

## Approach — frontend fan-out, no backend

Reuse the ops-console 403-tolerant fan-out: fetch `listMembers(instance)` + `listMembers({project,pid})` for every project + `listMembers({environment,pid,eid})` for every environment, all cached via TanStack Query, and assemble the grid client-side. **Rejected alternative:** a new `GET /v1/members/all` backend endpoint — cleaner (one authz-filtered query) but adds backend surface; not worth it now. The fan-out is an N-query call (instance + #projects + #environments), modest for a self-hosted single-tenant instance and cached; a `/v1/members/all` endpoint is the noted future optimization if it ever gets slow.

**Explicit bindings only.** The matrix shows what's actually stored at each scope. It does NOT compute effective/inherited access (an instance-owner is not auto-painted across every project cell). Replicating the authz resolution engine on the client risks misrepresenting real access and diverging from the server. A one-line legend states the caveat.

## Section A — `RbacMatrix` (read-only grid)

New `web/src/members/RbacMatrix.tsx` + a data hook (e.g. `useRbacMatrix.ts`).

- **Data hook**: uses `useProjects()` + `useEnvironments()` (or a per-project env fan-out) to enumerate scopes, then `useQueries` to fetch `listMembers` for instance + each project + each environment, `retry: false`, 403-tolerant (a failed scope contributes no bindings rather than erroring). Returns an assembled structure keyed by `user_id`: `{ instanceRole?, byProject: Map<pid, { role?, envRoles: {eid, role}[] }> }`, plus the project list (columns) and the resolved user list (rows).
- **Rows** = users from `listUsers()`; when that's 403/unavailable, fall back to the **union of `user_id`s** appearing in any binding (emails degrade to id-prefixes, same as the existing page).
- **Columns** = **Instance** (first) + one per project (project name header).
- **Cell** = the user's explicit role at that scope as a **role-colored chip** (reuse the role Pill tone mapping); empty cell renders a muted "—". If the user has **environment-level** bindings inside that project, the cell also shows a small **"+N env"** badge (N = count of that user's env bindings in that project).
- **Sticky first column** (user email) + **sticky header row**; the grid lives in an `overflow-x-auto` container so wide project sets scroll inside the grid, never the page body.
- **Cell → editor link**: clicking a cell (or the "+N env" badge) calls an `onPickScope(scope)` callback (wired in Section B) that switches to the List view and sets its scope selector to the clicked scope, so all edits still go through the existing reviewed per-scope editor. The matrix itself is read-only.
- **403 / empty**: if instance member-read is forbidden, show the existing "Member access required" empty state. Unreadable project/env scopes simply contribute no cells (greyed), never an error.
- **Legend**: one line — "Cells show explicit role bindings. Instance roles apply everywhere; project/environment roles are scoped."

## Section B — List | Matrix view toggle

- Add a **List | Matrix** segmented toggle at the top of `MembersPage`, controlled by a `?view=` URL param (default `list`, so current behavior is unchanged and the Matrix view is linkable).
- **List** = the entire existing page (scope selector, editable members table, Users table, all sheets/dialogs) — unchanged.
- **Matrix** = renders `<RbacMatrix onPickScope={…} />`; `onPickScope` switches `view` back to `list` and sets the scope selector to the clicked cell's scope.
- The Users table + create/disable stay in List (instance scope), unchanged.

## Section C — Searchable add-member picker (§2.6 secondary)

- Replace the `AddMemberSheet` user `<select>` (`MembersPage.tsx:67-76`) with a small **searchable combobox**: a text input that filters the candidate set (`users.filter(!disabled && !alreadyMember)`) by email substring (case-insensitive), plus a keyboard-navigable option list; selecting sets `uid`. Everything else (role select, `putMember`, confirm, invalidation) unchanged. Reuse existing input/list token styling; keep it self-contained (a small local component, not a new shared primitive unless one already exists — check `web/src/ui` first).

## Data flow

Matrix load: enumerate scopes (projects, envs) → `useQueries` fan-out `listMembers` per scope (cached, 403-tolerant) → assemble per-user structure → render grid. Pick a cell → set `view=list` + scope → existing List editor handles the change → its `['members', scopePath]` invalidation refreshes; the matrix re-reads the same cached query keys, so an edit is reflected without bespoke wiring.

## Error handling

- Any single scope's `listMembers` failing (403/other) → that scope contributes nothing; the grid still renders.
- `listUsers` 403 → rows fall back to the binding user-id union; emails show as id-prefixes.
- Instance member-read forbidden → "Member access required" empty state (existing).

## Testing

- **Matrix assembly** (pure): from mocked per-scope `listMembers` data — correct role chip per (user, instance/project) cell; empty cell when no binding; "+N env" badge when the user has env bindings in that project; row-union fallback when users list is absent.
- **RbacMatrix render**: sticky header + first column present; grid in an `overflow-x-auto` container; cell click calls `onPickScope` with the right scope; 403 scope contributes no error.
- **View toggle**: List↔Matrix switches via the segmented control and `?view=` param; default is List; picking a cell returns to List at that scope.
- **Searchable picker**: typing filters candidates by email; selecting sets the user; add still calls `putMember`.
- Token classes only; dual-theme smoke; no-raw-palette guard. MSW mocks mirror the per-scope `listMembers` wire shape.

## Non-goals

- Editable matrix cells (grant/change/revoke directly in the grid) — deliberate follow-up; edits stay in the per-scope List editor.
- Computing effective/inherited access on the client (explicit bindings only).
- A `/v1/members/all` backend endpoint (fan-out for now).
- Environment columns in the top-level grid (surfaced via the "+N env" badge + drill-in, not as their own columns).
- Any change to roles, scopes, or the binding data model.

## Rollout

Frontend-only, no migration. After merge: rebuild dev containers (`docker compose up -d --build`) + `dev-unseal.sh` (no schema change; rebuild just embeds the new assets). Update gaps.md §2.6.
