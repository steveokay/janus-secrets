# B4 — Tokens & Members Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the tokens and members placeholder pages with real management UIs — mint/revoke scoped service tokens (show-once raw value), manage users (show-once initial password, disable with guardrails), and grant/revoke member roles at instance/project/environment scope.

**Architecture:** Pure FE slice — the backend surface exists and was recon-verified (see the spec's API contract; every msw mock mirrors it). One new kit primitive (`RevealOnce` show-once modal) shared by token mint and user create. Two pages composed from the existing kit (Sheet, ConfirmDialog, Toast, Pill, EmptyState) and nav query hooks.

**Tech Stack:** existing web stack; zero new dependencies.

**Authority:** spec `docs/superpowers/specs/2026-07-06-b4-tokens-members-design.md` — its API-contract and Units sections ARE the reference; where this plan abbreviates, the spec governs. Palette gate applies. Server guardrail strings (ceiling/last-owner/self) are surfaced verbatim in danger toasts — they are static server strings, safe to display.

**Commands from `web/`. Commit trailer on every commit (blank line before): `Co-Authored-By: Claude Fable 5 <noreply@anthropic.com>`. Never push.**

---

### Task 0: Branch

- [ ] `git checkout -b milestone-17-b4-tokens-members` from up-to-date main.

---

### Task 1: Endpoints + types

**Files:** Modify `web/src/lib/endpoints.ts`; append to `web/src/lib/endpoints.test.ts`.

- [ ] **Step 1 (TDD):** append tests:

```ts
test('memberScopePath covers all three scopes', () => {
  expect(memberScopePath({ kind: 'instance' })).toBe('/v1/instance/members')
  expect(memberScopePath({ kind: 'project', pid: 'p1' })).toBe('/v1/projects/p1/members')
  expect(memberScopePath({ kind: 'environment', pid: 'p1', eid: 'e1' })).toBe('/v1/projects/p1/environments/e1/members')
})

test('mintToken posts the exact request body', async () => {
  let body: unknown
  server.use(http.post('/v1/tokens', async ({ request }) => {
    body = await request.json()
    return HttpResponse.json({ token: 'janus_svc_abc', id: 't1', name: 'ci',
      scope: { kind: 'config', id: 'c1' }, access: 'read', expires_at: null })
  }))
  const r = await endpoints.mintToken({ name: 'ci', scope: { kind: 'config', id: 'c1' }, access: 'read', ttl_seconds: 3600 })
  expect(r.token).toBe('janus_svc_abc')
  expect(body).toEqual({ name: 'ci', scope: { kind: 'config', id: 'c1' }, access: 'read', ttl_seconds: 3600 })
})

test('listTokens unwraps and tolerates omitted optionals', async () => {
  server.use(http.get('/v1/tokens', () => HttpResponse.json({ tokens: [
    { id: 't1', name: 'ci', scope_kind: 'config', scope_id: 'c1', access: 'read',
      created_by: 'u1', created_at: '2026-07-06T10:00:00Z' },
  ] })))
  const list = await endpoints.listTokens()
  expect(list[0].expires_at).toBeUndefined()
  expect(list[0].revoked_at).toBeUndefined()
})

test('putMember sends role to the scoped path', async () => {
  let body: unknown, hit = false
  server.use(http.put('/v1/projects/p1/members/u2', async ({ request }) => {
    hit = true; body = await request.json()
    return new HttpResponse(null, { status: 204 })
  }))
  await endpoints.putMember({ kind: 'project', pid: 'p1' }, 'u2', 'developer')
  expect(hit).toBe(true)
  expect(body).toEqual({ role: 'developer' })
})
```

Import `memberScopePath` from './endpoints'. FAIL first.

- [ ] **Step 2:** implement in `endpoints.ts` — types `TokenMeta` (`scope_kind: 'config'|'environment'|'transit'`, `expires_at?`/`revoked_at?` optional strings), `MintTokenRequest`, `MintTokenResult` (`expires_at: string | null`), `UserInfo {id,email,disabled}`, `MemberRole = 'viewer'|'developer'|'admin'|'owner'`, `Member {user_id, role: MemberRole}`, `MemberScope` union + exported `memberScopePath`; endpoints exactly:

```ts
  // tokens & users & members (B4). Raw token / one-time password appear ONLY in
  // mint/create responses — never cached, logged, or shown twice.
  mintToken: (req: MintTokenRequest) => api.post<MintTokenResult>('/v1/tokens', req),
  listTokens: () => api.get<{ tokens: TokenMeta[] }>('/v1/tokens').then((r) => r.tokens),
  revokeToken: (id: string) => api.del<void>(`/v1/tokens/${id}`),
  createUser: (email: string) =>
    api.post<{ id: string; email: string; password: string }>('/v1/users', { email }),
  listUsers: () => api.get<{ users: UserInfo[] }>('/v1/users').then((r) => r.users),
  disableUser: (id: string) => api.post<void>(`/v1/users/${id}/disable`),
  listMembers: (s: MemberScope) => api.get<{ members: Member[] }>(memberScopePath(s)).then((r) => r.members),
  putMember: (s: MemberScope, uid: string, role: MemberRole) =>
    api.put<void>(`${memberScopePath(s)}/${uid}`, { role }),
  deleteMember: (s: MemberScope, uid: string) => api.del<void>(`${memberScopePath(s)}/${uid}`),
```

- [ ] **Step 3:** verify (tests pass; full suite; typecheck) → commit `feat(web): token/user/member endpoints + wire types`.

---

### Task 2: RevealOnce primitive

**Files:** Create `web/src/ui/RevealOnce.tsx`; test `web/src/ui/RevealOnce.test.tsx`.

- [ ] **Step 1 (TDD):**

```tsx
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { ToastProvider } from './Toast'
import { RevealOnce } from './RevealOnce'

test('shows secret, copies with toast, closes explicitly', async () => {
  const writeText = vi.fn().mockResolvedValue(undefined)
  Object.assign(navigator, { clipboard: { writeText } })
  const onClose = vi.fn()
  render(
    <ToastProvider>
      <RevealOnce open onClose={onClose} title="Service token" secret="janus_svc_abc" hint="Shown once." />
    </ToastProvider>,
  )
  expect(await screen.findByText('janus_svc_abc')).toBeInTheDocument()
  expect(screen.getByText('Shown once.')).toBeInTheDocument()
  await userEvent.click(screen.getByRole('button', { name: 'Copy' }))
  expect(writeText).toHaveBeenCalledWith('janus_svc_abc')
  expect(await screen.findByText(/won't be shown again/)).toBeInTheDocument()
  await userEvent.click(screen.getByRole('button', { name: /stored it/i }))
  expect(onClose).toHaveBeenCalled()
})
```

FAIL first.

- [ ] **Step 2:** implement per spec Unit 1 — Radix Dialog centered (`aria-describedby={undefined}`), title, hint (`text-muted`), secret block `select-all break-all rounded border border-warning/40 bg-warning-soft px-3 py-2 font-mono text-[12.5px]`, Copy button (clipboard + toast `"Copied — store it now, it won't be shown again"`), primary "I've stored it" → `onClose`; `onOpenChange(false)` also routes to `onClose`. Full code:

```tsx
import * as D from '@radix-ui/react-dialog'
import { useToast } from './Toast'

// One-time secret display (minted tokens, initial passwords). The secret lives
// only in the caller's state; never cache, log, or render it anywhere else.
export function RevealOnce({ open, onClose, title, secret, hint }: {
  open: boolean
  onClose: () => void
  title: string
  secret: string
  hint: string
}) {
  const toast = useToast()
  return (
    <D.Root open={open} onOpenChange={(o) => { if (!o) onClose() }}>
      <D.Portal>
        <D.Overlay className="fixed inset-0 z-50 bg-ink/30" />
        <D.Content aria-describedby={undefined} className="fixed left-1/2 top-1/2 z-50 w-[420px] max-w-[calc(100vw-2rem)] -translate-x-1/2 -translate-y-1/2 rounded-card border border-line bg-card p-5 shadow-pop">
          <D.Title className="mb-1 text-[15px] font-semibold tracking-tight">{title}</D.Title>
          <p className="mb-3 text-[12.5px] text-muted">{hint}</p>
          <div className="mb-3 select-all break-all rounded border border-warning/40 bg-warning-soft px-3 py-2 font-mono text-[12.5px]">
            {secret}
          </div>
          <div className="flex justify-end gap-2">
            <button
              type="button"
              onClick={() => {
                void navigator.clipboard.writeText(secret)
                toast({ title: "Copied — store it now, it won't be shown again" })
              }}
              className="rounded border border-line bg-card px-3 py-1.5 text-[13px] font-semibold"
            >
              Copy
            </button>
            <button type="button" onClick={onClose} className="rounded bg-brand px-3 py-1.5 text-[13px] font-semibold text-white">
              I've stored it
            </button>
          </div>
        </D.Content>
      </D.Portal>
    </D.Root>
  )
}
```

- [ ] **Step 3:** verify → commit `feat(web): RevealOnce show-once secret modal`.

---

### Task 3: TokensPage

**Files:** Create `web/src/tokens/TokensPage.tsx`; test `web/src/tokens/TokensPage.test.tsx`.

- [ ] **Step 1 (TDD):** tests (msw, contract shapes; wrap renders in `<ToastProvider>`; `renderApp(..., { route: '/tokens', withAuth: false })`); enumerate at least:
1. list renders rows: name, scope pill by kind (config→brand, environment→info, transit→muted), access, "never" for missing expires_at, danger "revoked" pill when revoked_at present
2. mint flow: open sheet → kind select flips access options (`read/readwrite` for config; switch to transit → `use/manage`) → cascading pickers for config kind (mock projects/envs/configs endpoints) → submit asserts POST body → RevealOnce shows the raw token → tokens query invalidated (assert refetch hit)
3. revoke: row button → ConfirmDialog → confirm asserts DELETE `/v1/tokens/t1` → success toast
4. 403 on list → EmptyState "Token access required"
5. zero tokens → EmptyState with a mint CTA button that opens the sheet
FAIL first.
- [ ] **Step 2:** implement per spec Unit 3: `useTitle('Service tokens')`; header + "Mint token" primary button; table per Slice-1 conventions (mono for token names? no — sans; scope name resolution: look up config/env names from the nav caches via `useQueryClient().getQueryData` best-effort, falling back to `scope_id.slice(0, 8)`; transit renders "all keys" when scope_id empty); mint Sheet form (name input required; kind select; project→env(→config) cascading selects using `useProjects/useEnvironments/useConfigs` — env kind stops at env level; access select options by kind; TTL number input seconds optional); on mint success: close sheet, hold `minted` in state → `<RevealOnce open={!!minted} secret={minted.token} …>`, clear on close; invalidate `['tokens']`. Revoke via ConfirmDialog tone danger. Query key `['tokens']`. Errors: list inline; mutations → danger toast (`ApiError.message` for 403/409 else "Request failed.").
- [ ] **Step 3:** verify (all new tests; full suite; typecheck) → commit `feat(web): service tokens page — mint (show-once), list, revoke`.

---

### Task 4: MembersPage

**Files:** Create `web/src/members/MembersPage.tsx`; test `web/src/members/MembersPage.test.tsx`.

- [ ] **Step 1 (TDD):** tests, at least:
1. instance members render with emails joined from `/v1/users` (member u2 + user u2/email → row shows email); unknown user_id falls back to id prefix
2. scope switch to Project + pick p1 → members fetched from `/v1/projects/p1/members` (assert path)
3. role change: select new role → ConfirmDialog → confirm asserts PUT body `{role:'admin'}` → success toast
4. ceiling: PUT returns 403 `{error:{code:'forbidden',message:'cannot grant a role above your own'}}` → danger toast with that exact message
5. remove member: confirm → DELETE asserted → toast
6. add member: sheet lists enabled users only; submit PUTs role for chosen uid
7. create user: sheet email input → POST asserted → RevealOnce shows one-time password
8. disable user self-guard: 409 `{error:{code:'validation',message:'cannot disable yourself'}}` → danger toast with server message
9. 403 on members list → EmptyState "Member access required"
FAIL first.
- [ ] **Step 2:** implement per spec Unit 4: `useTitle('Members')`; scope selector (segmented select instance/project/environment + conditional project/env pickers from nav hooks) resolving to a `MemberScope`; members query `['members', memberScopePath(scope)]`; users query `['users']` (best-effort — 403 leaves emails as id prefixes and hides the Users section); members table (email, role `<select>` with confirm-on-change via ConfirmDialog, remove button via danger ConfirmDialog); "Add member" Sheet (user select over enabled non-member users + role select → putMember); Users section (instance scope only): table email/status pill/disable action (ConfirmDialog danger), "Create user" Sheet → on success `RevealOnce` with `password`, invalidate `['users']`. All mutation errors: 403/409 → danger toast with `ApiError.message`, else "Request failed."; invalidate `['members', …]` (and `['users']` for user mutations) on success.
- [ ] **Step 3:** verify → commit `feat(web): members & users page — roles, guardrail surfacing, show-once password`.

---

### Task 5: Routes + gates + tracker

**Files:** Modify `web/src/App.tsx`; modify `fe-improvements.md`.

- [ ] **Step 1:** route swaps ONLY: `/tokens` → `<TokensPage />`, `/members` → `<MembersPage />` (+ the two imports; Placeholder remains for transit/settings). Full suite green.
- [ ] **Step 2:** gates: `npm run typecheck && npx vitest run && npm run build && npm run smoke` (web/) + `go build ./...` (root; no Go changes).
- [ ] **Step 3:** `fe-improvements.md` §8: annotate **token management** and **member management** as *(SHIPPED — B4: mint/revoke with show-once raw value, users with show-once password + disable guardrails, role management at instance/project/env scope with server-guardrail surfacing)*.
- [ ] **Step 4:** commit `feat(web): wire tokens/members routes; docs(fe): check off B4`.
- [ ] **Step 5 (controller):** final whole-branch review → PR → merge per standing orders.

## Out of scope

Token rotation/rename · user re-enable (no endpoint) · transit key-scoped pickers (all-keys `""` only) · pagination · OIDC member sources (C).
