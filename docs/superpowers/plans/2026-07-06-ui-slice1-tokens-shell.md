# UI Slice 1 — Design Tokens + App Shell Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Wire the approved design tokens into Tailwind, rebuild the top bar and sidebar to match `docs/design/ui-mockup.html`, restyle every existing screen onto tokens, and add a build gate that forbids raw Tailwind palette classes in `web/src`.

**Architecture:** Tokens live in `web/tailwind.config.js` (roles: `page/card/line/ink/muted/faint/brand/success/warning/danger/info`) + a base layer in `index.css`. New `web/src/ui/` holds tiny shared primitives (`cn`, `Brand`, `Pill`, env-color util). Shell components (`TopBar`, `Sidebar`, new `Breadcrumb`, new `UserMenu`) are rebuilt; all other screens are mechanically converted to tokens (their full redesign is Slices 2–3). A vitest "no raw palette classes" test enforces the system forever.

**Tech Stack:** React 18 + TS + Tailwind v3 + TanStack Query v5 (existing). New deps (decided: shadcn-lean approach): `@radix-ui/react-dropdown-menu` (accessible user menu), `lucide-react` (icons), `clsx` + `tailwind-merge` (`cn()` helper). No other runtime deps.

**Authority documents:** spec `docs/superpowers/specs/2026-07-06-ui-visual-design.md`; mockup `docs/design/ui-mockup.html`. When prose here and the mockup disagree, the mockup wins.

**Token-name mapping note:** the spec's `border`/`border-soft` tokens are named **`line`/`line-soft`** in Tailwind (so classes read `border-line`, not `border-border`). `brand-line` = the spec's accent-tinted border `#D8D2FB`.

**Test invariants for every task:** never change existing `aria-label`s, roles, or user-visible strings that tests query (e.g. "Sign in", "Unseal key share", `add config to ${name}`) unless the task explicitly says so and updates the test in the same task.

**All commands run from `web/` unless stated otherwise.**

---

### Task 0: Branch

**Files:** none

- [ ] **Step 1: Create the milestone branch** (from repo root)

```bash
git checkout -b milestone-13-ui-slice1
```

- [ ] **Step 2: Verify clean state**

Run: `git status` — expect clean tree on `milestone-13-ui-slice1`.

---

### Task 1: Dependencies, design tokens, base styles, `cn()` helper

**Files:**
- Modify: `web/package.json` (via npm install)
- Modify: `web/tailwind.config.js`
- Modify: `web/src/index.css`
- Create: `web/src/ui/cn.ts`
- Test: `web/src/ui/cn.test.ts`

- [ ] **Step 1: Install dependencies**

```bash
npm install @radix-ui/react-dropdown-menu lucide-react clsx tailwind-merge
```

- [ ] **Step 2: Replace `web/tailwind.config.js` with the token theme**

```js
/** @type {import('tailwindcss').Config} */
export default {
  content: ['./index.html', './src/**/*.{ts,tsx}'],
  theme: {
    extend: {
      colors: {
        page: '#F6F6FA',
        card: '#FFFFFF',
        line: { DEFAULT: '#E5E3F0', soft: '#EEECF6' },
        ink: '#211D35',
        muted: '#6E6A85',
        faint: '#9B97B0',
        brand: { DEFAULT: '#6A5CF5', deep: '#5546E0', soft: '#EFECFE', line: '#D8D2FB' },
        success: { DEFAULT: '#178A50', soft: '#E4F5EC' },
        warning: { DEFAULT: '#B45309', soft: '#FCF0DF' },
        danger: { DEFAULT: '#C92A2A', soft: '#FBE9E9' },
        info: { DEFAULT: '#2563EB', soft: '#E7EFFD' },
      },
      borderRadius: {
        DEFAULT: '8px', // controls & inputs
        card: '10px',   // cards & tables
      },
      boxShadow: {
        card: '0 1px 2px rgba(33,29,53,.05), 0 4px 16px rgba(33,29,53,.05)',
        pop: '0 4px 10px rgba(33,29,53,.08), 0 16px 40px rgba(33,29,53,.12)',
      },
      fontFamily: {
        sans: ['-apple-system', 'BlinkMacSystemFont', '"Segoe UI"', 'Roboto', '"Helvetica Neue"', 'Arial', 'sans-serif'],
        mono: ['ui-monospace', '"Cascadia Code"', '"SF Mono"', 'Menlo', 'Consolas', 'monospace'],
      },
    },
  },
  plugins: [],
}
```

- [ ] **Step 3: Replace `web/src/index.css` with tokens base layer**

```css
@tailwind base;
@tailwind components;
@tailwind utilities;

@layer base {
  html {
    @apply bg-page text-ink antialiased;
  }
  body {
    @apply font-sans text-[13.5px] leading-normal;
  }
  /* Spec §"Shape, depth, motion": visible 2px brand focus on every interactive element. */
  :focus-visible {
    @apply outline-none ring-2 ring-brand ring-offset-2 ring-offset-page;
  }
}
```

- [ ] **Step 4: Write the failing test for `cn()`** — `web/src/ui/cn.test.ts`

```ts
import { cn } from './cn'

test('merges conditional classes and resolves Tailwind conflicts', () => {
  expect(cn('px-2', false && 'hidden', 'px-4')).toBe('px-4')
  expect(cn('text-muted', undefined, 'font-semibold')).toBe('text-muted font-semibold')
})
```

Run: `npx vitest run src/ui/cn.test.ts` — expect FAIL (`cn.ts` missing).

- [ ] **Step 5: Implement `web/src/ui/cn.ts`**

```ts
import { clsx, type ClassValue } from 'clsx'
import { twMerge } from 'tailwind-merge'

export const cn = (...inputs: ClassValue[]) => twMerge(clsx(inputs))
```

- [ ] **Step 6: Verify**

Run: `npx vitest run src/ui/cn.test.ts` — PASS. Then `npm run typecheck` and `npx vitest run` — all existing tests still pass (token config only *adds* classes).

- [ ] **Step 7: Commit**

```bash
git add package.json package-lock.json tailwind.config.js src/index.css src/ui/
git commit -m "feat(web): design tokens, base layer, cn helper (ui slice 1)"
```

