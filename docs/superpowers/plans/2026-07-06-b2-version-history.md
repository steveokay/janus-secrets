# B2 — Version History Drawer Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** A "History" button in the secret editor opens a right-side sheet listing config versions with per-version key-name diffs and audited rollback — plus the three kit primitives it needs (Toast, ConfirmDialog, Sheet) and a permanent real-browser smoke script.

**Architecture:** Kit primitives in `web/src/ui/` (Radix Toast/AlertDialog/Dialog, shadcn-lean pattern from Slice 1). Feature code in `web/src/secrets/VersionHistory.tsx` consuming three new endpoints whose types and ALL msw mocks mirror `internal/api/versions_handlers.go` exactly (the Slice-2 mock-drift rule). Rollback invalidates `['config', cid]` so the editor/dashboard refresh via existing query keys.

**Tech Stack:** Existing React 18/TS/Tailwind tokens/TanStack v5/vitest+msw. New deps: `@radix-ui/react-dialog`, `@radix-ui/react-alert-dialog`, `@radix-ui/react-toast`; dev-only `puppeteer-core`.

**Authority:** spec `docs/superpowers/specs/2026-07-06-b2-version-history-design.md` (contains the verified API contract — mocks must match it byte-for-byte). Palette gate applies: token classes only, no hex.

**All commands from `web/` unless stated. Every commit ends with the trailer (blank line before): `Co-Authored-By: Claude Fable 5 <noreply@anthropic.com>`. Never push.**

---

### Task 0: Branch

- [ ] **Step 1** (repo root): `git checkout -b milestone-15-b2-history` from up-to-date main; `git status` clean.

---

### Task 1: Deps + endpoints + types

**Files:** Modify `web/package.json` (via npm), `web/src/lib/endpoints.ts`, `web/src/lib/endpoints.test.ts` (append only).

- [ ] **Step 1:** `npm install @radix-ui/react-dialog @radix-ui/react-alert-dialog @radix-ui/react-toast && npm install -D puppeteer-core`

- [ ] **Step 2 (TDD):** Append to `web/src/lib/endpoints.test.ts` (follow the file's existing msw pattern; REAL shapes from the spec's API contract):

```ts
test('listVersions unwraps versions with real shape', async () => {
  server.use(http.get('/v1/configs/c1/versions', () =>
    HttpResponse.json({ versions: [
      { version: 2, message: 'rotate keys', created_by: 'steve@acme.dev', created_at: '2026-07-06T10:00:00Z' },
      { version: 1, message: '', created_by: 'steve@acme.dev', created_at: '2026-07-05T10:00:00Z' },
    ] }),
  ))
  await expect(endpoints.listVersions('c1')).resolves.toHaveLength(2)
})

test('diffVersions returns key-name arrays', async () => {
  server.use(http.get('/v1/configs/c1/versions/diff', () =>
    HttpResponse.json({ a: 1, b: 2, added: ['NEW_KEY'], changed: ['DB_URL'], removed: [] }),
  ))
  await expect(endpoints.diffVersions('c1', 1, 2)).resolves.toMatchObject({ a: 1, b: 2, added: ['NEW_KEY'] })
})

test('rollback posts target_version and message', async () => {
  let body: unknown
  server.use(http.post('/v1/configs/c1/rollback', async ({ request }) => {
    body = await request.json()
    return HttpResponse.json({ version: 3, id: 'cv3', created_at: '2026-07-06T11:00:00Z' })
  }))
  await expect(endpoints.rollback('c1', 1, 'Rollback to v1')).resolves.toMatchObject({ version: 3 })
  expect(body).toEqual({ target_version: 1, message: 'Rollback to v1' })
})
```

Run: FAIL (endpoints missing).

- [ ] **Step 3:** In `web/src/lib/endpoints.ts` add types + endpoints:

```ts
export interface VersionMeta { version: number; message: string; created_by: string; created_at: string }
export interface VersionDiff { a: number; b: number; added: string[]; changed: string[]; removed: string[] }
```

and in the `endpoints` object (after `saveSecrets`):

```ts
  // versions (B2): reads are config:read and NOT audited; diff is key NAMES only.
  listVersions: (cid: string) =>
    api.get<{ versions: VersionMeta[] }>(`/v1/configs/${cid}/versions`).then((r) => r.versions),
  diffVersions: (cid: string, a: number, b: number) =>
    api.get<VersionDiff>(`/v1/configs/${cid}/versions/diff?a=${a}&b=${b}`),
  rollback: (cid: string, target_version: number, message: string) =>
    api.post<VersionResult>(`/v1/configs/${cid}/rollback`, { target_version, message }),
```

