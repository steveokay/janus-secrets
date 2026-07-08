# §6 Auth / Unseal (branded) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: superpowers:subagent-driven-development. Steps use checkbox (`- [ ]`) syntax.

**Goal:** Re-skin login + unseal onto the mockup §07 branded centered `AuthCard`, using the new kit. Frontend-only, behavior-preserving.

**Tech Stack:** React 18 + TS, Tailwind tokens, new kit (`Button`/`Input`), `Brand` (markOnly), vitest + @testing-library.

**Spec:** `docs/superpowers/specs/2026-07-08-auth-unseal-design.md`. Rules: token classes only (`no-raw-palette` gate); both themes; run npm from `web/`. Preserve all existing behavior + security (shares memory-only, cleared before await).

---

## Task 1: `AuthCard` shell

**Files:** Create `web/src/auth/AuthCard.tsx`, `web/src/auth/AuthCard.test.tsx`

- [ ] **Step 1: Failing test**
```tsx
import { render, screen } from '@testing-library/react'
import { AuthCard } from './AuthCard'

test('renders the Janus mark and its children', () => {
  render(<AuthCard><h1>Sign in</h1></AuthCard>)
  expect(screen.getByRole('heading', { name: 'Sign in' })).toBeInTheDocument()
  expect(screen.getByRole('img', { name: /janus logo/i })).toBeInTheDocument() // Brand mark
})
```

- [ ] **Step 2: Run → FAIL** `cd web && npx vitest run src/auth/AuthCard.test.tsx`

- [ ] **Step 3: Implement** `web/src/auth/AuthCard.tsx`
```tsx
import type { ReactNode } from 'react'
import { Brand } from '../ui/Brand'

// Centered, branded card on the page background — the shell for login + unseal
// (mockup §07). Behavior lives in the composing screens; this is presentation only.
export function AuthCard({ children }: { children: ReactNode }) {
  return (
    <div className="flex min-h-screen items-center justify-center bg-page px-4">
      <div className="w-[340px] max-w-full rounded-[14px] border border-line bg-card p-7 text-center shadow-card">
        <div className="mx-auto mb-4 flex h-11 w-11 items-center justify-center rounded-xl border border-brand-line bg-brand-soft">
          <Brand markOnly size={24} />
        </div>
        {children}
      </div>
    </div>
  )
}
```

- [ ] **Step 4: Run → PASS**, then `npm run typecheck && npx vitest run src/test/no-raw-palette.test.ts`
- [ ] **Step 5: Commit** `feat(web): AuthCard branded shell (mockup §07)`

---

## Task 2: Re-skin `LoginPage`

**Files:** Modify `web/src/auth/LoginPage.tsx`, `web/src/auth/LoginPage.test.tsx`

Current behavior to PRESERVE: `submit` calls `endpoints.login(email, password)` then `refresh()`; on `ApiError` 429 → "Too many attempts — wait a moment and try again.", else "Invalid email or password."; `busy` disables during submit; form has `aria-label="login"`; `useTitle('Sign in')`.

- [ ] **Step 1:** Read `LoginPage.test.tsx` — note its selectors (likely `getByLabelText(/email/i)`, `/password/i`, `getByRole('button', { name: /sign in/i })`, and the error assertions). Keep those resolvable.
- [ ] **Step 2:** Rewrite the JSX to compose `AuthCard`, keeping the existing state/`submit` logic:
```tsx
import { FormEvent, useState } from 'react'
import { endpoints } from '../lib/endpoints'
import { ApiError } from '../lib/api'
import { useAuth } from './AuthProvider'
import { useTitle } from '../lib/title'
import { AuthCard } from './AuthCard'
import { Input } from '../ui/Input'
import { Button } from '../ui/Button'

export function LoginPage() {
  useTitle('Sign in')
  const { refresh } = useAuth()
  const [email, setEmail] = useState('')
  const [password, setPassword] = useState('')
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)

  async function submit(e: FormEvent) {
    e.preventDefault()
    setError(''); setBusy(true)
    try {
      await endpoints.login(email, password)
      await refresh()
    } catch (err) {
      if (err instanceof ApiError && err.status === 429) setError('Too many attempts — wait a moment and try again.')
      else setError('Invalid email or password.')
    } finally {
      setBusy(false)
    }
  }

  return (
    <AuthCard>
      <form onSubmit={submit} aria-label="login" className="flex flex-col gap-3 text-left">
        <div className="text-center">
          <h1 className="text-[17px] font-semibold tracking-tight text-ink">Sign in to Janus</h1>
          <p className="text-[12.5px] text-muted">Self-hosted secrets manager</p>
        </div>
        <Input label="Email" type="email" value={email} onChange={(e) => setEmail(e.target.value)} required />
        <Input label="Password" type="password" value={password} onChange={(e) => setPassword(e.target.value)} required />
        {error && <p role="alert" className="text-center text-[12.5px] text-danger">{error}</p>}
        <Button type="submit" block loading={busy}>Sign in</Button>
      </form>
    </AuthCard>
  )
}
```
(Note: `Button` `loading` shows a spinner + disables; the old "Signing in…" text is replaced by the spinner. If a test asserted the literal "Signing in…" text, update it to assert the button is disabled while busy instead — do NOT weaken the disabled-during-submit coverage.)

