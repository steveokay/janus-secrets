# R1 — Dual-Theme Foundation Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Give the Janus SPA a working light/dark theme toggle across the entire existing app, with zero visual regression in light mode.

**Architecture:** Convert the hardcoded-hex Tailwind color tokens to CSS variables defined in a new `web/src/theme.css` (`:root` = light, `html.dark` = dark). Tailwind tokens point at the variables, so every component that already uses semantic classes (`bg-page`, `text-ink`, …) themes automatically. A `ThemeProvider` toggles `class="dark"` on `<html>`, persists to `localStorage`, and defaults to the OS preference; a no-flash inline script in `index.html` applies it before first paint.

**Tech Stack:** React 18 + TypeScript + Vite + Tailwind v3 (CSS variables) + Radix DropdownMenu + Vitest + puppeteer-core smoke.

**Spec:** `docs/superpowers/specs/2026-07-07-dark-redesign-design.md` (see "Theming architecture", token tables, hard rules).

**Branch:** create `milestone-18-r1-dual-theme` off `main` before Task 1.

---

## File structure

- Create `web/src/theme.css` — the ONLY file holding token hex values; defines all CSS variables for both themes.
- Create `web/src/theme/ThemeProvider.tsx` — theme state (context, localStorage, system-preference, applies `.dark`).
- Create `web/src/theme/ThemeProvider.test.tsx` — unit tests for the provider + toggle.
- Modify `web/tailwind.config.js` — map color + shadow tokens to `var(--…)`; add `sidebar`, `topbar`, `elevated`, `brand.text`.
- Modify `web/src/main.tsx` — import `theme.css`; wrap `<App/>` in `<ThemeProvider>`.
- Modify `web/index.html` — add the no-flash inline script in `<head>`.
- Modify `web/src/shell/UserMenu.tsx` — add a Theme radio group (Light / Dark / System).
- Modify `web/src/test/no-raw-palette.test.ts` — allow hex ONLY in `theme.css`.
- Modify `web/scripts/smoke.mjs` — add a dark-theme pass.
- Modify `CLAUDE.md` + `fe-improvements.md` — repoint the visual-system authority to the redesign spec/mockup.

---

### Task 1: CSS-variable token plumbing (light unchanged)

Migrate the token source from hardcoded hex in `tailwind.config.js` to CSS variables in a new `theme.css`, and point Tailwind at the variables. Light values stay byte-identical.

**Files:**
- Create: `web/src/theme.css`
- Modify: `web/tailwind.config.js`
- Modify: `web/src/main.tsx:4` (add the `theme.css` import)
- Modify: `web/src/test/no-raw-palette.test.ts`

- [ ] **Step 1: Create `web/src/theme.css`** with both theme value sets. Every hex from the spec's token tables lives here and NOWHERE else.

```css
/* The ONLY file that may contain raw color hex values (enforced by
   web/src/test/no-raw-palette.test.ts). Token ROLES are consumed everywhere
   else via Tailwind classes that resolve to these variables.
   Light = the approved 2026-07-06 system (unchanged). Dark = 2026-07-07 redesign. */

:root {
  --page: #F6F6FA;
  --sidebar: #FFFFFF;
  --topbar: #FFFFFF;
  --card: #FFFFFF;
  --elevated: #FFFFFF;
  --border: #E5E3F0;
  --border-soft: #EEECF6;
  --ink: #211D35;
  --muted: #6E6A85;
  --faint: #9B97B0;
  --brand: #6A5CF5;
  --brand-deep: #5546E0;
  --brand-text: #5546E0;
  --brand-soft: #EFECFE;
  --brand-line: #D8D2FB;
  --ok: #178A50;      --ok-soft: #E4F5EC;
  --warn: #B45309;    --warn-soft: #FCF0DF;
  --danger: #C92A2A;  --danger-soft: #FBE9E9;
  --info: #2563EB;    --info-soft: #E7EFFD;
  --shadow-card: 0 1px 2px rgba(33,29,53,.05), 0 4px 16px rgba(33,29,53,.05);
  --shadow-pop: 0 4px 10px rgba(33,29,53,.08), 0 16px 40px rgba(33,29,53,.12);
}

html.dark {
  --page: #0B0B10;
  --sidebar: #08080C;
  --topbar: #08080C;
  --card: #15151C;
  --elevated: #1B1B24;
  --border: #26262F;
  --border-soft: #1E1E27;
  --ink: #ECECF2;
  --muted: #9C9AAB;
  --faint: #6C6A7C;
  --brand: #6A5CF5;
  --brand-deep: #5546E0;
  --brand-text: #A79CFF;
  --brand-soft: rgba(106,92,245,0.16);
  --brand-line: rgba(106,92,245,0.30);
  --ok: #3FBE7A;      --ok-soft: rgba(23,138,80,0.16);
  --warn: #E0A253;    --warn-soft: rgba(180,83,9,0.18);
  --danger: #F0685F;  --danger-soft: rgba(201,42,42,0.18);
  --info: #5B8DEF;    --info-soft: rgba(37,99,235,0.18);
  --shadow-card: 0 1px 0 rgba(255,255,255,0.03);
  --shadow-pop: 0 1px 0 rgba(255,255,255,0.04), 0 18px 50px rgba(0,0,0,0.55);
}
```