- [ ] **Step 4:** Verify: appended tests PASS; full suite green; typecheck clean.
- [ ] **Step 5:** Commit: `git add package.json package-lock.json src/lib/endpoints.ts src/lib/endpoints.test.ts` → `feat(web): version endpoints + radix/puppeteer deps (b2)`

---

### Task 2: Toast

**Files:** Create `web/src/ui/Toast.tsx`; Test `web/src/ui/Toast.test.tsx`; Modify `web/src/App.tsx` (wrap providers).

- [ ] **Step 1 (TDD):** `web/src/ui/Toast.test.tsx`:

```tsx
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { ToastProvider, useToast } from './Toast'

function Pusher() {
  const toast = useToast()
  return (
    <>
      <button onClick={() => toast({ title: 'Saved as v2' })}>ok</button>
      <button onClick={() => toast({ title: 'Failed.', tone: 'danger' })}>bad</button>
    </>
  )
}

test('pushes success and danger toasts', async () => {
  render(<ToastProvider><Pusher /></ToastProvider>)
  await userEvent.click(screen.getByRole('button', { name: 'ok' }))
  expect(await screen.findByText('Saved as v2')).toBeInTheDocument()
  await userEvent.click(screen.getByRole('button', { name: 'bad' }))
  expect(await screen.findByText('Failed.')).toBeInTheDocument()
})
```

Run: FAIL.

- [ ] **Step 2:** Implement `web/src/ui/Toast.tsx`:

```tsx
import { createContext, useCallback, useContext, useState, ReactNode } from 'react'
import * as RT from '@radix-ui/react-toast'

type Push = (t: { title: string; tone?: 'success' | 'danger' }) => void
type Msg = { id: number; title: string; tone: 'success' | 'danger' }

const Ctx = createContext<Push>(() => {})
export const useToast = () => useContext(Ctx)

// App-level toast surface. NEVER pass secret values in titles.
export function ToastProvider({ children }: { children: ReactNode }) {
  const [msgs, setMsgs] = useState<Msg[]>([])
  const push = useCallback<Push>((t) => {
    setMsgs((s) => [...s, { id: Date.now() + Math.random(), title: t.title, tone: t.tone ?? 'success' }])
  }, [])
  return (
    <Ctx.Provider value={push}>
      <RT.Provider swipeDirection="right" duration={4000}>
        {children}
        {msgs.map((m) => (
          <RT.Root
            key={m.id}
            onOpenChange={(open) => { if (!open) setMsgs((s) => s.filter((x) => x.id !== m.id)) }}
            className="flex items-center gap-2.5 rounded-card bg-ink px-4 py-2.5 text-[12.5px] text-card shadow-pop"
          >
            <span aria-hidden className={m.tone === 'success' ? 'text-success' : 'text-danger'}>
              {m.tone === 'success' ? '✓' : '✕'}
            </span>
            <RT.Title>{m.title}</RT.Title>
          </RT.Root>
        ))}
        <RT.Viewport className="fixed bottom-4 right-4 z-50 flex w-[360px] max-w-[calc(100vw-2rem)] flex-col gap-2" />
      </RT.Provider>
    </Ctx.Provider>
  )
}
```

- [ ] **Step 3:** In `web/src/App.tsx`, import `ToastProvider` from `./ui/Toast` and wrap inside QueryClientProvider:

```tsx
    <QueryClientProvider client={queryClient}>
      <ToastProvider>
        <BrowserRouter>
          <AuthProvider>
            <Gate />
          </AuthProvider>
        </BrowserRouter>
      </ToastProvider>
    </QueryClientProvider>
```

- [ ] **Step 4:** Verify: new test PASS; full suite green; typecheck clean.
- [ ] **Step 5:** Commit → `feat(web): toast system (radix) with app-level provider`

---

### Task 3: ConfirmDialog

**Files:** Create `web/src/ui/ConfirmDialog.tsx`; Test `web/src/ui/ConfirmDialog.test.tsx`.

- [ ] **Step 1 (TDD):** `web/src/ui/ConfirmDialog.test.tsx`:

```tsx
import { useState } from 'react'
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { ConfirmDialog } from './ConfirmDialog'

function Host({ onConfirm }: { onConfirm: () => void }) {
  const [open, setOpen] = useState(true)
  return (
    <ConfirmDialog
      open={open}
      onOpenChange={setOpen}
      title="Roll back to v2?"
      body="This creates a new version."
      confirmLabel="Roll back"
      onConfirm={onConfirm}
    />
  )
}

test('confirm fires callback; cancel closes without firing', async () => {
  const onConfirm = vi.fn()
  render(<Host onConfirm={onConfirm} />)
  expect(await screen.findByText('Roll back to v2?')).toBeInTheDocument()
  await userEvent.click(screen.getByRole('button', { name: 'Roll back' }))
  expect(onConfirm).toHaveBeenCalledOnce()
})

test('cancel does not fire confirm', async () => {
  const onConfirm = vi.fn()
  render(<Host onConfirm={onConfirm} />)
  await userEvent.click(await screen.findByRole('button', { name: 'Cancel' }))
  expect(onConfirm).not.toHaveBeenCalled()
  expect(screen.queryByText('Roll back to v2?')).not.toBeInTheDocument()
})
```

(`vi` is available via vitest globals.) Run: FAIL.

- [ ] **Step 2:** Implement `web/src/ui/ConfirmDialog.tsx`:

```tsx
import { ReactNode } from 'react'
import * as AD from '@radix-ui/react-alert-dialog'
import { cn } from './cn'

export function ConfirmDialog({ open, onOpenChange, title, body, confirmLabel, tone = 'brand', onConfirm }: {
  open: boolean
  onOpenChange: (open: boolean) => void
  title: string
  body: ReactNode
  confirmLabel: string
  tone?: 'brand' | 'danger'
  onConfirm: () => void
}) {
  return (
    <AD.Root open={open} onOpenChange={onOpenChange}>
      <AD.Portal>
        <AD.Overlay className="fixed inset-0 z-50 bg-ink/30" />
        <AD.Content className="fixed left-1/2 top-1/2 z-50 w-80 -translate-x-1/2 -translate-y-1/2 rounded-card border border-line bg-card p-5 shadow-pop">
          <AD.Title className="mb-2 text-[15px] font-semibold tracking-tight">{title}</AD.Title>
          <AD.Description className="mb-4 text-[12.5px] text-muted">{body}</AD.Description>
          <div className="flex justify-end gap-2">
            <AD.Cancel className="rounded border border-line bg-card px-3 py-1.5 text-[13px] font-semibold">Cancel</AD.Cancel>
            <AD.Action
              onClick={onConfirm}
              className={cn('rounded px-3 py-1.5 text-[13px] font-semibold text-white', tone === 'danger' ? 'bg-danger' : 'bg-brand')}
            >
              {confirmLabel}
            </AD.Action>
          </div>
        </AD.Content>
      </AD.Portal>
    </AD.Root>
  )
}
```

- [ ] **Step 3:** Verify PASS; suite green; typecheck clean.
- [ ] **Step 4:** Commit → `feat(web): ConfirmDialog primitive (radix alert-dialog)`

---

### Task 4: Sheet

**Files:** Create `web/src/ui/Sheet.tsx`; Test `web/src/ui/Sheet.test.tsx`.

- [ ] **Step 1 (TDD):** `web/src/ui/Sheet.test.tsx`:

```tsx
import { useState } from 'react'
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { Sheet } from './Sheet'

function Host() {
  const [open, setOpen] = useState(true)
  return <Sheet open={open} onOpenChange={setOpen} title="Version history"><p>content here</p></Sheet>
}

test('renders title and children; close button dismisses', async () => {
  render(<Host />)
  expect(await screen.findByText('Version history')).toBeInTheDocument()
  expect(screen.getByText('content here')).toBeInTheDocument()
  await userEvent.click(screen.getByRole('button', { name: /close/i }))
  expect(screen.queryByText('Version history')).not.toBeInTheDocument()
})
```

Run: FAIL.

- [ ] **Step 2:** Implement `web/src/ui/Sheet.tsx`:

