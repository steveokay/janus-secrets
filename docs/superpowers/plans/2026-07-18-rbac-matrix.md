# Members RBAC Matrix Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a read-only users × scopes RBAC matrix to the Members page (columns = Instance + each project, cells = explicit role + "+N env" badge), a List|Matrix view toggle, and a searchable add-member picker — all frontend-only (gaps.md §2.6).

**Architecture:** A pure `assembleMatrix` builds the grid model from per-scope member lists; a `useRbacMatrix` hook fans out `listMembers` across instance + every project + every environment (403-tolerant, cached, reusing the same query keys as the existing page so edits reflect automatically); `RbacMatrix` renders a sticky-header/sticky-first-column scrollable grid of role chips; `MembersPage` gains a `?view=` List|Matrix toggle and a searchable `UserPicker` in its add-member sheet.

**Tech Stack:** React + TypeScript + Tailwind (token classes only) + TanStack Query (`useQueries`) + Vitest/MSW.

---

## Reference facts (verified against the code)

- Types (`web/src/lib/endpoints.ts`): `MemberRole = 'viewer'|'developer'|'admin'|'owner'`; `Member = { user_id, role }`; `UserInfo = { id, email, disabled }`; `MemberScope = {kind:'instance'} | {kind:'project',pid} | {kind:'environment',pid,eid}`; `memberScopePath(scope)`.
- Endpoints: `endpoints.listMembers(scope)`, `listUsers()`, `listProjects()`, `listEnvironments(pid)`, `putMember(scope,uid,role)`, `deleteMember(scope,uid)`.
- Query keys to REUSE (shared cache with the existing page): projects `['projects']`, envs `['envs', pid]`, members `['members', memberScopePath(scope)]`. Reusing the member key means a List-view edit's `invalidateQueries(['members', scopePath])` auto-refreshes the matrix.
- Hooks: `useProjects`, `useEnvironments(pid)` in `web/src/secrets/nav.ts`. Fan-out precedent: `useFanOut`/`useQueries` in `web/src/operations/useAggregated.ts` (403 → drop scope, non-403 → isError).
- `ApiError` (`web/src/lib/api.ts`) has `.status`; `apiErrorTitle(e)`.
- `Pill` (`web/src/ui/Pill.tsx`): `Tone = 'success'|'warning'|'danger'|'info'|'brand'|'muted'`; classes are token-based.
- Existing page `web/src/members/MembersPage.tsx`: `ROLES`, `displayName(uid, byId)`, `AddMemberSheet` (the `<select>` at ~line 67-76 to replace), the scope selector state (`scopeKind`/`pid`/`eid`), `useTableControls`/`SortHeader`/`TableSearch`, `Sheet`, `ConfirmDialog`, `RevealOnce`.
- **Role → tone** (this plan's mapping): `viewer→muted, developer→info, admin→brand, owner→warning`.
- Web env: `npm test` is WATCH → `npm test -- --run <path>`; tsconfig ES2020 (no `.at()`); token classes only; guards `web/src/test/no-raw-palette.test.ts` + `npm run smoke`.

---

## Task 1: `matrix.ts` — pure assembly + role tone

**Files:**
- Create: `web/src/members/matrix.ts` + `web/src/members/matrix.test.ts`

- [ ] **Step 1: Write the failing test** (`web/src/members/matrix.test.ts`)

```ts
import { describe, it, expect } from 'vitest'
import { assembleMatrix, roleTone } from './matrix'
import type { MemberScope } from '../lib/endpoints'

const inst = (uid: string, role: string) => ({ scope: { kind: 'instance' } as MemberScope, members: [{ user_id: uid, role }] })
const proj = (pid: string, uid: string, role: string) => ({ scope: { kind: 'project', pid } as MemberScope, members: [{ user_id: uid, role }] })
const env = (pid: string, eid: string, uid: string, role: string) => ({ scope: { kind: 'environment', pid, eid } as MemberScope, members: [{ user_id: uid, role }] })

describe('assembleMatrix', () => {
  const projects = [{ id: 'p1', name: 'App' }, { id: 'p2', name: 'Web' }]
  const users = [{ id: 'u1', email: 'alice@x.io', disabled: false }, { id: 'u2', email: 'bob@x.io', disabled: false }]

  it('places explicit instance + project roles in cells', () => {
    const m = assembleMatrix([inst('u1', 'owner'), proj('p1', 'u1', 'admin')], projects, users)
    expect(m.instanceRole.get('u1')).toBe('owner')
    expect(m.projectCells.get('u1')!.get('p1')).toEqual({ role: 'admin', envCount: 0 })
    // u1 has no p2 binding
    expect(m.projectCells.get('u1')!.get('p2')).toEqual({ role: undefined, envCount: 0 })
  })

  it('counts environment bindings into the project cell badge', () => {
    const m = assembleMatrix([env('p1', 'e1', 'u2', 'developer'), env('p1', 'e2', 'u2', 'viewer')], projects, users)
    expect(m.projectCells.get('u2')!.get('p1')).toEqual({ role: undefined, envCount: 2 })
  })

  it('rows come from users, sorted by email; email resolved', () => {
    const m = assembleMatrix([], projects, users)
    expect(m.rows.map((r) => r.email)).toEqual(['alice@x.io', 'bob@x.io'])
  })

  it('falls back to the union of binding user-ids when users list is absent', () => {
    const m = assembleMatrix([inst('zzz-user-id', 'viewer')], projects, undefined)
    expect(m.rows).toHaveLength(1)
    expect(m.rows[0].userId).toBe('zzz-user-id')
    expect(m.rows[0].email).toBe('zzz-user') // uid.slice(0,8)
  })

  it('roleTone maps each role to a token Pill tone', () => {
    expect(roleTone).toEqual({ viewer: 'muted', developer: 'info', admin: 'brand', owner: 'warning' })
  })
})
```

- [ ] **Step 2: Run to verify it fails**

Run: `cd web && npm test -- --run src/members/matrix.test.ts`
Expected: FAIL — module not found.

- [ ] **Step 3: Implement `matrix.ts`**

```ts
import type { Tone } from '../ui/Pill'
import type { Member, MemberRole, MemberScope, UserInfo } from '../lib/endpoints'

export const roleTone: Record<MemberRole, Tone> = {
  viewer: 'muted',
  developer: 'info',
  admin: 'brand',
  owner: 'warning',
}

export interface ScopeMembers {
  scope: MemberScope
  members: Member[]
}

export interface Cell {
  role?: MemberRole
  envCount: number
}

export interface MatrixModel {
  rows: { userId: string; email: string }[]
  columns: { id: string; name: string }[]
  instanceRole: Map<string, MemberRole>
  /** userId -> pid -> Cell (every user has an entry for every column). */
  projectCells: Map<string, Map<string, Cell>>
}

/**
 * Assemble the read-only RBAC matrix model from per-scope member lists.
 * EXPLICIT bindings only — no effective/inherited resolution. Env-level
 * bindings contribute to the owning project cell's envCount badge.
 */
export function assembleMatrix(
  scopeMembers: ScopeMembers[],
  projects: { id: string; name: string }[],
  users: UserInfo[] | undefined,
): MatrixModel {
  const instanceRole = new Map<string, MemberRole>()
  const projectCells = new Map<string, Map<string, Cell>>()
  const seenUsers = new Set<string>()

  const cellFor = (uid: string, pid: string): Cell => {
    let byPid = projectCells.get(uid)
    if (!byPid) {
      byPid = new Map()
      projectCells.set(uid, byPid)
      for (const p of projects) byPid.set(p.id, { role: undefined, envCount: 0 })
    }
    return byPid.get(pid)!
  }

  for (const { scope, members } of scopeMembers) {
    for (const m of members) {
      seenUsers.add(m.user_id)
      if (scope.kind === 'instance') {
        instanceRole.set(m.user_id, m.role)
      } else if (scope.kind === 'project') {
        cellFor(m.user_id, scope.pid).role = m.role
      } else {
        cellFor(m.user_id, scope.pid).envCount += 1
      }
    }
  }

  // Rows: prefer the users list (non-disabled), else the union of binding ids.
  const byId = new Map((users ?? []).map((u) => [u.id, u]))
  const rowIds = users
    ? users.filter((u) => !u.disabled).map((u) => u.id)
    : [...seenUsers]
  const rows = rowIds
    .map((userId) => ({ userId, email: byId.get(userId)?.email ?? userId.slice(0, 8) }))
    .sort((a, b) => a.email.localeCompare(b.email))

  // Guarantee every row has a full column map (so the component can index safely).
  for (const r of rows) if (!projectCells.has(r.userId)) cellFor(r.userId, projects[0]?.id ?? '')

  return { rows, columns: projects, instanceRole, projectCells }
}
```

Note: `cellFor` seeds a full per-project map on first touch; the final loop guarantees rows with no bindings still have a map. (The `projects[0]?.id ?? ''` touch is only to create the map; if there are no projects it's a harmless empty entry.)

- [ ] **Step 4: Run to verify it passes**

Run: `cd web && npm test -- --run src/members/matrix.test.ts`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add web/src/members/matrix.ts web/src/members/matrix.test.ts
git commit -m "feat(web): pure RBAC matrix assembly + role tone"
```

---

## Task 2: `useRbacMatrix` — fan-out data hook

**Files:**
- Create: `web/src/members/useRbacMatrix.ts` + `web/src/members/useRbacMatrix.test.tsx`

- [ ] **Step 1: Write the failing test** (`useRbacMatrix.test.tsx`)

Render the hook inside a QueryClientProvider with MSW (or `vi.spyOn(endpoints, ...)`) mocking `listProjects`, `listEnvironments`, `listMembers`, `listUsers`. Assert the assembled `model` has the expected instance/project cells and env badge counts, and that a 403 on one project's members doesn't error the whole hook. Mirror how an existing hook test (e.g. an operations `useAggregated` test, or a members test) sets up the QueryClient + mocks. Minimal shape:

```ts
// mock: listProjects -> [{id:'p1',name:'App'}]; listEnvironments('p1') -> [{id:'e1',...}]
// listMembers(instance) -> [{user_id:'u1',role:'owner'}]
// listMembers(project p1) -> [{user_id:'u1',role:'admin'}]
// listMembers(env e1) -> [{user_id:'u1',role:'developer'}]
// listUsers -> [{id:'u1',email:'a@x.io',disabled:false}]
// After settle: model.instanceRole.get('u1')==='owner';
//   model.projectCells.get('u1').get('p1') == { role:'admin', envCount:1 }
```

- [ ] **Step 2: Run to verify it fails**

Run: `cd web && npm test -- --run src/members/useRbacMatrix.test.tsx`
Expected: FAIL — module not found.

- [ ] **Step 3: Implement `useRbacMatrix.ts`**

```ts
import { useQueries, useQuery } from '@tanstack/react-query'
import { endpoints, memberScopePath, type Member, type MemberScope, type UserInfo } from '../lib/endpoints'
import { ApiError } from '../lib/api'
import { assembleMatrix, type MatrixModel, type ScopeMembers } from './matrix'

interface Result {
  model: MatrixModel
  isLoading: boolean
  forbidden: boolean   // instance member-read denied
}

export function useRbacMatrix(): Result {
  const projectsQ = useQuery({ queryKey: ['projects'], queryFn: endpoints.listProjects })
  const projects = (projectsQ.data ?? []).map((p) => ({ id: p.id, name: p.name }))

  // Env lists per project (shared cache key with useEnvironments).
  const envListQs = useQueries({
    queries: projects.map((p) => ({ queryKey: ['envs', p.id], queryFn: () => endpoints.listEnvironments(p.id) })),
  })
  const envScopes: MemberScope[] = []
  projects.forEach((p, i) => {
    for (const e of envListQs[i]?.data ?? []) envScopes.push({ kind: 'environment', pid: p.id, eid: e.id })
  })

  const scopes: MemberScope[] = [
    { kind: 'instance' },
    ...projects.map((p) => ({ kind: 'project', pid: p.id }) as MemberScope),
    ...envScopes,
  ]

  const memberQs = useQueries({
    queries: scopes.map((s) => ({
      queryKey: ['members', memberScopePath(s)],
      queryFn: () => endpoints.listMembers(s),
      retry: false,
    })),
  })

  const usersQ = useQuery({ queryKey: ['users'], queryFn: endpoints.listUsers, retry: false })

  // Instance scope is index 0; a 403 there means the caller can't read membership.
  const instanceErr = memberQs[0]?.error
  const forbidden = instanceErr instanceof ApiError && instanceErr.status === 403

  const scopeMembers: ScopeMembers[] = scopes
    .map((scope, i) => ({ scope, members: (memberQs[i]?.data ?? []) as Member[] }))
    .filter((sm) => sm.members.length > 0)

  const model = assembleMatrix(scopeMembers, projects, usersQ.data as UserInfo[] | undefined)

  const isLoading =
    projectsQ.isLoading ||
    envListQs.some((q) => q.isLoading) ||
    memberQs.some((q) => q.isLoading)

  return { model, isLoading, forbidden }
}
```

- [ ] **Step 4: Run to verify it passes**

Run: `cd web && npm test -- --run src/members/useRbacMatrix.test.tsx`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add web/src/members/useRbacMatrix.ts web/src/members/useRbacMatrix.test.tsx
git commit -m "feat(web): useRbacMatrix fan-out hook (403-tolerant, shared cache)"
```

---

## Task 3: `RbacMatrix` — the read-only grid

**Files:**
- Create: `web/src/members/RbacMatrix.tsx` + `web/src/members/RbacMatrix.test.tsx`

- [ ] **Step 1: Write the failing test**

Render `<RbacMatrix onPickScope={spy} />` inside a QueryClientProvider with the same mocks as Task 2. Assert: the header shows `Instance` + each project name; a cell shows the role chip (e.g. text `admin`); a cell with env bindings shows a `+1 env` badge; clicking a project cell calls `onPickScope` with `{ kind:'project', pid:'p1' }`; the grid is wrapped in an element with `overflow-x-auto`. Also a test that when `forbidden`, the "Member access required" empty state shows.

- [ ] **Step 2: Run to verify it fails**

Run: `cd web && npm test -- --run src/members/RbacMatrix.test.tsx`
Expected: FAIL — module not found.

- [ ] **Step 3: Implement `RbacMatrix.tsx`**

```tsx
import { useRbacMatrix } from './useRbacMatrix'
import { roleTone } from './matrix'
import type { MemberScope } from '../lib/endpoints'
import { Pill } from '../ui/Pill'
import { EmptyState } from '../ui/EmptyState'

export function RbacMatrix({ onPickScope }: { onPickScope: (scope: MemberScope) => void }) {
  const { model, isLoading, forbidden } = useRbacMatrix()

  if (forbidden) {
    return <EmptyState title="Member access required" hint="Ask an instance admin or owner for access." />
  }
  if (isLoading) {
    return (
      <div className="flex flex-col gap-1.5" aria-hidden="true">
        {[0, 1, 2].map((i) => <div key={i} className="h-8 animate-pulse rounded bg-line-soft" />)}
      </div>
    )
  }
  if (model.rows.length === 0) {
    return <EmptyState title="No role bindings yet" hint="Grant a user a role in a scope to see it here." />
  }

  return (
    <div>
      <p className="mb-2 text-[12px] text-ink-faint">
        Cells show explicit role bindings. Instance roles apply everywhere; project and environment roles are scoped.
      </p>
      <div className="overflow-x-auto rounded-card border border-line bg-surface-2 shadow-elev-1">
        <table className="w-full text-sm">
          <thead>
            <tr className="text-left text-[10.5px] uppercase tracking-[.1em] text-ink-faint">
              <th className="sticky left-0 z-20 bg-surface-1 px-3 py-2">User</th>
              <th className="sticky top-0 z-10 bg-surface-1 px-3 py-2">Instance</th>
              {model.columns.map((c) => (
                <th key={c.id} className="sticky top-0 z-10 bg-surface-1 px-3 py-2 whitespace-nowrap">{c.name}</th>
              ))}
            </tr>
          </thead>
          <tbody>
            {model.rows.map((r) => (
              <tr key={r.userId} className="border-t border-line-soft hover:bg-row-hover transition-nocturne">
                <td className="sticky left-0 z-10 bg-surface-2 px-3 py-1.5 font-medium text-ink whitespace-nowrap">{r.email}</td>
                <td className="px-3 py-1.5">
                  {model.instanceRole.has(r.userId) ? (
                    <button type="button" onClick={() => onPickScope({ kind: 'instance' })} className="cursor-pointer">
                      <Pill tone={roleTone[model.instanceRole.get(r.userId)!]}>{model.instanceRole.get(r.userId)}</Pill>
                    </button>
                  ) : <span className="text-ink-faint">—</span>}
                </td>
                {model.columns.map((c) => {
                  const cell = model.projectCells.get(r.userId)?.get(c.id) ?? { role: undefined, envCount: 0 }
                  const empty = !cell.role && cell.envCount === 0
                  return (
                    <td key={c.id} className="px-3 py-1.5">
                      {empty ? <span className="text-ink-faint">—</span> : (
                        <button
                          type="button"
                          onClick={() => onPickScope({ kind: 'project', pid: c.id })}
                          className="inline-flex items-center gap-1.5 cursor-pointer"
                        >
                          {cell.role && <Pill tone={roleTone[cell.role]}>{cell.role}</Pill>}
                          {cell.envCount > 0 && <Pill tone="muted">+{cell.envCount} env</Pill>}
                        </button>
                      )}
                    </td>
                  )
                })}
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </div>
  )
}
```

Token classes only (no raw palette / hex / `dark:`). Confirm `bg-surface-1`/`bg-surface-2`/`bg-row-hover`/`text-ink-faint`/`bg-line-soft` are existing tokens (they're used across MembersPage — reuse the same).

- [ ] **Step 4: Run to verify it passes**

Run: `cd web && npm test -- --run src/members/RbacMatrix.test.tsx`; then `cd web && npm run typecheck` and `npm test -- --run src/test/no-raw-palette.test.ts`.
Expected: all PASS.

- [ ] **Step 5: Commit**

```bash
git add web/src/members/RbacMatrix.tsx web/src/members/RbacMatrix.test.tsx
git commit -m "feat(web): read-only RBAC matrix grid (sticky, role chips, +N env)"
```

---

## Task 4: List | Matrix view toggle on MembersPage

**Files:**
- Modify: `web/src/members/MembersPage.tsx`
- Test: `web/src/members/MembersPage.test.tsx` (append)

- [ ] **Step 1: Write the failing test**

Append tests: MembersPage renders a `List` | `Matrix` toggle (two buttons / a segmented control). Default view is List (the scope selector is visible). Clicking `Matrix` shows the matrix (header text `Instance`, project names) and hides the scope selector. Clicking a matrix cell returns to List with that scope selected (e.g. after clicking a project cell, the scope `<select>` reads `project` and the project `<select>` reads that project). Reuse the file's existing MSW/mock setup; add mocks for the fan-out (projects/envs/members) if not present. Assert the toggle reflects `?view=` (render at `?view=matrix` shows the matrix directly).

- [ ] **Step 2: Run to verify it fails**

Run: `cd web && npm test -- --run src/members/MembersPage.test.tsx`
Expected: FAIL — no toggle / matrix.

- [ ] **Step 3: Implement**

In `MembersPage.tsx`:
- Import `RbacMatrix` from `./RbacMatrix`, `useSearchParams` from `react-router-dom`.
- Read the view from the URL: `const [params, setParams] = useSearchParams(); const view = params.get('view') === 'matrix' ? 'matrix' : 'list'`. Add a setter `setView(v)` that updates the param (`params.set('view', v)`; `setParams(params, { replace: true })`).
- Render a segmented toggle near the top header (two `Button`s or `aria-pressed` toggle: `List` / `Matrix`), token-styled, matching existing controls.
- When `view === 'matrix'`, render `<RbacMatrix onPickScope={(scope) => { setScopeFromMatrix(scope); setView('list') }} />` INSTEAD of the scope selector + members table (keep the Users section logic as-is, but it only shows in List). `setScopeFromMatrix(scope)` sets `scopeKind`/`pid`/`eid` from the scope object.
- When `view === 'list'`, render the existing page body unchanged.
- Keep all sheets/dialogs mounted regardless of view (they're gated by their own state).

Add the helper:
```tsx
function setScopeFromMatrix(
  scope: MemberScope,
  setScopeKind: (k: ScopeKind) => void,
  setPid: (v: string) => void,
  setEid: (v: string) => void,
) {
  setScopeKind(scope.kind)
  setPid(scope.kind === 'instance' ? '' : scope.pid)
  setEid(scope.kind === 'environment' ? scope.eid : '')
}
```
(Or inline it — the point is the matrix's `onPickScope` sets the three scope state vars and flips the view to list.)

- [ ] **Step 4: Run to verify it passes**

Run: `cd web && npm test -- --run src/members/MembersPage.test.tsx`; `cd web && npm run typecheck`.
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add web/src/members/MembersPage.tsx web/src/members/MembersPage.test.tsx
git commit -m "feat(web): List | Matrix view toggle on the members page"
```

---

## Task 5: Searchable add-member picker (§2.6 secondary)

**Files:**
- Create: `web/src/members/UserPicker.tsx` + `web/src/members/UserPicker.test.tsx`
- Modify: `web/src/members/MembersPage.tsx` (`AddMemberSheet`)

- [ ] **Step 1: Write the failing test** (`UserPicker.test.tsx`)

```
// <UserPicker candidates={[{id:'u1',email:'alice@x.io'},{id:'u2',email:'bob@x.io'}]} value="" onChange={spy} />
// typing "bob" in the search input filters the list to bob@x.io only;
// clicking bob calls onChange('u2'); typing a non-match shows a "no matches" state.
```

- [ ] **Step 2: Run to verify it fails**

Run: `cd web && npm test -- --run src/members/UserPicker.test.tsx`
Expected: FAIL — module not found.

- [ ] **Step 3: Implement `UserPicker.tsx`**

A small self-contained combobox: a search `<input>` (aria-label `search users`) + a scrollable list of matching candidates as `<button>` options (each `aria-label` or text = email); filters by case-insensitive email substring; clicking an option calls `onChange(id)` and shows the selected email in the input or a selected state; a "No users match" row when empty. Props: `{ candidates: { id: string; email: string }[]; value: string; onChange: (id: string) => void }`. Token classes only; keyboard-accessible (options are real buttons). Do NOT introduce a new dependency; keep it a plain controlled component. (If `web/src/ui` already has a combobox primitive, reuse it and skip the new file — check first; the plan assumes none exists.)

In `MembersPage.tsx` `AddMemberSheet`: replace the user `<select>` (lines ~67-76) with `<UserPicker candidates={candidates.map((u) => ({ id: u.id, email: u.email }))} value={uid} onChange={setUid} />`. Keep the role select, submit, and mutation unchanged.

- [ ] **Step 4: Run to verify it passes**

Run: `cd web && npm test -- --run src/members/UserPicker.test.tsx src/members/MembersPage.test.tsx`; `cd web && npm run typecheck`; `npm test -- --run src/test/no-raw-palette.test.ts`.
Expected: all PASS.

- [ ] **Step 5: Commit**

```bash
git add web/src/members/UserPicker.tsx web/src/members/UserPicker.test.tsx web/src/members/MembersPage.tsx
git commit -m "feat(web): searchable add-member user picker"
```

---

## Task 6: Full web gate + smoke + gaps.md

**Files:**
- Modify: `gaps.md` (mark §2.6 matrix + picker done)

- [ ] **Step 1: Full web suite + typecheck + smoke + guards**

Run (from `web/`): `npm test -- --run && npm run typecheck && npm run smoke && npm test -- --run src/test/no-raw-palette.test.ts`
Expected: all PASS; smoke light + dark clean. (Also `npm test -- --run src/test/no-legacy-alias.test.ts` if present.)

- [ ] **Step 2: Update gaps.md §2.6**

Edit the "### 2.6 Members" section: mark the RBAC matrix (users × scopes grid — read-only, instance + project columns with "+N env" cells, explicit bindings, fan-out) and the add-member picker search as DONE (dated 2026-07-18), matching the file's ~~strikethrough~~/**[DONE …]** style. Note remaining §2.6 item: no last-login column (backend gap).

- [ ] **Step 3: Commit**

```bash
git add gaps.md
git commit -m "docs(gaps): mark §2.6 RBAC matrix + picker search done"
```

---

## Self-review checklist (author)

- **Spec coverage:** Section A (read-only grid) → Tasks 1–3; Section B (view toggle) → Task 4; Section C (searchable picker) → Task 5; testing/gate → Task 6. All covered.
- **Type consistency:** `assembleMatrix(scopeMembers, projects, users) → MatrixModel {rows, columns, instanceRole, projectCells}`, `Cell {role?, envCount}`, `roleTone` (Task 1) consumed identically by `useRbacMatrix` (Task 2) and `RbacMatrix` (Task 3). `onPickScope(scope: MemberScope)` (Task 3) matches the toggle wiring (Task 4). `UserPicker {candidates, value, onChange}` (Task 5).
- **Shared cache:** matrix reuses `['projects']`/`['envs',pid]`/`['members',memberScopePath(scope)]` so a List-view edit's invalidation refreshes the matrix — no bespoke sync.
- **Frontend-only, no migration.** 403-tolerant fan-out; explicit bindings only (no client-side authz resolution). Token classes only.
- **Open verification points flagged inline** (existing hook-test harness shape, whether a combobox primitive already exists, exact surface/token class names) — implementer confirms against the code.