- [ ] **Step 2: Point Tailwind at the variables** — replace the `colors` and `boxShadow` blocks in `web/tailwind.config.js`. Keep every EXISTING token key (so components don't change) and add `sidebar`, `topbar`, `elevated`, and `brand.text`.

```js
/** @type {import('tailwindcss').Config} */
export default {
  content: ['./index.html', './src/**/*.{ts,tsx}'],
  theme: {
    extend: {
      colors: {
        page: 'var(--page)',
        sidebar: 'var(--sidebar)',
        topbar: 'var(--topbar)',
        card: 'var(--card)',
        elevated: 'var(--elevated)',
        line: { DEFAULT: 'var(--border)', soft: 'var(--border-soft)' },
        ink: 'var(--ink)',
        muted: 'var(--muted)',
        faint: 'var(--faint)',
        brand: {
          DEFAULT: 'var(--brand)',
          deep: 'var(--brand-deep)',
          text: 'var(--brand-text)',
          soft: 'var(--brand-soft)',
          line: 'var(--brand-line)',
        },
        success: { DEFAULT: 'var(--ok)', soft: 'var(--ok-soft)' },
        warning: { DEFAULT: 'var(--warn)', soft: 'var(--warn-soft)' },
        danger: { DEFAULT: 'var(--danger)', soft: 'var(--danger-soft)' },
        info: { DEFAULT: 'var(--info)', soft: 'var(--info-soft)' },
      },
      borderRadius: {
        DEFAULT: '8px',
        card: '10px',
      },
      boxShadow: {
        card: 'var(--shadow-card)',
        pop: 'var(--shadow-pop)',
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

- [ ] **Step 3: Import `theme.css` first** in `web/src/main.tsx`. Change line 4 area so the variables load before Tailwind utilities consume them:

```tsx
import React from 'react'
import ReactDOM from 'react-dom/client'
import App from './App'
import './theme.css'
import './index.css'
```

- [ ] **Step 4: Update the palette gate** to allow hex ONLY in `theme.css`. In `web/src/test/no-raw-palette.test.ts`, add a constant for the theme file and gate the hex push on it (keep palette-class scanning for ALL files, keep the SELF exclusion):

```ts
const SELF = fileURLToPath(import.meta.url)
const SRC = join(dirname(SELF), '..')
const THEME_CSS = join(SRC, 'theme.css') // the sole legitimate home for token hex values
```

and inside the `forEach`, change the hex branch to:

```ts
      const hex = line.match(HEX_LITERAL)
      if (hex && f !== THEME_CSS) offenders.push(`${f}:${i + 1} hex literal ${hex[0]}`)
```

Also update the comment block at the top of the file to reference the new spec and note `theme.css` is the exception.

- [ ] **Step 5: Run the gate + full suite + build to prove light is unchanged**

Run (from `web/`): `npx vitest run src/test/no-raw-palette.test.ts && npx vitest run && npm run typecheck && npm run build`
Expected: palette gate PASSES (no offenders — theme.css hex is now allowed), all existing tests PASS (unchanged), typecheck clean, build succeeds. No visual token changed in light, so nothing should break.

- [ ] **Step 6: Commit**

```bash
git add web/src/theme.css web/tailwind.config.js web/src/main.tsx web/src/test/no-raw-palette.test.ts
git commit -m "refactor(web): tokens as CSS variables; add dark value set (light unchanged)"
```

---

### Task 2: ThemeProvider

State container that resolves + applies the theme, persists to `localStorage`, and tracks the OS preference in `system` mode.

**Files:**
- Create: `web/src/theme/ThemeProvider.tsx`
- Test: `web/src/theme/ThemeProvider.test.tsx`

- [ ] **Step 1: Write the failing test** `web/src/theme/ThemeProvider.test.tsx`:

```tsx
import { render, screen, act } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { beforeEach, expect, test, vi } from 'vitest'
import { ThemeProvider, useTheme } from './ThemeProvider'

function Probe() {
  const { theme, resolved, setTheme } = useTheme()
  return (
    <div>
      <span data-testid="theme">{theme}</span>
      <span data-testid="resolved">{resolved}</span>
      <button onClick={() => setTheme('dark')}>dark</button>
      <button onClick={() => setTheme('light')}>light</button>
      <button onClick={() => setTheme('system')}>system</button>
    </div>
  )
}

beforeEach(() => {
  localStorage.clear()
  document.documentElement.classList.remove('dark')
  // jsdom: default matchMedia to "light" unless a test overrides it
  window.matchMedia = vi.fn().mockImplementation((q: string) => ({
    matches: false, media: q, onchange: null,
    addEventListener: vi.fn(), removeEventListener: vi.fn(),
    addListener: vi.fn(), removeListener: vi.fn(), dispatchEvent: vi.fn(),
  })) as unknown as typeof window.matchMedia
})

test('defaults to system and applies light when OS prefers light', () => {
  render(<ThemeProvider><Probe /></ThemeProvider>)
  expect(screen.getByTestId('theme').textContent).toBe('system')
  expect(screen.getByTestId('resolved').textContent).toBe('light')
  expect(document.documentElement.classList.contains('dark')).toBe(false)
})

test('setTheme("dark") adds the dark class and persists', async () => {
  render(<ThemeProvider><Probe /></ThemeProvider>)
  await userEvent.click(screen.getByText('dark'))
  expect(document.documentElement.classList.contains('dark')).toBe(true)
  expect(localStorage.getItem('janus.theme')).toBe('dark')
  expect(screen.getByTestId('resolved').textContent).toBe('dark')
})

test('setTheme("light") removes the dark class and persists', async () => {
  localStorage.setItem('janus.theme', 'dark')
  render(<ThemeProvider><Probe /></ThemeProvider>)
  expect(document.documentElement.classList.contains('dark')).toBe(true)
  await userEvent.click(screen.getByText('light'))
  expect(document.documentElement.classList.contains('dark')).toBe(false)
  expect(localStorage.getItem('janus.theme')).toBe('light')
})

test('system mode follows matchMedia = dark', () => {
  window.matchMedia = vi.fn().mockImplementation((q: string) => ({
    matches: true, media: q, onchange: null,
    addEventListener: vi.fn(), removeEventListener: vi.fn(),
    addListener: vi.fn(), removeListener: vi.fn(), dispatchEvent: vi.fn(),
  })) as unknown as typeof window.matchMedia
  render(<ThemeProvider><Probe /></ThemeProvider>)
  expect(screen.getByTestId('resolved').textContent).toBe('dark')
  expect(document.documentElement.classList.contains('dark')).toBe(true)
})
```

- [ ] **Step 2: Run it to verify it fails**

Run (from `web/`): `npx vitest run src/theme/ThemeProvider.test.tsx`
Expected: FAIL — "Failed to resolve import './ThemeProvider'".

- [ ] **Step 3: Implement `web/src/theme/ThemeProvider.tsx`**

```tsx
import { createContext, useCallback, useContext, useEffect, useMemo, useState } from 'react'

export type Theme = 'light' | 'dark' | 'system'
type Resolved = 'light' | 'dark'

const KEY = 'janus.theme'
const MQ = '(prefers-color-scheme: dark)'

function readStored(): Theme {
  try {
    const v = localStorage.getItem(KEY)
    if (v === 'light' || v === 'dark' || v === 'system') return v
  } catch {
    /* localStorage unavailable — fall through to default */
  }
  return 'system'
}

function systemDark(): boolean {
  // Guarded: jsdom and older environments lack matchMedia — treat as light.
  return typeof window !== 'undefined' &&
    typeof window.matchMedia === 'function' &&
    window.matchMedia(MQ).matches
}

function resolve(theme: Theme): Resolved {
  if (theme === 'system') return systemDark() ? 'dark' : 'light'
  return theme
}

function applyClass(resolved: Resolved): void {
  document.documentElement.classList.toggle('dark', resolved === 'dark')
}

interface ThemeCtx {
  theme: Theme
  resolved: Resolved
  setTheme: (t: Theme) => void
}

const Ctx = createContext<ThemeCtx | null>(null)

export function ThemeProvider({ children }: { children: React.ReactNode }) {
  const [theme, setThemeState] = useState<Theme>(() => readStored())
  const [resolved, setResolved] = useState<Resolved>(() => resolve(theme))

  // Apply on mount + whenever `theme` changes.
  useEffect(() => {
    const r = resolve(theme)
    setResolved(r)
    applyClass(r)
  }, [theme])

  // While in system mode, follow OS changes live.
  useEffect(() => {
    if (theme !== 'system' || typeof window.matchMedia !== 'function') return
    const mql = window.matchMedia(MQ)
    const onChange = () => {
      const r: Resolved = mql.matches ? 'dark' : 'light'
      setResolved(r)
      applyClass(r)
    }
    mql.addEventListener('change', onChange)
    return () => mql.removeEventListener('change', onChange)
  }, [theme])

  const setTheme = useCallback((t: Theme) => {
    try {
      localStorage.setItem(KEY, t)
    } catch {
      /* ignore persistence failure */
    }
    setThemeState(t)
  }, [])

  const value = useMemo(() => ({ theme, resolved, setTheme }), [theme, resolved, setTheme])
  return <Ctx.Provider value={value}>{children}</Ctx.Provider>
}

export function useTheme(): ThemeCtx {
  const v = useContext(Ctx)
  if (!v) throw new Error('useTheme must be used within ThemeProvider')
  return v
}
```

- [ ] **Step 4: Run the test to verify it passes**

Run (from `web/`): `npx vitest run src/theme/ThemeProvider.test.tsx`
Expected: PASS (4/4).

- [ ] **Step 5: Commit**

```bash
git add web/src/theme/ThemeProvider.tsx web/src/theme/ThemeProvider.test.tsx
git commit -m "feat(web): ThemeProvider — light/dark/system with persistence"
```

---

### Task 3: Wire ThemeProvider + no-flash script

Mount the provider at the app root and prevent a light-flash on load for dark users.

**Files:**
- Modify: `web/src/main.tsx`
- Modify: `web/index.html`

- [ ] **Step 1: Wrap `<App/>`** in `web/src/main.tsx`:

```tsx
import React from 'react'
import ReactDOM from 'react-dom/client'
import App from './App'
import { ThemeProvider } from './theme/ThemeProvider'
import './theme.css'
import './index.css'

ReactDOM.createRoot(document.getElementById('root')!).render(
  <React.StrictMode>
    <ThemeProvider>
      <App />
    </ThemeProvider>
  </React.StrictMode>,
)
```

- [ ] **Step 2: Add the no-flash script** to `web/index.html` `<head>`, before the module script. It must run synchronously before paint and mirror `ThemeProvider`'s resolution:

```html
  <head>
    <meta charset="UTF-8" />
    <meta name="viewport" content="width=device-width, initial-scale=1.0" />
    <link rel="icon" type="image/svg+xml" href="/favicon.svg" />
    <title>Janus</title>
    <script>
      // No-flash theme: apply html.dark before first paint. Mirrors
      // web/src/theme/ThemeProvider.tsx (key 'janus.theme', default 'system').
      (function () {
        try {
          var t = localStorage.getItem('janus.theme');
          var dark = t === 'dark' ||
            ((t === 'system' || !t) && window.matchMedia('(prefers-color-scheme: dark)').matches);
          if (dark) document.documentElement.classList.add('dark');
        } catch (e) { /* ignore */ }
      })();
    </script>
  </head>
```

- [ ] **Step 3: Verify the app still builds and mounts**

Run (from `web/`): `npm run typecheck && npx vitest run && npm run build`
Expected: typecheck clean, all tests pass, build succeeds.

- [ ] **Step 4: Commit**

```bash
git add web/src/main.tsx web/index.html
git commit -m "feat(web): mount ThemeProvider + no-flash theme script"
```

---

### Task 4: Theme toggle in the user menu

Add a three-way Theme control (Light / Dark / System) to the avatar dropdown, driven by `useTheme`.

**Files:**
- Modify: `web/src/test/render.tsx` (wrap `renderApp` in `ThemeProvider` — fixes every transitive `useTheme` consumer at once)
- Modify: `web/src/shell/UserMenu.tsx`
- Test: `web/src/shell/UserMenu.test.tsx` (extend existing)

Context: the shared `renderApp` helper (`web/src/test/render.tsx`) wraps QueryClient + MemoryRouter + AuthProvider but NOT ThemeProvider. `UserMenu.test.tsx` renders via `renderApp(<UserMenu />)` and mocks `/v1/auth/me`. Once `UserMenu` calls `useTheme`, that render (and any test that renders the shell) throws without a provider — so add `ThemeProvider` to `renderApp` itself.

- [ ] **Step 1: Wrap `renderApp` in `ThemeProvider`** — `web/src/test/render.tsx`. Add the import and wrap the tree inside `QueryClientProvider`:

```tsx
import { ThemeProvider } from '../theme/ThemeProvider'
```

and change the returned tree to:

```tsx
  return render(
    <QueryClientProvider client={qc}>
      <ThemeProvider>
        <MemoryRouter initialEntries={[route]}>
          {wrap(
            <Routes>
              <Route path={pattern} element={ui} />
            </Routes>,
          )}
        </MemoryRouter>
      </ThemeProvider>
    </QueryClientProvider>,
  )
```

(jsdom lacks `matchMedia`; the guarded `systemDark()` from Task 2 makes `ThemeProvider` default to light here without throwing, so existing tests keep passing.)

- [ ] **Step 2: Add a failing test** to `web/src/shell/UserMenu.test.tsx` (it already imports `renderApp`, `screen`, `userEvent`, and has a `mockMe()` helper — reuse them):

```tsx
test('theme radio group switches to dark', async () => {
  mockMe()
  document.documentElement.classList.remove('dark')
  renderApp(<UserMenu />)
  await userEvent.click(await screen.findByRole('button', { name: /user menu/i }))
  await userEvent.click(screen.getByRole('menuitemradio', { name: 'Dark' }))
  expect(document.documentElement.classList.contains('dark')).toBe(true)
})
```

Run (from `web/`): `npx vitest run src/shell/UserMenu.test.tsx`
Expected: FAIL — no `menuitemradio` named "Dark" (the toggle isn't built yet). The two existing UserMenu tests should still PASS (proving the `renderApp` ThemeProvider wrap didn't break them).

- [ ] **Step 3: Implement the toggle** in `web/src/shell/UserMenu.tsx`. Add the `useTheme` hook and a `Menu.RadioGroup` with three `Menu.RadioItem`s, plus a separator and a section label. Full file:

```tsx
import { useState } from 'react'
import * as Menu from '@radix-ui/react-dropdown-menu'
import { useAuth } from '../auth/AuthProvider'
import { ChangePasswordForm } from '../auth/ChangePassword'
import { useTheme, type Theme } from '../theme/ThemeProvider'

const item =
  'flex w-full cursor-default select-none items-center rounded px-2.5 py-1.5 text-[13px] text-ink outline-none data-[highlighted]:bg-brand-soft data-[highlighted]:text-brand-deep'
const radio =
  'relative flex w-full cursor-default select-none items-center rounded py-1.5 pl-7 pr-2.5 text-[13px] text-ink outline-none data-[highlighted]:bg-brand-soft data-[highlighted]:text-brand-deep'

const THEMES: { value: Theme; label: string }[] = [
  { value: 'light', label: 'Light' },
  { value: 'dark', label: 'Dark' },
  { value: 'system', label: 'System' },
]

export function UserMenu() {
  const { user, logout } = useAuth()
  const { theme, setTheme } = useTheme()
  const [showPw, setShowPw] = useState(false)
  if (!user) return null
  const initials = (user.name.split('@')[0] || user.name).slice(0, 2).toUpperCase()

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
            <div className="px-2.5 pb-1.5 pt-1 text-[12px] text-faint">{user.name}</div>
            <Menu.Separator className="my-1 h-px bg-line-soft" />
            <div className="px-2.5 pb-1 pt-1 text-[10.5px] font-bold uppercase tracking-[.12em] text-faint">
              Theme
            </div>
            <Menu.RadioGroup value={theme} onValueChange={(v) => setTheme(v as Theme)}>
              {THEMES.map((t) => (
                <Menu.RadioItem key={t.value} value={t.value} className={radio}>
                  <Menu.ItemIndicator className="absolute left-2.5 text-brand-deep">•</Menu.ItemIndicator>
                  {t.label}
                </Menu.RadioItem>
              ))}
            </Menu.RadioGroup>
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

- [ ] **Step 4: Run the UserMenu tests to verify pass**

Run (from `web/`): `npx vitest run src/shell/UserMenu.test.tsx`
Expected: PASS (the two existing tests + the new dark-radio test).

- [ ] **Step 5: Run the full suite**

Run (from `web/`): `npx vitest run && npm run typecheck`
Expected: all green. Because `renderApp` now provides `ThemeProvider` (Task 4 Step 1), any test reaching `UserMenu` through `AppLayout`/`TopBar` is already covered. If a test renders a `useTheme` consumer WITHOUT `renderApp` (raw `render()`), wrap it in `<ThemeProvider>` — grep `src` for `render(` in `.test.tsx` files to confirm none are left unwrapped.

- [ ] **Step 6: Commit**

```bash
git add web/src/shell/UserMenu.tsx web/src/shell/UserMenu.test.tsx web/src/test/render.tsx
git commit -m "feat(web): theme toggle (light/dark/system) in user menu"
```

---

### Task 5: Smoke test both themes

Extend the real-browser smoke so a regression that only breaks dark is caught.

**Files:**
- Modify: `web/scripts/smoke.mjs`

- [ ] **Step 1: Add a dark pass** after the existing light assertion in `web/scripts/smoke.mjs`. Replace the tail of the file (from the `page.goto` line through the end) with:

```js
await page.goto(BASE + '/', { waitUntil: 'networkidle0', timeout: 20000 })
await new Promise((r) => setTimeout(r, 800))
const html = await page.evaluate(() => document.getElementById('root')?.innerHTML ?? '')

// Dark pass: seed the stored theme, reload, and confirm html.dark applied +
// the shell still renders. Request interception + fixtures persist across reload.
await page.evaluateOnNewDocument(() => localStorage.setItem('janus.theme', 'dark'))
await page.reload({ waitUntil: 'networkidle0', timeout: 20000 })
await new Promise((r) => setTimeout(r, 800))
const darkOn = await page.evaluate(() => document.documentElement.classList.contains('dark'))
const darkHtml = await page.evaluate(() => document.getElementById('root')?.innerHTML ?? '')
const darkBg = await page.evaluate(() =>
  getComputedStyle(document.documentElement).backgroundColor,
)

await browser.close()

const lightOk = errors.length === 0 && html.length >= 500 && html.includes('Janus')
const darkOk = darkOn && darkHtml.length >= 500 && darkHtml.includes('Janus')
if (!lightOk || !darkOk) {
  console.error('SMOKE FAILED', JSON.stringify(
    { errors, rootLength: html.length, darkOn, darkRootLength: darkHtml.length, darkBg }, null, 2))
  process.exit(1)
}
console.log(`smoke ok — light (${html.length} chars) + dark (${darkHtml.length} chars, bg ${darkBg})`)
```

(Leave the top of the file — imports, `exe` detection, `fixtures`, `launch`, request interception — unchanged.)

- [ ] **Step 2: Verify smoke passes in both themes**

Rebuild + restart the dev container so the served bundle includes R1, then run smoke:
```bash
docker compose up -d --build && ./scripts/dev-unseal.sh
cd web && npm run smoke
```
Expected: `smoke ok — light (…) + dark (…, bg rgb(11, 11, 16))` — dark bg is the `#0B0B10` page color, proving the class + variables took effect end-to-end in a real browser.

- [ ] **Step 3: Commit**

```bash
git add web/scripts/smoke.mjs
git commit -m "test(web): smoke asserts both light and dark themes render"
```

---

### Task 6: Repoint the visual-system authority

Bind every future agent to the redesign spec/mockup and record R1 in the tracker.

**Files:**
- Modify: `CLAUDE.md` (the "Web UI visual system (locked)" section)
- Modify: `fe-improvements.md`
- Modify: `docs/superpowers/specs/2026-07-06-ui-visual-design.md` (superseded banner)

- [ ] **Step 1: Update `CLAUDE.md`.** In the "Web UI visual system (locked)" section, change the mockup pointer to `docs/design/ui-redesign-mockup.html` and the spec pointer to `docs/superpowers/specs/2026-07-07-dark-redesign-design.md`, and add two rules to the bullet list:
  - "The theme is dual (light + dark) via CSS variables in `web/src/theme.css`; components use token classes only — never `dark:` palette variants, never raw hex (hex lives solely in `theme.css`)."
  - "Every UI change must render correctly in BOTH themes."
Keep the existing rules (one accent, mono-for-secrets, env coding) intact.

- [ ] **Step 2: Add a superseded banner** to the TOP of `docs/superpowers/specs/2026-07-06-ui-visual-design.md`:

```markdown
> **SUPERSEDED (2026-07-07)** by `2026-07-07-dark-redesign-design.md` (dark-first
> + light toggle via CSS-variable tokens). Token roles, mono-for-secrets, env
> coding, and security invariants below still hold; the light-only theme model
> and `ui-mockup.html` no longer do. Kept for historical record.
```

- [ ] **Step 3: Add a redesign section to `fe-improvements.md`** near the top (after the intro), tracking the slices and marking dark mode delivered:

```markdown
## Dark redesign (2026-07-07 — in progress)

Canonical: `docs/design/ui-redesign-mockup.html` + spec
`docs/superpowers/specs/2026-07-07-dark-redesign-design.md`. Slices:
- [x] **R1** dual-theme foundation (CSS-var tokens, ThemeProvider, toggle, dark palette) — delivers the long-deferred dark-mode P1 item.
- [ ] **R2** shell & ⌘K command palette
- [ ] **R3** projects list & env-columns project board & create-project modal
- [ ] **R4** screen polish pass (editor, version history, audit, tokens, members, auth/unseal)
```

- [ ] **Step 4: Commit**

```bash
git add CLAUDE.md fe-improvements.md docs/superpowers/specs/2026-07-06-ui-visual-design.md
git commit -m "docs: repoint visual-system authority to dark redesign; track R1"
```

---

## Final gate (after all tasks)

Run (from `web/`): `npm run typecheck && npx vitest run && npm run build && npm run smoke`, and from repo root `go build ./...`.
Expected: typecheck clean; all vitest green (incl. the palette gate with the `theme.css` exception and the new ThemeProvider/UserMenu tests); build succeeds; smoke reports BOTH themes; Go builds (no Go changes — sanity). Then hand off to `superpowers:finishing-a-development-branch` (PR + merge per standing orders), rebuild the dev container, and update memory.

**Exit criteria:** a user can toggle Light/Dark/System from the avatar menu; the choice persists across reloads with no light-flash; every existing screen renders correctly in both themes; light mode is visually identical to before; the palette gate still forbids hex everywhere except `theme.css`.