```tsx
import { ReactNode } from 'react'
import * as D from '@radix-ui/react-dialog'
import { X } from 'lucide-react'

// Right-side slide-over panel (shadcn-lean Radix Dialog variant).
export function Sheet({ open, onOpenChange, title, children }: {
  open: boolean
  onOpenChange: (open: boolean) => void
  title: string
  children: ReactNode
}) {
  return (
    <D.Root open={open} onOpenChange={onOpenChange}>
      <D.Portal>
        <D.Overlay className="fixed inset-0 z-50 bg-ink/30" />
        <D.Content aria-describedby={undefined} className="fixed inset-y-0 right-0 z-50 w-[380px] max-w-full overflow-y-auto border-l border-line bg-card p-5 shadow-pop">
          <div className="mb-4 flex items-center justify-between">
            <D.Title className="text-[15px] font-semibold tracking-tight">{title}</D.Title>
            <D.Close aria-label="close" className="flex h-6 w-6 items-center justify-center rounded text-faint hover:bg-line-soft hover:text-ink">
              <X size={15} strokeWidth={1.7} />
            </D.Close>
          </div>
          {children}
        </D.Content>
      </D.Portal>
    </D.Root>
  )
}
```

(`aria-describedby={undefined}` silences Radix's missing-Description warning — the sheet body is arbitrary content.)

- [ ] **Step 3:** Verify PASS; suite green; typecheck clean.
- [ ] **Step 4:** Commit → `feat(web): Sheet slide-over primitive (radix dialog)`

---

### Task 5: VersionHistory

**Files:** Create `web/src/secrets/VersionHistory.tsx`; Test `web/src/secrets/VersionHistory.test.tsx`.

- [ ] **Step 1 (TDD):** `web/src/secrets/VersionHistory.test.tsx` — REAL shapes only:

```tsx
import { http, HttpResponse } from 'msw'
import { screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { server } from '../test/msw'
import { renderApp } from '../test/render'
import { ToastProvider } from '../ui/Toast'
import { VersionHistory } from './VersionHistory'

const VERSIONS = {
  versions: [
    { version: 3, message: 'rotate stripe key', created_by: 'steve@acme.dev', created_at: '2026-07-06T10:00:00Z' },
    { version: 2, message: '', created_by: 'steve@acme.dev', created_at: '2026-07-05T10:00:00Z' },
    { version: 1, message: 'initial import', created_by: 'steve@acme.dev', created_at: '2026-07-04T10:00:00Z' },
  ],
}

function mount(dirty = false) {
  return renderApp(
    <ToastProvider><VersionHistory cid="c1" dirty={dirty} /></ToastProvider>,
    { withAuth: false },
  )
}

test('lists versions newest-first with current marker; v1 shows Initial version', async () => {
  server.use(http.get('/v1/configs/c1/versions', () => HttpResponse.json(VERSIONS)))
  mount()
  expect(await screen.findByText('v3')).toBeInTheDocument()
  expect(screen.getByText('current')).toBeInTheDocument()
  expect(screen.getByText('no message')).toBeInTheDocument()
  await userEvent.click(screen.getByText('initial import'))
  expect(await screen.findByText('Initial version')).toBeInTheDocument()
})

test('expanding v3 loads diff vs v2 and renders key chips', async () => {
  server.use(
    http.get('/v1/configs/c1/versions', () => HttpResponse.json(VERSIONS)),
    http.get('/v1/configs/c1/versions/diff', ({ request }) => {
      const u = new URL(request.url)
      expect(u.searchParams.get('a')).toBe('2')
      expect(u.searchParams.get('b')).toBe('3')
      return HttpResponse.json({ a: 2, b: 3, added: ['SENTRY_DSN'], changed: ['STRIPE_KEY'], removed: ['LEGACY'] })
    }),
  )
  mount()
  await userEvent.click(await screen.findByText('rotate stripe key'))
  expect(await screen.findByText('SENTRY_DSN')).toBeInTheDocument()
  expect(screen.getByText('STRIPE_KEY')).toBeInTheDocument()
  expect(screen.getByText('LEGACY')).toBeInTheDocument()
})

test('rollback: confirm dialog → POST body → success toast', async () => {
  let body: unknown
  server.use(
    http.get('/v1/configs/c1/versions', () => HttpResponse.json(VERSIONS)),
    http.post('/v1/configs/c1/rollback', async ({ request }) => {
      body = await request.json()
      return HttpResponse.json({ version: 4, id: 'cv4', created_at: '2026-07-06T12:00:00Z' })
    }),
  )
  mount()
  const rollbacks = await screen.findAllByRole('button', { name: 'Roll back' })
  await userEvent.click(rollbacks[0]) // v2's button (v3 is current)
  await userEvent.click(await screen.findByRole('button', { name: 'Roll back', hidden: false }))
  // the dialog's confirm button has the same label; the click above targets the dialog (last rendered)
  expect(await screen.findByText('Rolled back to v2 — saved as v4')).toBeInTheDocument()
  expect(body).toEqual({ target_version: 2, message: 'Rollback to v2' })
})

test('dirty editor disables rollback with hint', async () => {
  server.use(http.get('/v1/configs/c1/versions', () => HttpResponse.json(VERSIONS)))
  mount(true)
  const btns = await screen.findAllByRole('button', { name: 'Roll back' })
  expect(btns[0]).toBeDisabled()
  expect(btns[0]).toHaveAttribute('title', 'Save or discard your changes first')
})
```

NOTE on the rollback test: after clicking the row's "Roll back", the ConfirmDialog opens and ALSO contains a button named "Roll back". If the double-click targeting is ambiguous, disambiguate by scoping the second click to the dialog: `within(screen.getByRole('alertdialog')).getByRole('button', { name: 'Roll back' })` (import `within` from @testing-library/react). Use that form if the plain query matches multiple elements.

Run: FAIL (module missing).

- [ ] **Step 2:** Implement `web/src/secrets/VersionHistory.tsx`:

```tsx
import { useState } from 'react'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { endpoints, VersionMeta } from '../lib/endpoints'
import { Pill } from '../ui/Pill'
import { ConfirmDialog } from '../ui/ConfirmDialog'
import { useToast } from '../ui/Toast'
import { timeAgo } from '../lib/time'
import { cn } from '../ui/cn'

function DiffView({ cid, version }: { cid: string; version: number }) {
  // Key NAMES only — the server never returns values on this surface.
  const diff = useQuery({
    queryKey: ['config', cid, 'diff', version - 1, version],
    queryFn: () => endpoints.diffVersions(cid, version - 1, version),
  })
  if (diff.isLoading) return <p className="text-[12px] text-faint">Loading…</p>
  if (diff.isError) return <p className="text-[12px] text-danger">Couldn't load diff.</p>
  const d = diff.data!
  const groups = [
    { label: 'Added', keys: d.added, tone: 'success' as const },
    { label: 'Changed', keys: d.changed, tone: 'warning' as const },
    { label: 'Removed', keys: d.removed, tone: 'danger' as const },
  ].filter((g) => g.keys.length > 0)
  if (!groups.length) return <p className="text-[12px] text-faint">No key changes</p>
  return (
    <div className="flex flex-col gap-2">
      {groups.map((g) => (
        <div key={g.label}>
          <p className="mb-1 text-[10.5px] font-bold uppercase tracking-[.1em] text-faint">{g.label}</p>
          <div className="flex flex-wrap gap-1">
            {g.keys.map((k) => <Pill key={k} tone={g.tone} className="font-mono text-[11px]">{k}</Pill>)}
          </div>
        </div>
      ))}
    </div>
  )
}

export function VersionHistory({ cid, dirty }: { cid: string; dirty: boolean }) {
  const qc = useQueryClient()
  const toast = useToast()
  const versions = useQuery({ queryKey: ['config', cid, 'versions'], queryFn: () => endpoints.listVersions(cid) })
  const [openDiff, setOpenDiff] = useState<number | null>(null)
  const [confirming, setConfirming] = useState<VersionMeta | null>(null)

  const rollback = useMutation({
    mutationFn: (v: VersionMeta) => endpoints.rollback(cid, v.version, `Rollback to v${v.version}`),
    onSuccess: (res, v) => {
      toast({ title: `Rolled back to v${v.version} — saved as v${res.version}` })
      void qc.invalidateQueries({ queryKey: ['config', cid] })
    },
    onError: () => toast({ title: 'Rollback failed.', tone: 'danger' }),
  })

  if (versions.isLoading) return <p className="text-[12.5px] text-faint">Loading…</p>
  if (versions.isError) return <p role="alert" className="text-[12.5px] text-danger">Couldn't load versions.</p>
  const list = [...(versions.data ?? [])].sort((x, y) => y.version - x.version)
  const latest = list[0]?.version

  return (
    <ul className="flex flex-col gap-1.5">
      {list.map((v) => (
        <li key={v.version} className="rounded-card border border-line-soft">
          <button
            type="button"
            onClick={() => setOpenDiff((s) => (s === v.version ? null : v.version))}
            className="flex w-full items-center gap-2 px-3 py-2 text-left hover:bg-line-soft/50"
          >
            <Pill tone={v.version === latest ? 'success' : 'brand'}>v{v.version}</Pill>
            <span className={cn('flex-1 truncate text-[13px]', v.message ? 'text-ink' : 'text-faint')}>
              {v.message || 'no message'}
            </span>
          </button>
          <div className="flex items-center justify-between px-3 pb-2 text-[11.5px] text-faint">
            <span>{v.created_by} · {timeAgo(v.created_at)}</span>
            {v.version === latest ? (
              <span className="text-[10.5px] font-bold uppercase tracking-[.1em]">current</span>
            ) : (
              <button
                type="button"
                disabled={dirty || rollback.isPending}
                title={dirty ? 'Save or discard your changes first' : undefined}
                onClick={() => setConfirming(v)}
                className="rounded border border-line bg-card px-2 py-0.5 text-[11.5px] font-semibold disabled:opacity-40"
              >
                Roll back
              </button>
            )}
          </div>
          {openDiff === v.version && (
            <div className="border-t border-line-soft px-3 py-2">
              {v.version === 1
                ? <p className="text-[12px] text-faint">Initial version</p>
                : <DiffView cid={cid} version={v.version} />}
            </div>
          )}
        </li>
      ))}
      <ConfirmDialog
        open={confirming !== null}
        onOpenChange={(o) => { if (!o) setConfirming(null) }}
        title={`Roll back to v${confirming?.version}?`}
        body={`This creates a new version that restores v${confirming?.version}'s keys — nothing is deleted.`}
        confirmLabel="Roll back"
        onConfirm={() => { if (confirming) rollback.mutate(confirming); setConfirming(null) }}
      />
    </ul>
  )
}
```

- [ ] **Step 3:** Verify: all 4 new tests PASS; full suite green; typecheck clean.
- [ ] **Step 4:** Commit → `feat(web): version history list, key-name diffs, audited rollback flow`

---

### Task 6: SecretEditor integration

**Files:** Modify `web/src/secrets/SecretEditor.tsx`; Modify `web/src/secrets/SecretEditor.test.tsx` (append ONE test).

- [ ] **Step 1 (TDD):** Append to `SecretEditor.test.tsx` (reuse the file's existing handler pattern + `withAuth: false`):

```tsx
test('History button opens the version sheet', async () => {
  // reuse an existing config handler; add a versions handler:
  // http.get('/v1/configs/<cid>/versions', () => HttpResponse.json({ versions: [
  //   { version: 1, message: 'first', created_by: 'x@y.io', created_at: '2026-07-04T10:00:00Z' },
  // ] }))
  renderApp(<SecretEditor />, { route: '/projects/p1/configs/c1', withAuth: false })
  await userEvent.click(await screen.findByRole('button', { name: /history/i }))
  expect(await screen.findByText('Version history')).toBeInTheDocument()
  expect(await screen.findByText('first')).toBeInTheDocument()
})
```

Note: the editor now requires `ToastProvider` in the tree (VersionHistory calls `useToast`). If existing SecretEditor tests fail with a context error, they won't — `useToast` falls back to the no-op default context value, so ONLY wrap the new test if needed; prefer wrapping the `renderApp(...)` ui in `<ToastProvider>` for the new test. Run: FAIL (no History button).

- [ ] **Step 2:** In `web/src/secrets/SecretEditor.tsx`:
- Imports: `import { History } from 'lucide-react'`, `import { Sheet } from '../ui/Sheet'`, `import { VersionHistory } from './VersionHistory'`.
- State: `const [showHistory, setShowHistory] = useState(false)`.
- In the header row, wrap the save button and a new History button in `<div className="flex items-center gap-2">`:

```tsx
        <div className="flex items-center gap-2">
          <button
            type="button"
            onClick={() => setShowHistory(true)}
            className="flex items-center gap-1.5 rounded border border-line bg-card px-3 py-1.5 text-[13px] font-semibold"
          >
            <History size={14} strokeWidth={1.7} /> History
          </button>
          {/* existing save button unchanged */}
        </div>
