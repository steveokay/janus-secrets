# B5 — Transit (KMS) Web UI Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development. Steps use checkbox (`- [ ]`) syntax.

**Goal:** A `/transit` screen to manage transit keys (create/rotate/configure/trim/delete, versions, `min_decryption_version`) and run a plaintext-free crypto playground (encrypt/rewrap for aes256-gcm; sign/verify for ed25519), built in the dark redesign system.

**Architecture:** New `web/src/transit/` module (`TransitPage.tsx`, `KeyActions.tsx`, `Playground.tsx`) + transit types/endpoints in `lib/endpoints.ts` + a Sidebar nav item and `/transit` route. Instance-scoped. Key list is a cached query; crypto ops are un-cached mutations whose results live in local component state only. Follows the B4 management-page pattern (Radix dropdown, ConfirmDialog, toasts via `apiErrorTitle`).

**Tech Stack:** React 18 + TS + Tailwind (dark CSS-var tokens) + TanStack Query v5 + Radix + lucide + Vitest/MSW.

**Spec:** `docs/superpowers/specs/2026-07-07-transit-ui-design.md` (authoritative — read it). **Wire-shape rule:** msw mocks MUST mirror the Go handler shapes (see spec's API contract).

**Design constraints (CLAUDE.md):** token classes only — never raw palette/hex/`dark:` (gates: `no-raw-palette.test.ts`, `dark-aa.test.ts`). Both themes must render. No secret plaintext in any response (decrypt/datakey are OUT of scope).

**Reference files implementers should read for established patterns:** `web/src/tokens/TokensPage.tsx` + `web/src/members/MembersPage.tsx` (mgmt page: Radix dropdown, ConfirmDialog, `apiErrorTitle` toasts, mutation+invalidate), `web/src/shell/UserMenu.tsx` (Radix dropdown-menu), `web/src/ui/` (`Pill`, `EmptyState`, `ConfirmDialog`, `Toast`/toast hook, `cn`), `web/src/lib/api.ts` (`apiErrorTitle`, `ApiError`), `web/src/test/render.tsx` + `web/src/test/msw.ts`.

**Branch:** `milestone-22-b5-transit-ui` (already created).

---

### Task 1: Transit types + endpoints

**Files:** Modify `web/src/lib/endpoints.ts`; test `web/src/lib/endpoints.transit.test.ts`.

- [ ] **Step 1:** Add these types near the other interfaces in `endpoints.ts`:
```ts
// transit (B5) — instance-scoped KMS. Key-mgmt is audited; crypto ops are not.
export type TransitKeyType = 'aes256-gcm' | 'ed25519'
export interface TransitKey {
  name: string
  type: TransitKeyType
  latest_version: number
  min_decryption_version: number
  deletion_allowed: boolean
  versions: number[]
}
export interface TransitKeyConfig { min_decryption_version?: number; deletion_allowed?: boolean }
```

- [ ] **Step 2:** Add to the `endpoints` object (after the tokens/members block). `enc` = `encodeURIComponent`:
```ts
  // transit (B5). Crypto op responses are used in ephemeral component state only
  // (never cached); no decrypt/datakey here, so no plaintext ever returns.
  listTransitKeys: () => api.get<{ keys: TransitKey[] }>('/v1/transit/keys').then((r) => r.keys),
  getTransitKey: (name: string) => api.get<TransitKey>(`/v1/transit/keys/${encodeURIComponent(name)}`),
  createTransitKey: (name: string, type: TransitKeyType) =>
    api.post<TransitKey>('/v1/transit/keys', { name, type }),
  rotateTransitKey: (name: string) =>
    api.post<TransitKey>(`/v1/transit/keys/${encodeURIComponent(name)}/rotate`, {}),
  configTransitKey: (name: string, cfg: TransitKeyConfig) =>
    api.post<TransitKey>(`/v1/transit/keys/${encodeURIComponent(name)}/config`, cfg),
  trimTransitKey: (name: string, min_available_version: number) =>
    api.post<TransitKey>(`/v1/transit/keys/${encodeURIComponent(name)}/trim`, { min_available_version }),
  deleteTransitKey: (name: string) =>
    api.del<void>(`/v1/transit/keys/${encodeURIComponent(name)}`),
  transitEncrypt: (name: string, plaintext: string, associated_data?: string) =>
    api.post<{ ciphertext: string }>(`/v1/transit/encrypt/${encodeURIComponent(name)}`, { plaintext, associated_data }),
  transitRewrap: (name: string, ciphertext: string, associated_data?: string) =>
    api.post<{ ciphertext: string }>(`/v1/transit/rewrap/${encodeURIComponent(name)}`, { ciphertext, associated_data }),
  transitSign: (name: string, input: string) =>
    api.post<{ signature: string }>(`/v1/transit/sign/${encodeURIComponent(name)}`, { input }),
  transitVerify: (name: string, input: string, signature: string) =>
    api.post<{ valid: boolean }>(`/v1/transit/verify/${encodeURIComponent(name)}`, { input, signature }),
```

- [ ] **Step 3:** Test `web/src/lib/endpoints.transit.test.ts` — assert the wire shapes with msw (mirror the Go handlers):
```ts
import { http, HttpResponse } from 'msw'
import { server } from '../test/msw'
import { endpoints } from './endpoints'

test('listTransitKeys unwraps {keys}', async () => {
  server.use(http.get('/v1/transit/keys', () =>
    HttpResponse.json({ keys: [
      { name: 'app', type: 'aes256-gcm', latest_version: 2, min_decryption_version: 1, deletion_allowed: false, versions: [1, 2] },
    ] })))
  const keys = await endpoints.listTransitKeys()
  expect(keys).toHaveLength(1)
  expect(keys[0]).toMatchObject({ name: 'app', type: 'aes256-gcm', latest_version: 2, versions: [1, 2] })
})

test('createTransitKey posts name + type', async () => {
  let body: unknown
  server.use(http.post('/v1/transit/keys', async ({ request }) => {
    body = await request.json()
    return HttpResponse.json({ name: 'k', type: 'ed25519', latest_version: 1, min_decryption_version: 1, deletion_allowed: false, versions: [1] }, { status: 201 })
  }))
  await endpoints.createTransitKey('k', 'ed25519')
  expect(body).toEqual({ name: 'k', type: 'ed25519' })
})

test('transitEncrypt posts base64 plaintext and returns ciphertext', async () => {
  server.use(http.post('/v1/transit/encrypt/app', async ({ request }) => {
    const b = (await request.json()) as { plaintext: string }
    expect(b.plaintext).toBe('aGVsbG8=')
    return HttpResponse.json({ ciphertext: 'janus:v2:Zm9v' })
  }))
  const r = await endpoints.transitEncrypt('app', 'aGVsbG8=')
  expect(r.ciphertext).toBe('janus:v2:Zm9v')
})
```

- [ ] **Step 4:** Run `npx vitest run src/lib/endpoints.transit.test.ts` (green), `npx vitest run` (full green), `npm run typecheck`. Commit: `feat(web): transit types + endpoints`.

---

### Task 2: Transit nav, route, key list + create-key modal

**Files:** Create `web/src/transit/TransitPage.tsx`; modify `web/src/shell/Sidebar.tsx`, `web/src/App.tsx`; test `web/src/transit/TransitPage.test.tsx`.

- [ ] **Step 1 — nav + route.**
  - `Sidebar.tsx`: import `Shield` from lucide (`KeyRound` is taken by Tokens); add to the `PRIMARY` array, before Settings: `{ to: '/transit', label: 'Transit', Icon: Shield, match: (p: string) => p === '/transit' }`.
  - `App.tsx`: import `TransitPage` and add `<Route path="/transit" element={<TransitPage />} />` alongside the other instance routes (`/members`, `/tokens`).

- [ ] **Step 2 (TDD): `web/src/transit/TransitPage.test.tsx`:**
```tsx
import { http, HttpResponse } from 'msw'
import { screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { server } from '../test/msw'
import { renderApp } from '../test/render'
import { TransitPage } from './TransitPage'

const KEYS = [
  { name: 'app', type: 'aes256-gcm', latest_version: 3, min_decryption_version: 2, deletion_allowed: false, versions: [1, 2, 3] },
  { name: 'signer', type: 'ed25519', latest_version: 1, min_decryption_version: 1, deletion_allowed: true, versions: [1] },
]
function mockKeys(keys = KEYS) {
  server.use(http.get('/v1/transit/keys', () => HttpResponse.json({ keys })))
}

test('lists keys with type, version and min-decryption cues', async () => {
  mockKeys()
  renderApp(<TransitPage />, { route: '/transit', withAuth: false })
  const app = await screen.findByText('app')
  const row = app.closest('[data-key-row]') as HTMLElement
  expect(within(row).getByText('aes256-gcm')).toBeInTheDocument()
  expect(within(row).getByText(/v3/)).toBeInTheDocument()
  expect(within(row).getByText(/min.*2/i)).toBeInTheDocument()
  expect(screen.getByText('signer')).toBeInTheDocument()
  expect(screen.getByText('ed25519')).toBeInTheDocument()
})

test('empty state offers to create a key', async () => {
  mockKeys([])
  renderApp(<TransitPage />, { route: '/transit', withAuth: false })
  expect(await screen.findByText(/no transit keys/i)).toBeInTheDocument()
  expect(screen.getByRole('button', { name: /create.*key|new key/i })).toBeInTheDocument()
})

test('create-key modal posts name + type and refreshes', async () => {
  mockKeys([])
  let created: unknown
  server.use(http.post('/v1/transit/keys', async ({ request }) => {
    created = await request.json()
    return HttpResponse.json({ name: 'newkey', type: 'aes256-gcm', latest_version: 1, min_decryption_version: 1, deletion_allowed: false, versions: [1] }, { status: 201 })
  }))
  renderApp(<TransitPage />, { route: '/transit', withAuth: false })
  await userEvent.click(await screen.findByRole('button', { name: /create.*key|new key/i }))
  await userEvent.type(screen.getByLabelText(/name/i), 'newkey')
  await userEvent.click(screen.getByRole('button', { name: /^create/i }))
  expect(created).toEqual({ name: 'newkey', type: 'aes256-gcm' })
})

test('duplicate name surfaces the 409 conflict', async () => {
  mockKeys([])
  server.use(http.post('/v1/transit/keys', () =>
    HttpResponse.json({ error: { code: 'conflict', message: 'conflict' } }, { status: 409 })))
  renderApp(<TransitPage />, { route: '/transit', withAuth: false })
  await userEvent.click(await screen.findByRole('button', { name: /create.*key|new key/i }))
  await userEvent.type(screen.getByLabelText(/name/i), 'app')
  await userEvent.click(screen.getByRole('button', { name: /^create/i }))
  expect(await screen.findByRole('alert')).toHaveTextContent(/conflict/i)
})
```

- [ ] **Step 3: implement `web/src/transit/TransitPage.tsx`.** Structure (fill kit details from the reference files):
  - `useTitle('Transit')`; `useQuery(['transit','keys'], endpoints.listTransitKeys)`.
  - Header: `<h2>Transit</h2>` + one-line subtitle + a **New key** primary button (`bg-brand text-white`).
  - Loading skeleton; error `role="alert"`; empty → `EmptyState` (icon `Shield`/`KeyRound`, title "No transit keys yet", CTA).
  - Key list: each row wrapped with `data-key-row` and `data-key-name={k.name}`; shows name (mono `text-ink`), type `Pill` (`aes256-gcm`→`tone="info"`, `ed25519`→`tone="brand"`), `Pill tone="muted"` `v{latest_version}`, and when `min_decryption_version > 1` a `text-faint` `min v{n}` label, and a padlock cue for `deletion_allowed`. Selecting a row sets `selected` state (name) — used by the Playground in Task 4 (leave a `{/* playground slot */}` region + `selected` state now).
  - **Create-key modal:** reuse the shared dialog pattern from `structure/CreateForms.tsx` (a centered `Dialog`) OR a local Radix dialog matching it; fields: name input (`aria-label="name"`, client-validate `^[A-Za-z0-9_-]{1,64}$` with a hint), type radio (aes256-gcm default / ed25519). `useMutation(createTransitKey)` → on success `invalidateQueries(['transit','keys'])` + close; on error set an inline `role="alert"` line via `apiErrorTitle(err)`.
  - Token classes only.

- [ ] **Step 4:** Run the new test file (4 green), full `npx vitest run`, `npm run typecheck`, `npm run build`. Commit: `feat(web): transit page — key list, nav, create-key modal`.

---

### Task 3: Key management operations (rotate / configure / trim / delete)

**Files:** Create `web/src/transit/KeyActions.tsx`; wire into `TransitPage.tsx`; test `web/src/transit/KeyActions.test.tsx`.

- [ ] **Step 1 (TDD): `web/src/transit/KeyActions.test.tsx`** — render `TransitPage` (so the menu is in context) with a single aes key `deletion_allowed:false`:
```tsx
import { http, HttpResponse } from 'msw'
import { screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { server } from '../test/msw'
import { renderApp } from '../test/render'
import { TransitPage } from './TransitPage'

const KEY = { name: 'app', type: 'aes256-gcm', latest_version: 3, min_decryption_version: 1, deletion_allowed: false, versions: [1, 2, 3] }
function mock(key = KEY) { server.use(http.get('/v1/transit/keys', () => HttpResponse.json({ keys: [key] }))) }

test('rotate posts and refreshes', async () => {
  mock()
  let rotated = false
  server.use(http.post('/v1/transit/keys/app/rotate', () => { rotated = true; return HttpResponse.json({ ...KEY, latest_version: 4, versions: [1, 2, 3, 4] }) }))
  renderApp(<TransitPage />, { route: '/transit', withAuth: false })
  await userEvent.click(await screen.findByRole('button', { name: /actions for app|app actions|more/i }))
  await userEvent.click(await screen.findByRole('menuitem', { name: /rotate/i }))
  expect(rotated).toBe(true)
})

test('delete of a protected key surfaces the 409 verbatim', async () => {
  mock()
  server.use(http.delete('/v1/transit/keys/app', () =>
    HttpResponse.json({ error: { code: 'conflict', message: 'deletion not allowed for this key' } }, { status: 409 })))
  renderApp(<TransitPage />, { route: '/transit', withAuth: false })
  await userEvent.click(await screen.findByRole('button', { name: /actions for app|app actions|more/i }))
  await userEvent.click(await screen.findByRole('menuitem', { name: /delete/i }))
  await userEvent.click(await screen.findByRole('button', { name: /^delete|confirm/i }))
  expect(await screen.findByText(/deletion not allowed for this key/i)).toBeInTheDocument()
})

test('configure posts min_decryption_version within bounds', async () => {
  mock()
  let cfg: unknown
  server.use(http.post('/v1/transit/keys/app/config', async ({ request }) => { cfg = await request.json(); return HttpResponse.json({ ...KEY, min_decryption_version: 2 }) }))
  renderApp(<TransitPage />, { route: '/transit', withAuth: false })
  await userEvent.click(await screen.findByRole('button', { name: /actions for app|app actions|more/i }))
  await userEvent.click(await screen.findByRole('menuitem', { name: /configure/i }))
  const input = await screen.findByLabelText(/min.*decryption/i)
  await userEvent.clear(input); await userEvent.type(input, '2')
  await userEvent.click(screen.getByRole('button', { name: /save|apply/i }))
  expect(cfg).toMatchObject({ min_decryption_version: 2 })
})
```

- [ ] **Step 2: implement `KeyActions.tsx`** — a Radix `DropdownMenu` (pattern from `UserMenu.tsx`) triggered by a `⋯` icon-button with `aria-label={`actions for ${key.name}`}`. Items: **Rotate** (mutation `rotateTransitKey`, invalidate, success toast), **Configure…** (opens a dialog: `min_decryption_version` number input bounded `[1, latest_version]` with a hint, `deletion_allowed` checkbox; `configTransitKey`), **Trim…** (dialog: `min_available_version` number, hint "≤ min_decryption_version"; `trimTransitKey`), **Delete** (ConfirmDialog; `deleteTransitKey`; on error show `apiErrorTitle(err)` — for 409 this is the verbatim "deletion not allowed for this key"). All mutations `invalidateQueries(['transit','keys'])` on success. Surface errors via the shared toast/`apiErrorTitle` posture from `TokensPage`/`MembersPage`. Wire `<KeyActions key={k.name} keyMeta={k} />` into each row in `TransitPage`.

- [ ] **Step 3:** Run `KeyActions.test.tsx` (green), full suite, typecheck. Commit: `feat(web): transit key management — rotate/configure/trim/delete`.

---

### Task 4: Crypto playground

**Files:** Create `web/src/transit/Playground.tsx`; wire into `TransitPage.tsx` (`selected` slot); test `web/src/transit/Playground.test.tsx`.

- [ ] **Step 1: base64 helper** (in `Playground.tsx`): `const toB64 = (s: string) => btoa(String.fromCharCode(...new TextEncoder().encode(s)))`.

- [ ] **Step 2 (TDD): `web/src/transit/Playground.test.tsx`** — render `<Playground keyMeta={...} />` directly (no route needed) with QueryClient via `renderApp(<Playground .../>, { route: '/transit', withAuth: false })`:
```tsx
import { http, HttpResponse } from 'msw'
import { screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { server } from '../test/msw'
import { renderApp } from '../test/render'
import { Playground } from './Playground'

const AES = { name: 'app', type: 'aes256-gcm', latest_version: 2, min_decryption_version: 1, deletion_allowed: false, versions: [1, 2] } as const
const ED = { name: 'signer', type: 'ed25519', latest_version: 1, min_decryption_version: 1, deletion_allowed: false, versions: [1] } as const

test('encrypt base64-encodes the text input and shows ciphertext', async () => {
  let sent: { plaintext: string } | undefined
  server.use(http.post('/v1/transit/encrypt/app', async ({ request }) => {
    sent = (await request.json()) as { plaintext: string }
    return HttpResponse.json({ ciphertext: 'janus:v2:Zm9v' })
  }))
  renderApp(<Playground keyMeta={AES} />, { route: '/transit', withAuth: false })
  await userEvent.type(screen.getByLabelText(/plaintext|text to encrypt/i), 'hello')
  await userEvent.click(screen.getByRole('button', { name: /encrypt/i }))
  expect(await screen.findByText('janus:v2:Zm9v')).toBeInTheDocument()
  expect(sent!.plaintext).toBe('aGVsbG8=') // base64('hello')
})

test('verify shows a valid/invalid badge without treating a bad sig as an error', async () => {
  server.use(http.post('/v1/transit/verify/signer', () => HttpResponse.json({ valid: false })))
  renderApp(<Playground keyMeta={ED} />, { route: '/transit', withAuth: false })
  await userEvent.type(screen.getByLabelText(/message|input/i), 'hi')
  await userEvent.type(screen.getByLabelText(/signature/i), 'janus:v1:AAAA')
  await userEvent.click(screen.getByRole('button', { name: /verify/i }))
  expect(await screen.findByText(/invalid/i)).toBeInTheDocument()
})

test('a 403 on a crypto op surfaces a guardrail message', async () => {
  server.use(http.post('/v1/transit/encrypt/app', () =>
    HttpResponse.json({ error: { code: 'forbidden', message: 'forbidden' } }, { status: 403 })))
  renderApp(<Playground keyMeta={AES} />, { route: '/transit', withAuth: false })
  await userEvent.type(screen.getByLabelText(/plaintext|text to encrypt/i), 'x')
  await userEvent.click(screen.getByRole('button', { name: /encrypt/i }))
  expect(await screen.findByRole('alert')).toBeInTheDocument()
})
```

- [ ] **Step 3: implement `Playground.tsx`.** Props `{ keyMeta: TransitKey }`. Render an op set by `keyMeta.type`:
  - `aes256-gcm`: **Encrypt** — a textarea (`aria-label="plaintext"`), optional "Associated data" text field, an Encrypt button → `transitEncrypt(name, toB64(text), aad ? toB64(aad) : undefined)`; show `ciphertext` in a mono, copyable block. **Rewrap** — a ciphertext input (paste `janus:vN:…` verbatim), optional AAD, → `transitRewrap`; show new ciphertext.
  - `ed25519`: **Sign** — text input → `transitSign(name, toB64(text))`; show `signature` (mono, copyable). **Verify** — text input + `signature` input (`aria-label="signature"`) → `transitVerify(name, toB64(text), sig)`; show a `Pill tone="success"` "Valid" or `tone="danger"` "Invalid" per `{valid}`.
  - Each op is a `useMutation` (no query key). Results held in local `useState` — **never cached, never logged**; clear on `keyMeta.name` change (key via `useEffect` on name, or `key={keyMeta.name}` on the component from the parent). Errors → an inline `role="alert"` via `apiErrorTitle(err)`.
  - Copy buttons use `navigator.clipboard.writeText` guarded like B4's `RevealOnce` (optional).
  - Token classes only; renders in both themes.
  - In `TransitPage`, render `{selected && <Playground key={selected} keyMeta={keys.find(k => k.name === selected)!} />}` in the slot from Task 2 (the `key={selected}` remounts on switch, clearing state).

- [ ] **Step 4:** Run `Playground.test.tsx` (green), full suite, typecheck, build. Commit: `feat(web): transit crypto playground (encrypt/rewrap/sign/verify)`.

---

### Task 5: Gates, final review, tracker, merge

- [ ] **Step 1:** Full gates (web/): `npm run typecheck && npx vitest run && npm run build`. Rebuild container `docker compose up -d --build janus && ./scripts/dev-unseal.sh`; `npm run smoke` (both themes). `go build ./...` at root.
- [ ] **Step 2:** Final whole-branch review subagent — verify: no secret plaintext path (decrypt/datakey absent); crypto results never enter the query cache or logs; msw mocks match the Go wire shapes; token-only styling (gates green); both key types drive the correct op set; 403/409 guardrails surface correctly; a11y on the dropdown/dialogs.
- [ ] **Step 3:** Add a **B5** line to `fe-improvements.md` (transit UI) and check it off. Commit `docs(fe): record B5 transit UI`.
- [ ] **Step 4 (controller):** PR → merge per standing orders → rebuild container → update memory.

**Exit criteria:** `/transit` lists keys with type/version/min-dec/deletion cues; create/rotate/configure/trim/delete work with 403/409 guardrails; the playground encrypts/rewraps (aes) and signs/verifies (ed25519) with base64-encoded text input and ephemeral results; no decrypt/datakey; both themes pass smoke; go builds; final review clean.