---

### Task 2: Brand mark, favicon, page titles

**Files:**
- Create: `web/src/ui/Brand.tsx`
- Create: `web/public/favicon.svg`
- Create: `web/src/lib/title.ts`
- Modify: `web/index.html`
- Test: `web/src/ui/Brand.test.tsx`

- [ ] **Step 1: Write the failing test** — `web/src/ui/Brand.test.tsx`

```tsx
import { render, screen } from '@testing-library/react'
import { Brand } from './Brand'

test('renders the Janus mark and wordmark', () => {
  render(<Brand />)
  expect(screen.getByText('Janus')).toBeInTheDocument()
  expect(screen.getByRole('img', { name: /janus/i })).toBeInTheDocument()
})

test('mark-only mode omits the wordmark', () => {
  render(<Brand markOnly />)
  expect(screen.queryByText('Janus')).not.toBeInTheDocument()
})
```

Run: `npx vitest run src/ui/Brand.test.tsx` — FAIL (module missing).

- [ ] **Step 2: Implement `web/src/ui/Brand.tsx`** (SVG copied verbatim from the mockup's top bar)

```tsx
export function Brand({ markOnly = false, size = 20 }: { markOnly?: boolean; size?: number }) {
  return (
    <span className="flex items-center gap-2 text-[15px] font-bold tracking-tight text-ink">
      <svg width={size} height={size} viewBox="0 0 20 20" role="img" aria-label="Janus logo" className="text-brand">
        <path d="M10 1.5 L17.5 6 V14 L10 18.5 L2.5 14 V6 Z" fill="none" stroke="currentColor" strokeWidth="1.6" />
        <path d="M10 1.5 V18.5 M10 10 L17.5 6 M10 10 L2.5 6" stroke="currentColor" strokeWidth="1.1" opacity=".55" />
        <path d="M10 1.5 L17.5 6 V14 L10 18.5 Z" fill="currentColor" opacity=".18" />
      </svg>
      {!markOnly && <span>Janus</span>}
    </span>
  )
}
```

(No hex literals: the mark inherits the accent via `className="text-brand"` + `currentColor`, so the gate's hex-literal ban (Task 10, merged design) passes. `public/favicon.svg` keeps its hex values — it's a static asset outside `src/` and is not scanned.)

- [ ] **Step 3: Create `web/public/favicon.svg`**

```xml
<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 20 20">
  <path d="M10 1.5 L17.5 6 V14 L10 18.5 L2.5 14 V6 Z" fill="none" stroke="#6A5CF5" stroke-width="1.6"/>
  <path d="M10 1.5 V18.5 M10 10 L17.5 6 M10 10 L2.5 6" stroke="#6A5CF5" stroke-width="1.1" opacity=".55"/>
  <path d="M10 1.5 L17.5 6 V14 L10 18.5 Z" fill="#6A5CF5" opacity=".18"/>
</svg>
```

- [ ] **Step 4: Reference it from `web/index.html`** — inside `<head>`, after the viewport meta:

```html
    <link rel="icon" type="image/svg+xml" href="/favicon.svg" />
```

- [ ] **Step 5: Create `web/src/lib/title.ts`**

```ts
import { useEffect } from 'react'

// Sets "<section> · Janus" while mounted; restores the bare product name on unmount.
export function useTitle(section?: string) {
  useEffect(() => {
    document.title = section ? `${section} · Janus` : 'Janus'
    return () => {
      document.title = 'Janus'
    }
  }, [section])
}
```

(Consumed by screens in Tasks 7–9.)

- [ ] **Step 6: Verify**

Run: `npx vitest run src/ui/Brand.test.tsx` — PASS. `npm run typecheck` — clean.

- [ ] **Step 7: Commit**

```bash
git add src/ui/Brand.tsx src/ui/Brand.test.tsx public/favicon.svg index.html src/lib/title.ts
git commit -m "feat(web): brand mark, favicon, useTitle hook"
```

---

### Task 3: `Pill` primitive + environment color util

**Files:**
- Create: `web/src/ui/Pill.tsx`
- Create: `web/src/ui/env.ts`
- Test: `web/src/ui/Pill.test.tsx`, `web/src/ui/env.test.ts`

- [ ] **Step 1: Write failing tests**

`web/src/ui/Pill.test.tsx`:

```tsx
import { render, screen } from '@testing-library/react'
import { Pill } from './Pill'

test('renders children with tone classes', () => {
  render(<Pill tone="success">Unsealed</Pill>)
  const el = screen.getByText('Unsealed')
  expect(el).toHaveClass('bg-success-soft', 'text-success')
})

test('renders a status dot when asked', () => {
  const { container } = render(<Pill tone="danger" dot>Sealed</Pill>)
  expect(container.querySelector('[data-dot]')).toBeInTheDocument()
})
```

`web/src/ui/env.test.ts`:

```ts
import { envTone } from './env'

test.each([
  ['dev', 'info'],
  ['development', 'info'],
  ['staging', 'warning'],
  ['stage', 'warning'],
  ['test', 'warning'],
  ['qa', 'warning'],
  ['prod', 'danger'],
  ['production', 'danger'],
  ['custom-env', 'info'],
])('envTone(%s) → %s', (slug, tone) => {
  expect(envTone(slug)).toBe(tone)
})
```

Run: `npx vitest run src/ui` — both FAIL.

- [ ] **Step 2: Implement `web/src/ui/Pill.tsx`**

```tsx
import { ReactNode } from 'react'
import { cn } from './cn'

export type Tone = 'success' | 'warning' | 'danger' | 'info' | 'brand' | 'muted'

const tones: Record<Tone, string> = {
  success: 'bg-success-soft text-success',
  warning: 'bg-warning-soft text-warning',
  danger: 'bg-danger-soft text-danger',
  info: 'bg-info-soft text-info',
  brand: 'bg-brand-soft text-brand-deep',
  muted: 'bg-line-soft text-muted',
}

export function Pill({ tone, dot = false, className, children }: {
  tone: Tone
  dot?: boolean
  className?: string
  children: ReactNode
}) {
  return (
    <span className={cn('inline-flex items-center gap-1.5 rounded-full px-2.5 py-px text-[11.5px] font-semibold', tones[tone], className)}>
      {dot && <span data-dot className="h-1.5 w-1.5 rounded-full bg-current" />}
      {children}
    </span>
  )
}
```

- [ ] **Step 3: Implement `web/src/ui/env.ts`**

```ts
import type { Tone } from './Pill'

// Doppler-signature env coding (spec §Environment colors):
// dev=info blue · staging/test/qa=warning amber · prod=danger red · other=info.
export function envTone(slugOrName: string): Extract<Tone, 'info' | 'warning' | 'danger'> {
  const s = slugOrName.toLowerCase()
  if (/^prod/.test(s)) return 'danger'
  if (/^(stag|test|qa)/.test(s)) return 'warning'
  return 'info'
}

// For the sidebar's 7px square env dots.
export const envDotClass: Record<ReturnType<typeof envTone>, string> = {
  info: 'bg-info',
  warning: 'bg-warning',
  danger: 'bg-danger',
}
```

- [ ] **Step 4: Verify**

Run: `npx vitest run src/ui` — PASS. `npm run typecheck` — clean.

- [ ] **Step 5: Commit**

```bash
git add src/ui/Pill.tsx src/ui/Pill.test.tsx src/ui/env.ts src/ui/env.test.ts
git commit -m "feat(web): Pill primitive and env color util"
```

---

### Task 4: Breadcrumb component

**Files:**
- Create: `web/src/shell/Breadcrumb.tsx`
- Test: `web/src/shell/Breadcrumb.test.tsx`

The breadcrumb resolves names from the same query keys the Sidebar already populates (`['projects']`, `['envs', pid]`, `['configs', pid, eid]`), so on real navigation it renders from cache with no extra requests.

- [ ] **Step 1: Write the failing test** — `web/src/shell/Breadcrumb.test.tsx`

```tsx
import { http, HttpResponse } from 'msw'
import { screen } from '@testing-library/react'
import { server } from '../test/msw'
import { renderApp } from '../test/render'
import { Breadcrumb } from './Breadcrumb'

test('renders project / env / config for the active config route', async () => {
  server.use(
    http.get('/v1/projects', () => HttpResponse.json({ projects: [{ id: 'p1', slug: 'acme', name: 'acme-api' }] })),
    http.get('/v1/projects/p1/environments', () => HttpResponse.json({ environments: [{ id: 'e1', slug: 'prod', name: 'production' }] })),
    http.get('/v1/projects/p1/environments/e1/configs', () =>
      HttpResponse.json({ configs: [{ id: 'c1', environment_id: 'e1', name: 'root', inherits_from: null, created_at: '' }] })),
  )
  renderApp(<Breadcrumb />, { route: '/projects/p1/configs/c1', withAuth: false })
  expect(await screen.findByText('acme-api')).toBeInTheDocument()
  expect(await screen.findByText('production')).toBeInTheDocument()
  expect(await screen.findByText('root')).toBeInTheDocument()
})

test('renders nothing outside a project route', () => {
  const { container } = renderApp(<Breadcrumb />, { route: '/', withAuth: false })
  expect(container.querySelector('nav')).toBeNull()
})
```

Run: `npx vitest run src/shell/Breadcrumb.test.tsx` — FAIL.

- [ ] **Step 2: Implement `web/src/shell/Breadcrumb.tsx`**

```tsx
import { Fragment } from 'react'
import { useLocation, matchPath } from 'react-router-dom'
import { useQueries } from '@tanstack/react-query'
import { endpoints } from '../lib/endpoints'
import { useProjects, useEnvironments } from '../secrets/nav'

// Rendered as a sibling of <Routes> (like Sidebar), so route params come from
// matchPath on the location, not useParams.
export function Breadcrumb() {
  const location = useLocation()
  const projectId = matchPath('/projects/:projectId/*', location.pathname)?.params.projectId
  const configId = matchPath('/projects/:projectId/configs/:configId', location.pathname)?.params.configId

  const projects = useProjects()
  const envs = useEnvironments(projectId)
  // Same queryKey shape the Sidebar uses, so these hit the cache after nav.
  const configLists = useQueries({
    queries: (envs.data ?? []).map((e) => ({
      queryKey: ['configs', projectId, e.id],
      queryFn: () => endpoints.listConfigs(projectId!, e.id),
      enabled: !!projectId && !!configId,
    })),
  })

  if (!projectId) return null

  const project = projects.data?.find((p) => p.id === projectId)
  const config = configLists.flatMap((q) => q.data ?? []).find((c) => c.id === configId)
  const env = config && envs.data?.find((e) => e.id === config.environment_id)

  const parts = [
    { key: 'project', label: project?.name, strong: true },
    { key: 'env', label: env?.name, strong: false },
    { key: 'config', label: config?.name, strong: true },
  ].filter((p): p is { key: string; label: string; strong: boolean } => !!p.label)

  return (
    <nav aria-label="breadcrumb" className="flex items-center gap-1.5 text-[13px] text-muted">
      {parts.map((p, i) => (
        <Fragment key={p.key}>
          {i > 0 && <span aria-hidden className="text-line">/</span>}
          <span className={p.strong ? 'font-semibold text-ink' : undefined}>{p.label}</span>
        </Fragment>
      ))}
    </nav>
  )
}
```

- [ ] **Step 3: Verify**

Run: `npx vitest run src/shell/Breadcrumb.test.tsx` — PASS.

- [ ] **Step 4: Commit**

```bash
git add src/shell/Breadcrumb.tsx src/shell/Breadcrumb.test.tsx
git commit -m "feat(web): breadcrumb resolving project/env/config from nav cache"
```

---

### Task 5: UserMenu (Radix dropdown) + ResizeObserver test stub

**Files:**
- Create: `web/src/shell/UserMenu.tsx`
- Modify: `web/src/test/setup.ts` (ResizeObserver stub — Radix popper needs it; jsdom lacks it)
- Test: `web/src/shell/UserMenu.test.tsx`

- [ ] **Step 1: Add the ResizeObserver stub to `web/src/test/setup.ts`** (append; keep existing content)

```ts
// Radix UI popper positioning requires ResizeObserver, which jsdom lacks.
class ResizeObserverStub {
  observe() {}
  unobserve() {}
  disconnect() {}
}
globalThis.ResizeObserver ??= ResizeObserverStub as unknown as typeof ResizeObserver
```

- [ ] **Step 2: Write the failing test** — `web/src/shell/UserMenu.test.tsx`

```tsx
import { screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import { server } from '../test/msw'
import { renderApp } from '../test/render'
import { UserMenu } from './UserMenu'

function mockMe() {
  server.use(http.get('/v1/auth/me', () => HttpResponse.json({ email: 'steve@acme.dev' })))
}

test('shows initials; opens menu with email, change password, log out', async () => {
  mockMe()
  renderApp(<UserMenu />)
  const trigger = await screen.findByRole('button', { name: /user menu/i })
  expect(trigger).toHaveTextContent('ST') // first two letters of local part, uppercased
  await userEvent.click(trigger)
  expect(await screen.findByText('steve@acme.dev')).toBeInTheDocument()
  expect(screen.getByRole('menuitem', { name: /change password/i })).toBeInTheDocument()
  expect(screen.getByRole('menuitem', { name: /log out/i })).toBeInTheDocument()
})

test('change password opens the dialog', async () => {
  mockMe()
  renderApp(<UserMenu />)
  await userEvent.click(await screen.findByRole('button', { name: /user menu/i }))
  await userEvent.click(screen.getByRole('menuitem', { name: /change password/i }))
  expect(await screen.findByRole('heading', { name: /change password/i })).toBeInTheDocument()
})
```

Run: `npx vitest run src/shell/UserMenu.test.tsx` — FAIL.

- [ ] **Step 3: Implement `web/src/shell/UserMenu.tsx`**

```tsx
import { useState } from 'react'
import * as Menu from '@radix-ui/react-dropdown-menu'
import { useAuth } from '../auth/AuthProvider'
import { ChangePasswordForm } from '../auth/ChangePassword'

const item =
  'flex w-full cursor-default select-none items-center rounded px-2.5 py-1.5 text-[13px] text-ink outline-none data-[highlighted]:bg-brand-soft data-[highlighted]:text-brand-deep'

export function UserMenu() {
  const { user, logout } = useAuth()
  const [showPw, setShowPw] = useState(false)
  if (!user) return null
  const initials = user.email.slice(0, 2).toUpperCase()

  return (
    <>
      <Menu.Root>
        <Menu.Trigger
          aria-label="user menu"
          className="flex h-7 w-7 items-center justify-center rounded-full border border-brand-line bg-brand-soft text-[11px] font-bold text-brand-deep"
        >
          {initials}
        </Menu.Trigger>
        <Menu.Portal>
          <Menu.Content
            align="end"
            sideOffset={6}
            className="min-w-[210px] rounded-card border border-line bg-card p-1.5 shadow-pop"
          >
            <div className="px-2.5 pb-1.5 pt-1 text-[12px] text-faint">{user.email}</div>
            <Menu.Separator className="my-1 h-px bg-line-soft" />
            <Menu.Item className={item} onSelect={() => setShowPw(true)}>
              Change password
            </Menu.Item>
            <Menu.Item className={item} onSelect={() => void logout()}>
              Log out
            </Menu.Item>
          </Menu.Content>
        </Menu.Portal>
      </Menu.Root>
      {showPw && <ChangePasswordForm onDone={() => setShowPw(false)} onClose={() => setShowPw(false)} />}
    </>
  )
}
```

- [ ] **Step 4: Verify**

Run: `npx vitest run src/shell/UserMenu.test.tsx` — PASS. (If Radix's menu doesn't open under `userEvent.click`, the known jsdom workaround is `await userEvent.pointer({ keys: '[MouseLeft]', target: trigger })`; use it in the test rather than changing the component.)

- [ ] **Step 5: Commit**

```bash
git add src/shell/UserMenu.tsx src/shell/UserMenu.test.tsx src/test/setup.ts
git commit -m "feat(web): user menu dropdown (radix) with initials avatar"
```

---

### Task 6: TopBar + AppLayout + App wiring (real seal status)

**Files:**
- Modify: `web/src/shell/TopBar.tsx` (full rewrite)
- Modify: `web/src/shell/AppLayout.tsx`
- Modify: `web/src/App.tsx:40` (pass real seal state; restyle bare landing strings)
- Modify: `web/src/App.test.tsx` (email now lives inside the user menu)

- [ ] **Step 1: Update the app-shell test first** — in `web/src/App.test.tsx`, replace the third test with:

```tsx
test('unsealed + authenticated shows the app shell', async () => {
  boot({ initialized: true, sealed: false, type: 'shamir' }, 200)
  render(<App />)
  // Email moved into the user-menu dropdown; the shell shows the avatar + seal pill.
  expect(await screen.findByRole('button', { name: /user menu/i })).toBeInTheDocument()
  expect(screen.getByText(/unsealed/i)).toBeInTheDocument()
})
```

Run: `npx vitest run src/App.test.tsx` — the updated test FAILS (TopBar still shows raw email / "🔓 unsealed").

- [ ] **Step 2: Rewrite `web/src/shell/TopBar.tsx`**

```tsx
import { Brand } from '../ui/Brand'
import { Pill } from '../ui/Pill'
import { Breadcrumb } from './Breadcrumb'
import { UserMenu } from './UserMenu'

export function TopBar({ sealed }: { sealed: boolean }) {
  return (
    <header className="flex items-center gap-5 border-b border-line bg-card px-4 py-2">
      <Brand />
      <Breadcrumb />
      <div className="ml-auto flex items-center gap-3.5">
        {sealed ? (
          <Pill tone="danger" dot>Sealed</Pill>
        ) : (
          <Pill tone="success" dot>Unsealed</Pill>
        )}
        <UserMenu />
      </div>
    </header>
  )
}
```

- [ ] **Step 3: Update `web/src/shell/AppLayout.tsx`** — accept and forward the seal state; token surfaces:

```tsx
import { ReactNode } from 'react'
import { TopBar } from './TopBar'

export function AppLayout({ sealed, sidebar, children }: { sealed: boolean; sidebar: ReactNode; children: ReactNode }) {
  return (
    <div className="flex h-screen flex-col bg-page">
      <TopBar sealed={sealed} />
      <div className="flex min-h-0 flex-1">
        <aside className="w-60 shrink-0 overflow-y-auto border-r border-line bg-card px-2.5 py-3.5">{sidebar}</aside>
        <main className="min-w-0 flex-1 overflow-y-auto p-6">{children}</main>
      </div>
    </div>
  )
}
```

- [ ] **Step 4: Update `web/src/App.tsx`** — pass the live seal state and tokenize the two landing strings. Replace the `return (` block of `Gate` (lines 39–53) with:

```tsx
  return (
    <AppLayout sealed={seal.sealed} sidebar={<Sidebar />}>
      <Routes>
        <Route path="/" element={<div className="mt-16 text-center text-muted">Select or create a project to begin.</div>} />
        <Route path="/projects/:projectId" element={<div className="mt-16 text-center text-muted">Select a config from the sidebar.</div>} />
        <Route path="/projects/:projectId/configs/:configId" element={<SecretEditor />} />
        <Route path="/projects/:projectId/audit" element={<Placeholder feature="Audit viewer" />} />
        <Route path="/tokens" element={<Placeholder feature="Token management" />} />
        <Route path="/members" element={<Placeholder feature="Member management" />} />
        <Route path="/transit" element={<Placeholder feature="Transit UI" />} />
        <Route path="/settings" element={<Placeholder feature="Settings" />} />
        <Route path="*" element={<Navigate to="/" replace />} />
      </Routes>
    </AppLayout>
  )
```

(The richer landing hero is Slice 2; this only removes raw palette classes. `seal` is non-null past the guard on line 33.)

- [ ] **Step 5: Verify**

Run: `npx vitest run src/App.test.tsx src/shell` — PASS. `npm run typecheck` — clean.

- [ ] **Step 6: Commit**

```bash
git add src/shell/TopBar.tsx src/shell/AppLayout.tsx src/App.tsx src/App.test.tsx
git commit -m "feat(web): branded top bar with breadcrumb, live seal pill, user menu"
```

---

### Task 7: Sidebar redesign

**Files:**
- Modify: `web/src/shell/Sidebar.tsx` (full rewrite; keep all aria-labels, link targets, and form-opening behavior identical)
- Modify: `web/src/shell/Sidebar.test.tsx` (add active-state assertion)

- [ ] **Step 1: Extend the test first** — in `web/src/shell/Sidebar.test.tsx`, add to the existing test after the current assertions:

```tsx
  // Active config link is marked for styling and a11y.
  expect(screen.getByRole('link', { name: /^prod$/i })).toHaveAttribute('aria-current', 'page')
```

Run: `npx vitest run src/shell/Sidebar.test.tsx` — FAIL (`aria-current` not set yet).

- [ ] **Step 2: Rewrite `web/src/shell/Sidebar.tsx`**

```tsx
import { useState } from 'react'
import { Link, useLocation, useNavigate, matchPath } from 'react-router-dom'
import { Plus, ScrollText, KeyRound, Users, Shield, Settings } from 'lucide-react'
import { useProjects, useEnvironments, useConfigs } from '../secrets/nav'
import { CreateProjectForm, CreateEnvironmentForm, CreateConfigForm } from '../structure/CreateForms'
import { Config } from '../lib/endpoints'
import { envTone, envDotClass } from '../ui/env'
import { cn } from '../ui/cn'

// Sidebar is rendered as a sibling of <Routes> (inside AppLayout), not nested
// within a matched <Route>, so useParams() would always be empty here.
// Derive the active ids from the URL directly via matchPath instead.
function useActiveIds() {
  const location = useLocation()
  return {
    projectId: matchPath('/projects/:projectId/*', location.pathname)?.params.projectId,
    configId: matchPath('/projects/:projectId/configs/:configId', location.pathname)?.params.configId,
  }
}

type OpenForm = null | 'project' | 'env' | { config: { eid: string; bases: Config[] } }

function SectionLabel({ children, action }: { children: React.ReactNode; action?: React.ReactNode }) {
  return (
    <div className="mb-1 mt-4 flex items-center justify-between px-2 text-[10.5px] font-bold uppercase tracking-[.12em] text-faint">
      <span>{children}</span>
      {action}
    </div>
  )
}

function IconAdd({ label, onClick }: { label: string; onClick: () => void }) {
  return (
    <button
      type="button"
      onClick={onClick}
      aria-label={label}
      className="flex h-5 w-5 items-center justify-center rounded text-faint hover:bg-brand-soft hover:text-brand-deep"
    >
      <Plus size={13} strokeWidth={1.7} />
    </button>
  )
}

function EnvConfigs({ pid, eid, name, activeConfigId, onAddConfig }: {
  pid: string
  eid: string
  name: string
  activeConfigId?: string
  onAddConfig: (eid: string, bases: Config[]) => void
}) {
  const configs = useConfigs(pid, eid)
  return (
    <li className="mx-1 mt-2">
      <div className="flex items-center justify-between px-2 text-[12px] font-semibold text-muted">
        <span className="flex items-center gap-2">
          <span className={cn('h-[7px] w-[7px] rounded-[2px]', envDotClass[envTone(name)])} />
          {name}
        </span>
        <IconAdd label={`add config to ${name}`} onClick={() => onAddConfig(eid, configs.data ?? [])} />
      </div>
      <ul className="mt-0.5">
        {configs.data?.map((c) => {
          const active = c.id === activeConfigId
          return (
            <li key={c.id} className="relative ml-3.5">
              {active && <span className="absolute -left-3.5 bottom-[5px] top-[5px] w-[3px] rounded-full bg-brand" />}
              <Link
                to={`/projects/${pid}/configs/${c.id}`}
                aria-current={active ? 'page' : undefined}
                className={cn(
                  'block rounded px-2 py-1 text-[12.5px] text-muted hover:bg-line-soft',
                  active && 'bg-brand-soft font-semibold text-brand-deep hover:bg-brand-soft',
                )}
              >
                {c.name}
                {c.inherits_from && <span className="ml-1 text-[11px] text-info">↳</span>}
              </Link>
            </li>
          )
        })}
      </ul>
    </li>
  )
}

const navItem =
  'mx-1 flex items-center gap-2.5 rounded px-2 py-1.5 text-[12.5px] text-muted hover:bg-line-soft hover:text-ink'

export function Sidebar() {
  const { projectId, configId } = useActiveIds()
  const navigate = useNavigate()
  const projects = useProjects()
  const envs = useEnvironments(projectId)
  const [open, setOpen] = useState<OpenForm>(null)

  return (
    <nav className="text-sm">
      <SectionLabel action={<IconAdd label="add project" onClick={() => setOpen('project')} />}>Project</SectionLabel>
      <select
        value={projectId ?? ''}
        onChange={(e) => navigate(`/projects/${e.target.value}`)}
        aria-label="project"
        className="mx-1 w-[calc(100%-8px)] rounded border border-line bg-card px-2.5 py-1.5 text-[13px] font-semibold text-ink"
      >
        <option value="" disabled>Select a project…</option>
        {projects.data?.map((p) => <option key={p.id} value={p.id}>{p.name}</option>)}
      </select>

      {projectId && (
        <>
          <SectionLabel action={<IconAdd label="add environment" onClick={() => setOpen('env')} />}>
            Environments
          </SectionLabel>
          <ul>
            {envs.data?.map((e) => (
              <EnvConfigs
                key={e.id}
                pid={projectId}
                eid={e.id}
                name={e.name}
                activeConfigId={configId}
                onAddConfig={(eid, bases) => setOpen({ config: { eid, bases } })}
              />
            ))}
          </ul>
        </>
      )}

      <SectionLabel>Instance</SectionLabel>
      <Link to={`/projects/${projectId ?? ''}/audit`} className={navItem}><ScrollText size={15} strokeWidth={1.7} /> Audit</Link>
      <Link to="/tokens" className={navItem}><KeyRound size={15} strokeWidth={1.7} /> Tokens</Link>
      <Link to="/members" className={navItem}><Users size={15} strokeWidth={1.7} /> Members</Link>
      <Link to="/transit" className={navItem}><Shield size={15} strokeWidth={1.7} /> Transit</Link>
      <Link to="/settings" className={navItem}><Settings size={15} strokeWidth={1.7} /> Settings</Link>

      {open === 'project' && (
        <CreateProjectForm
          onCreated={(p) => { setOpen(null); navigate('/projects/' + p.id) }}
          onClose={() => setOpen(null)}
        />
      )}
      {open === 'env' && projectId && (
        <CreateEnvironmentForm pid={projectId} onCreated={() => setOpen(null)} onClose={() => setOpen(null)} />
      )}
      {open && typeof open === 'object' && projectId && (
        <CreateConfigForm
          pid={projectId}
          eid={open.config.eid}
          bases={open.config.bases}
          onCreated={(c) => { setOpen(null); navigate('/projects/' + projectId + '/configs/' + c.id) }}
          onClose={() => setOpen(null)}
        />
      )}
    </nav>
  )
}
```

Behavior preserved: same routes, same `aria-label="project"` select, same `add config to ${name}` labels, same create-form flow. The old `＋ Env` text button becomes the labeled icon button `add environment`.

- [ ] **Step 3: Verify**

Run: `npx vitest run src/shell/Sidebar.test.tsx` — PASS. Full sweep: `npx vitest run` — everything green.

- [ ] **Step 4: Commit**

```bash
git add src/shell/Sidebar.tsx src/shell/Sidebar.test.tsx
git commit -m "feat(web): sidebar redesign — sections, env colors, active rail, icons"
```

---

### Task 8: Auth & unseal screens restyle

**Files:**
- Modify: `web/src/auth/LoginPage.tsx`
- Modify: `web/src/unseal/UnsealPage.tsx`
- Modify: `web/src/auth/ChangePassword.tsx`

Existing tests (`LoginPage.test.tsx`, `UnsealPage.test.tsx`, `ChangePassword.test.tsx`) must pass unchanged — all labels, roles, button names, and status strings stay identical.

- [ ] **Step 1: Rewrite `web/src/auth/LoginPage.tsx` render** — keep the component logic (state + `submit`) exactly as-is; replace only the returned JSX:

```tsx
  return (
    <div className="flex min-h-screen items-center justify-center bg-page px-4">
      <form onSubmit={submit} aria-label="login" className="flex w-[330px] flex-col gap-3 rounded-card border border-line bg-card p-7 shadow-pop">
        <Brand />
        <div>
          <h1 className="text-[17px] font-semibold tracking-tight">Sign in to Janus</h1>
          <p className="text-[12.5px] text-muted">Self-hosted secrets manager</p>
        </div>
        <label className="flex flex-col gap-1 text-[12px] font-semibold">Email
          <input type="email" value={email} onChange={(e) => setEmail(e.target.value)} required
            className="rounded border border-line bg-card px-3 py-2 text-[13px] font-normal" />
        </label>
        <label className="flex flex-col gap-1 text-[12px] font-semibold">Password
          <input type="password" value={password} onChange={(e) => setPassword(e.target.value)} required
            className="rounded border border-line bg-card px-3 py-2 text-[13px] font-normal" />
        </label>
        {error && <p role="alert" className="text-sm text-danger">{error}</p>}
        <button type="submit" disabled={busy}
          className="rounded bg-brand p-2 text-[13px] font-semibold text-white shadow-card disabled:opacity-50">
          {busy ? 'Signing in…' : 'Sign in'}
        </button>
      </form>
    </div>
  )
```

Add the import: `import { Brand } from '../ui/Brand'` and `import { useTitle } from '../lib/title'`; call `useTitle('Sign in')` at the top of the component.

- [ ] **Step 2: Rewrite `web/src/unseal/UnsealPage.tsx` render** — keep all logic (status polling, share clearing) untouched; replace the final `return` JSX:

```tsx
  return (
    <div className="flex min-h-screen items-center justify-center bg-page px-4">
      <form onSubmit={submitShare} className="flex w-[330px] flex-col gap-3 rounded-card border border-line bg-card p-7 shadow-pop">
        <Pill tone="danger" dot className="self-start">Sealed</Pill>
        <div>
          <h1 className="text-[17px] font-semibold tracking-tight">Unseal Janus</h1>
          <p className="text-[12.5px] text-muted">
            {(status.progress ?? 0)} of {status.threshold} shares submitted
          </p>
        </div>
        <div className="flex gap-1.5" aria-hidden>
          {Array.from({ length: status.threshold ?? 0 }, (_, i) => (
            <span key={i} className={cn('h-1.5 flex-1 rounded-full', i < (status.progress ?? 0) ? 'bg-brand' : 'bg-line-soft')} />
          ))}
        </div>
        <label className="flex flex-col gap-1 text-[12px] font-semibold">Unseal key share
          <input type="password" autoComplete="off" value={share} onChange={(e) => setShare(e.target.value)} required
            className="rounded border border-line bg-card px-3 py-2 font-mono text-[13px] font-normal" />
        </label>
        {error && <p role="alert" className="text-sm text-danger">{error}</p>}
        <div className="flex gap-2">
          <button type="submit" disabled={busy}
            className="flex-1 rounded bg-brand p-2 text-[13px] font-semibold text-white shadow-card disabled:opacity-50">
            Submit share
          </button>
          <button type="button" onClick={reset}
            className="rounded border border-line bg-card px-4 py-2 text-[13px] font-semibold">
            Reset
          </button>
        </div>
        <p className="text-[11.5px] text-faint">Shares are held in memory only and never logged.</p>
      </form>
    </div>
  )
```

Add imports: `import { Pill } from '../ui/Pill'`, `import { cn } from '../ui/cn'`, `import { useTitle } from '../lib/title'`; call `useTitle('Unseal')`. Also tokenize the two early returns: `className="mt-24 text-center text-muted"`.

- [ ] **Step 3: Restyle `web/src/auth/ChangePassword.tsx`** — replace the wrapper/form classes only (logic and all strings unchanged):

- overlay div: `className="fixed inset-0 z-50 flex items-center justify-center bg-ink/30"`
- form: `className="w-80 rounded-card border border-line bg-card p-5 shadow-pop"`
- inputs: `className="rounded border border-line px-3 py-2 text-[13px] font-normal"`
- error `<p>`: `text-sm text-danger`
- Cancel button: `className="rounded border border-line bg-card px-3 py-1.5 text-[13px] font-semibold"`
- submit button: `className="rounded bg-brand px-3 py-1.5 text-[13px] font-semibold text-white disabled:opacity-50"`

- [ ] **Step 4: Verify**

Run: `npx vitest run src/auth src/unseal` — all PASS with no test edits. `npm run typecheck` — clean.

- [ ] **Step 5: Commit**

```bash
git add src/auth/LoginPage.tsx src/unseal/UnsealPage.tsx src/auth/ChangePassword.tsx
git commit -m "feat(web): branded auth and unseal cards with share progress"
```

---

### Task 9: Dialogs, placeholders, and secret editor token conversion

**Files:**
- Modify: `web/src/structure/CreateForms.tsx` (Dialog shell + buttons/inputs)
- Modify: `web/src/shell/Placeholder.tsx`
- Modify: `web/src/secrets/SecretEditor.tsx` (mechanical class conversion only — full redesign is Slice 3)

- [ ] **Step 1: `CreateForms.tsx`** — restyle `Dialog` and the repeated form controls (all three forms use identical classes; apply the same replacements in each):

`Dialog`:

```tsx
function Dialog({ title, children }: { title: string; children: ReactNode }) {
  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-ink/30">
      <div className="w-80 rounded-card border border-line bg-card p-5 shadow-pop">
        <h2 className="mb-3 text-[15px] font-semibold tracking-tight">{title}</h2>
        {children}
      </div>
    </div>
  )
}
```

In all three forms: label `className="text-[12px] font-semibold"`, inputs/selects `className="w-full rounded border border-line px-3 py-2 text-[13px] font-normal"`, error `text-sm text-danger`, Cancel `className="rounded border border-line bg-card px-3 py-1.5 text-[13px] font-semibold"`, Create `className="rounded bg-brand px-3 py-1.5 text-[13px] font-semibold text-white disabled:opacity-50"`.

- [ ] **Step 2: `Placeholder.tsx`** — tokens + title (real EmptyState comes in Slice 2):

```tsx
import { useTitle } from '../lib/title'

export function Placeholder({ feature }: { feature: string }) {
  useTitle(feature)
  return (
    <div className="mt-16 text-center">
      <p className="text-[15px] font-semibold text-muted">{feature}</p>
      <p className="text-[12.5px] text-faint">Coming in a later Phase-2 slice.</p>
    </div>
  )
}
```

- [ ] **Step 3: `SecretEditor.tsx` mechanical conversion** — exact replacements, nothing else changes (aria-labels, strings, and logic stay):

| Old | New |
|---|---|
| `badge` map values `bg-green-100 text-green-700` / `bg-blue-100 text-blue-700` / `bg-amber-100 text-amber-700` | `bg-success-soft text-success` / `bg-line-soft text-muted` / `bg-brand-soft text-brand-deep` |
| `text-sm text-gray-500` (pending summary) | `text-sm text-muted` |
| save button `rounded bg-blue-600 px-3 py-1 text-white disabled:opacity-40` | `rounded bg-brand px-3 py-1.5 text-[13px] font-semibold text-white shadow-card disabled:opacity-40` |
| `text-sm text-red-600` (save failed) | `text-sm text-danger` |
| `<table className="w-full text-sm">` | `<table className="w-full overflow-hidden rounded-card border border-line bg-card text-sm shadow-card">` |
| thead `text-left text-gray-400` | `text-left text-[10.5px] uppercase tracking-[.1em] text-faint` |
| row `border-t` | `border-t border-line-soft` |
| edit/reveal buttons `text-gray-400` | `text-faint hover:text-brand-deep` |
| remove buttons `text-red-400` | `text-danger/70 hover:text-danger` |
| version cell `text-gray-400` | `text-faint` |
| added row `bg-green-50` / `text-green-600` | `bg-success-soft/50` / `text-success` |
| AddKeyRow inputs `rounded border p-1 font-mono` | `rounded border border-line px-2.5 py-1.5 font-mono text-[12.5px]` |
| AddKeyRow button `rounded border px-2 disabled:opacity-40` | `rounded border border-line bg-card px-3 text-[13px] font-semibold disabled:opacity-40` |
| value edit input `w-full rounded border p-1` | `w-full rounded border border-line px-2.5 py-1 font-mono text-[12.5px]` |

Also add `import { useTitle } from '../lib/title'` and call `useTitle('Secrets')` at the top of `SecretEditor`.

- [ ] **Step 4: Verify**

Run: `npx vitest run` — full suite PASS (secret editor tests query by role/label/text, all preserved). `npm run typecheck` — clean.

- [ ] **Step 5: Commit**

```bash
git add src/structure/CreateForms.tsx src/shell/Placeholder.tsx src/secrets/SecretEditor.tsx
git commit -m "refactor(web): convert dialogs, placeholders, secret editor to design tokens"
```

---

### Task 10: Palette gate — no raw palette classes or hex literals ever again

*(Merged rule set from the reconciled parallel design `2026-07-06-spa-foundations-slice1-design.md`: adds the hex-literal ban, file:line reporting, scans test files too, and lives in `web/src/test/`.)*

**Files:**
- Create: `web/src/test/no-raw-palette.test.ts`

- [ ] **Step 1: Write the gate test** — `web/src/test/no-raw-palette.test.ts`

```ts
import { readdirSync, readFileSync, statSync } from 'node:fs'
import { join, dirname } from 'node:path'
import { fileURLToPath } from 'node:url'

// Enforces spec hard rule #1 (docs/superpowers/specs/2026-07-06-ui-visual-design.md):
// (a) no raw Tailwind palette classes — components use theme tokens;
// (b) no hex color literals in src — token hexes live in tailwind.config.js,
//     static assets in public/ are not scanned.
// Scans every .ts/.tsx/.css under web/src including tests; the only exclusion
// is this file itself (its regexes would self-match).
const SELF = fileURLToPath(import.meta.url)
const SRC = join(dirname(SELF), '..')

const RAW_PALETTE =
  /\b(?:bg|text|border|ring|ring-offset|fill|stroke|divide|from|via|to|placeholder|accent|caret|decoration|outline|shadow)-(?:gray|slate|zinc|neutral|stone|red|orange|amber|yellow|lime|green|emerald|teal|cyan|sky|blue|indigo|violet|purple|fuchsia|pink|rose)-\d{2,3}(?:\/\d+)?\b/
const HEX_LITERAL = /#[0-9a-fA-F]{3,8}\b/

function walk(dir: string): string[] {
  return readdirSync(dir).flatMap((name) => {
    const p = join(dir, name)
    return statSync(p).isDirectory() ? walk(p) : [p]
  })
}

test('no raw Tailwind palette classes or hex literals in web/src (use theme tokens)', () => {
  const files = walk(SRC).filter((f) => /\.(ts|tsx|css)$/.test(f) && f !== SELF)
  const offenders: string[] = []
  for (const f of files) {
    readFileSync(f, 'utf8').split('\n').forEach((line, i) => {
      const raw = line.match(RAW_PALETTE)
      if (raw) offenders.push(`${f}:${i + 1} ${raw[0]}`)
      const hex = line.match(HEX_LITERAL)
      if (hex) offenders.push(`${f}:${i + 1} hex literal ${hex[0]}`)
    })
  }
  expect(offenders).toEqual([])
})
```

- [ ] **Step 2: Run it**

Run: `npx vitest run src/test/no-raw-palette.test.ts`
Expected: PASS — Tasks 6–9 removed every raw class and Task 2's Brand mark uses `currentColor` (no hex). If it fails, the message lists `file:line` + the offending match; fix those files with token equivalents from the spec's tables, do not touch the test.

- [ ] **Step 3: Commit**

```bash
git add src/test/no-raw-palette.test.ts
git commit -m "test(web): gate forbidding raw palette classes and hex literals in src"
```

---

### Task 11: Full verification + tracker updates

**Files:**
- Modify: `fe-improvements.md` (check off shipped items)

- [ ] **Step 1: Full web gates** (in `web/`)

```bash
npm run typecheck && npx vitest run && npm run build
```

Expected: typecheck clean, all tests pass, Vite build succeeds.

- [ ] **Step 2: Go embed still builds** (repo root)

```bash
go build ./...
```

Expected: success (embedded assets rebuilt by `npm run build` land where `go:embed` expects — same as every prior milestone).

- [ ] **Step 3: Check off the shipped items in `fe-improvements.md`**

Mark `[x]` exactly these boxes: §0 color palette, §0 typography scale, §0 surfaces & depth, §0 spacing & radius rhythm; §1 brand mark + favicon/titles, §1 top bar redesign, §1 sidebar redesign. Also change the Slice 1 rollout entry's "**→ PLANNED:**" marker to "**→ SHIPPED:**". (The shadcn-lean decision is already recorded under §0.) Leave everything else unchecked (env *tabs* remain P1 — the sidebar env color dots shipped, the tab strip in the editor header is Slice 3).

- [ ] **Step 4: Commit**

```bash
git add fe-improvements.md
git commit -m "docs(fe): check off slice-1 foundations + shell items"
```

- [ ] **Step 5: Manual visual check** (human or `verify` skill): `make dev`, open http://127.0.0.1:8210 — compare login, unseal, shell, and editor against `docs/design/ui-mockup.html` side by side.

---

## Out of scope for this slice (do not build)

- Environment tab strip in the editor header, dirty-bar redesign, table hover actions, copy-to-clipboard, toasts, skeletons → Slice 3
- Landing hero / EmptyState component → Slice 2
- Dark mode (P1 — tokens make it cheap later), collapsible sidebar, motion polish
- shadcn Dialog/Toast/Tooltip adoption → Slice 3 (only DropdownMenu ships now)