```

- After `<AddKeyRow ... />` add:

```tsx
      <Sheet open={showHistory} onOpenChange={setShowHistory} title="Version history">
        <VersionHistory cid={cid} dirty={dirty} />
      </Sheet>
```

No other editor changes.

- [ ] **Step 3:** Verify: all `src/secrets` tests PASS (existing ones untouched); full suite green; typecheck clean.
- [ ] **Step 4:** Commit → `feat(web): history button opens version sheet in secret editor`

---

### Task 7: Real-browser smoke script

**Files:** Create `web/scripts/smoke.mjs`; Modify `web/package.json` (add script `"smoke": "node scripts/smoke.mjs"`).

- [ ] **Step 1:** Create `web/scripts/smoke.mjs`:

```js
// Real-browser smoke: boots the served bundle in headless Edge/Chrome with
// REAL-shape /v1 fixtures and fails on page errors or an empty root.
// Codifies the rule from the 2026-07-06 blank-page incident: unit tests with
// invented mock shapes cannot catch authed-shell crashes.
import { existsSync } from 'node:fs'
import puppeteer from 'puppeteer-core'

const BASE = process.env.SMOKE_URL ?? 'http://127.0.0.1:8210'
const exe = [
  'C:/Program Files (x86)/Microsoft/Edge/Application/msedge.exe',
  'C:/Program Files/Google/Chrome/Application/chrome.exe',
].find((p) => existsSync(p))
if (!exe) { console.error('smoke: no browser binary found'); process.exit(2) }