- [ ] **Step 3:** Run `cd web && npx vitest run src/auth/LoginPage.test.tsx`; fix selectors as needed (label association preserved by `Input label`). Then full `npx vitest run`.
- [ ] **Step 4: Gates** typecheck + no-raw-palette.
- [ ] **Step 5: Commit** `feat(web): re-skin LoginPage onto branded AuthCard + kit`

---

## Task 3: Re-skin `UnsealPage`

**Files:** Modify `web/src/unseal/UnsealPage.tsx`, `web/src/unseal/UnsealPage.test.tsx`

Behavior to PRESERVE VERBATIM (security + flow): seal-status load on mount + "Could not read seal status." error; `awskms` type → "Waiting for KMS auto-unseal…" and the 1.5s poll; `submitShare` copies `share` to a local `s`, calls `setShare('')` BEFORE the await, then `endpoints.unsealShare(s)`; on error "That share was rejected."; `reset` via `endpoints.unsealReset()`; the null-status "Loading…" state; `useTitle('Unseal')`.

- [ ] **Step 1:** Read `UnsealPage.test.tsx` — note selectors (share input, submit/reset buttons, segment/progress assertions, share-cleared assertion, KMS state).
- [ ] **Step 2:** Rewrite the sealed-state JSX to compose `AuthCard` + kit, keeping ALL logic. Key markup:
  - `Pill tone="danger" dot` "Sealed" (centered).
  - `<h1 className="text-[17px] font-semibold tracking-tight text-ink">Unseal Janus</h1>`.
  - `<p className="text-[12.5px] text-muted">{submitted} of {threshold} shares submitted</p>`.
  - Segments: `<div className="my-4 flex gap-1.5" aria-label={`Share progress: ${submitted} of ${threshold}`}>` with each `<span className={cn('h-1.5 flex-1 rounded-full', i < submitted ? 'bg-success' : 'bg-line')} />`. (Filled = `bg-success` per mockup, changed from the current `bg-brand`.)
  - `Input` (share): `label="Key share"`, `type="password"`, `autoComplete="off"`, `className="font-mono"`.
  - `role="alert"` error line (danger, centered).
  - Buttons row: `<Button type="submit" block loading={busy}>Submit share</Button>` and `<Button type="button" variant="secondary" onClick={reset}>Reset</Button>` (keep them in a `flex gap-2`; `block` on submit + secondary Reset — adjust so they sit side by side, e.g. submit `flex-1` instead of `block` if `block`+row conflicts; prefer submit `flex-1` and Reset auto-width).
  - Memory-only hint: `<p className="text-[11.5px] text-faint">Shares are held in memory only and never logged.</p>`.
  - The `Loading…` null state and the `awskms` waiting state can stay as simple centered text (or wrap in `AuthCard` for consistency — either is fine; keep it simple).
- [ ] **Step 3:** Run `cd web && npx vitest run src/unseal/UnsealPage.test.tsx`; keep the share-cleared-after-submit assertion intact (strengthen if absent). Then full `npx vitest run`.
- [ ] **Step 4: Gates** typecheck + no-raw-palette + `npm run build` + `npm run smoke`.
- [ ] **Step 5: Commit** `feat(web): re-skin UnsealPage onto branded AuthCard + success segments`

---

## Final verification
- [ ] `cd web && npm run typecheck && npx vitest run && npm run build && npm run smoke` — all green, dual-theme.
- [ ] Rebuild dev container: `docker compose up -d --build janus && ./scripts/dev-unseal.sh`.