// Shapes mirror internal/api handlers — update alongside the Go code.
const fixtures = {
  '/v1/sys/seal-status': { initialized: true, sealed: false, type: 'shamir', threshold: 1, shares: 1 },
  '/v1/auth/me': { kind: 'user', id: 'u1', name: 'smoke@janus.dev' },
  '/v1/projects': { projects: [{ id: 'p1', slug: 'acme', name: 'acme-api' }] },
  '/v1/projects/p1/environments': { environments: [] },
}

const browser = await puppeteer.launch({ executablePath: exe, headless: 'new' })
const page = await browser.newPage()
const errors = []
page.on('pageerror', (e) => errors.push(String(e)))
await page.setRequestInterception(true)
page.on('request', (req) => {
  const path = new URL(req.url()).pathname
  if (path in fixtures) {
    return req.respond({ status: 200, contentType: 'application/json', body: JSON.stringify(fixtures[path]) })
  }
  if (path.startsWith('/v1/')) {
    return req.respond({ status: 404, contentType: 'application/json', body: '{"error":{"code":"not_found","message":"smoke fixture missing"}}' })
  }
  return req.continue()
})
await page.goto(BASE + '/', { waitUntil: 'networkidle0', timeout: 20000 })
await new Promise((r) => setTimeout(r, 800))
const html = await page.evaluate(() => document.getElementById('root')?.innerHTML ?? '')
await browser.close()

if (errors.length || html.length < 500 || !html.includes('Janus')) {
  console.error('SMOKE FAILED', JSON.stringify({ errors, rootLength: html.length }, null, 2))
  process.exit(1)
}
console.log(`smoke ok — authed shell rendered (${html.length} chars)`)
```

- [ ] **Step 2:** Add to `web/package.json` scripts: `"smoke": "node scripts/smoke.mjs"`.
- [ ] **Step 3:** Run it against the running dev container: `npm run smoke` → expect `smoke ok`. (If the container isn't running, note that in the report rather than failing the task.)
- [ ] **Step 4:** Commit → `test(web): real-browser smoke script (puppeteer-core) per mock-drift rule`

---

### Task 8: Gates + tracker

**Files:** Modify `fe-improvements.md`.

- [ ] **Step 1:** Gates: `npm run typecheck && npx vitest run && npm run build` (web/), `go build ./...` (root) — all green.
- [ ] **Step 2:** `fe-improvements.md`: check `[x]` §3 "Version history drawer" *(B2 — Sheet drawer, key-name diffs, audited rollback; dirty editor disables rollback)*; check `[x]` §4 "Modal/Dialog" *(B2 — ConfirmDialog + Sheet via Radix; CreateForms migration to it deferred)* and `[x]` §4 "Toast/notification" *(B2 — app-level provider; editor save/copy toasts arrive with Slice 3)*.
- [ ] **Step 3:** Commit → `docs(fe): check off B2 version history + dialog/toast kit items`
- [ ] **Step 4 (controller):** Final whole-branch review → PR.

## Out of scope

Value-level diffs · editor save/copy toasts (Slice 3) · CreateForms/ChangePassword migration onto ConfirmDialog/Sheet · sheet animations · versions pagination.
