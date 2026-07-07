# Component Kit (§4) + Feedback (§5) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax.

**Goal:** Ship a formal primitive kit (`web/src/ui/`) and systematize feedback (skeletons, toasts, friendly errors, styled unsaved-guard) on top of it.

**Architecture:** Small, token-only React primitives matching mockup §08; then wire §5 feedback and adopt the kit in forms + high-value spots. Frontend-only.

**Tech Stack:** React 18 + TS, Tailwind token classes, Radix primitives, lucide-react, TanStack Query, vitest + @testing-library.

**Spec:** `docs/superpowers/specs/2026-07-08-kit-feedback-design.md` — carries the exact token-class strings for every primitive; implementers should follow those verbatim.

**Rules:** token classes only (no raw palette/hex — `no-raw-palette.test.ts` gate); render in both themes; `cn()` from `../ui/cn` for class merging; run npm from `web/`.

---

## Task 1: `Button`

**Files:** Create `web/src/ui/Button.tsx`, `web/src/ui/Button.test.tsx`

- [ ] **Step 1: Failing test**
```tsx
import { render, screen } from '@testing-library/react'
import { Button } from './Button'

test('primary variant renders brand background and is a button', () => {
  render(<Button>Save</Button>)
  const b = screen.getByRole('button', { name: 'Save' })
  expect(b.className).toContain('bg-brand')
})
test('loading disables the button and shows a spinner', () => {
  render(<Button loading>Save</Button>)
  const b = screen.getByRole('button', { name: /save/i })
  expect(b).toBeDisabled()
  expect(b.querySelector('svg')).toBeTruthy() // spinner
})
test('danger + sm variant applies the mapped classes', () => {
  render(<Button variant="danger" size="sm">Delete</Button>)
  const b = screen.getByRole('button', { name: 'Delete' })
  expect(b.className).toContain('text-danger')
  expect(b.className).toContain('text-[12px]')
})
```

- [ ] **Step 2: Run → FAIL** `cd web && npx vitest run src/ui/Button.test.tsx`

- [ ] **Step 3: Implement** `web/src/ui/Button.tsx` — use the exact class mappings from spec §4.1:
```tsx
import type { ButtonHTMLAttributes, ReactNode } from 'react'
import { Loader2 } from 'lucide-react'
import { cn } from './cn'

type Variant = 'primary' | 'secondary' | 'ghost' | 'danger'
const variants: Record<Variant, string> = {
  primary: 'bg-brand text-white shadow-card hover:bg-brand-deep',
  secondary: 'bg-card text-ink border border-line hover:border-brand-line',
  ghost: 'bg-transparent text-muted hover:bg-brand-soft hover:text-brand-text',
  danger: 'bg-transparent text-danger border border-line hover:bg-danger-soft',
}

export function Button({
  variant = 'primary', size = 'md', block = false, loading = false,
  className, disabled, children, ...rest
}: {
  variant?: Variant
  size?: 'md' | 'sm'
  block?: boolean
  loading?: boolean
  children?: ReactNode
} & ButtonHTMLAttributes<HTMLButtonElement>) {
  return (
    <button
      {...rest}
      disabled={disabled || loading}
      className={cn(
        'inline-flex items-center gap-[7px] rounded font-semibold text-[13px] px-3.5 py-2',
        'focus-visible:outline focus-visible:outline-2 focus-visible:outline-brand focus-visible:outline-offset-2',
        variants[variant],
        size === 'sm' && 'text-[12px] px-2.5 py-1.5',
        block && 'w-full justify-center py-2.5',
        (disabled || loading) && 'opacity-40 cursor-not-allowed',
        className,
      )}
    >
      {loading && <Loader2 size={14} strokeWidth={1.8} className="animate-spin" />}
      {children}
    </button>
  )
}
```

- [ ] **Step 4: Run → PASS**, then `npm run typecheck && npx vitest run src/test/no-raw-palette.test.ts`
- [ ] **Step 5: Commit** `feat(web): Button primitive (variants/size/block/loading)`

---

## Task 2: `Input` / `Textarea` / `Select`

**Files:** Create `web/src/ui/Input.tsx`, `Textarea.tsx`, `Select.tsx` + one test file `web/src/ui/fields.test.tsx`.

Shared field classes (spec §4.2): `rounded border border-line bg-card px-3 py-1.5 text-[12.5px] text-ink placeholder:text-faint focus:border-brand focus-visible:outline-2 focus-visible:outline-brand`.

- [ ] **Step 1: Failing test** `web/src/ui/fields.test.tsx`
```tsx
import { render, screen } from '@testing-library/react'
import { Input } from './Input'
import { Select } from './Select'

test('Input associates its label and shows error with aria-invalid', () => {
  render(<Input label="Slug" error="required" value="" onChange={() => {}} />)
  const field = screen.getByLabelText('Slug')
  expect(field).toHaveAttribute('aria-invalid', 'true')
  expect(screen.getByText('required')).toBeInTheDocument()
})
test('Select renders options and its label', () => {
  render(<Select label="Base"><option value="a">a</option></Select>)
  expect(screen.getByLabelText('Base')).toBeInTheDocument()
  expect(screen.getByRole('option', { name: 'a' })).toBeInTheDocument()
})
```

- [ ] **Step 2: Run → FAIL**

- [ ] **Step 3: Implement.** Each component: generate an id with `useId()` when `id` prop absent; render an optional `<label className="text-[12px] font-semibold text-ink" htmlFor={id}>`; render the control with the shared classes; when `error`, set `aria-invalid` + `aria-describedby={errId}` and render `<p id={errId} className="mt-1 text-[11.5px] text-danger">{error}</p>`. `Input` = `<input>`; `Textarea` = `<textarea>`; `Select` = `<select>{children}</select>`. Spread native attrs. Extract the shared label+error wrapper into a tiny local `Field` helper in one file (e.g. `Input.tsx` exports `Field`) OR duplicate the ~6 lines — keep it simple, DRY within reason.

- [ ] **Step 4: Run → PASS**, typecheck, no-raw-palette.
- [ ] **Step 5: Commit** `feat(web): Input/Textarea/Select field primitives`

---

## Task 3: `Card` + `Skeleton`

**Files:** Create `web/src/ui/Card.tsx`, `web/src/ui/Skeleton.tsx` + `web/src/ui/surfaces.test.tsx`.

- [ ] **Step 1: Failing test**
```tsx
import { render, screen } from '@testing-library/react'
import { Card } from './Card'
import { Skeleton } from './Skeleton'

test('Card renders children on the card surface', () => {
  render(<Card>hi</Card>)
  const el = screen.getByText('hi')
  expect(el.className).toContain('bg-card')
  expect(el.className).toContain('rounded-card')
})
test('Skeleton is decorative and animated', () => {
  const { container } = render(<Skeleton className="h-4 w-10" />)
  const el = container.firstChild as HTMLElement
  expect(el).toHaveAttribute('aria-hidden')
  expect(el.className).toContain('animate-pulse')
  expect(el.className).toContain('bg-line-soft')
})
```

- [ ] **Step 2: Run → FAIL**
- [ ] **Step 3: Implement.**
```tsx
// Card.tsx
import type { ReactNode } from 'react'
import { cn } from './cn'
export function Card({ className, children }: { className?: string; children: ReactNode }) {
  return <div className={cn('rounded-card border border-line bg-card', className)}>{children}</div>
}
// Skeleton.tsx
import { cn } from './cn'
export function Skeleton({ className }: { className?: string }) {
  return <div aria-hidden className={cn('animate-pulse rounded bg-line-soft', className)} />
}
```
- [ ] **Step 4: Run → PASS**, typecheck, no-raw-palette.
- [ ] **Step 5: Commit** `feat(web): Card + Skeleton primitives`

---

## Task 4: `Tooltip` (Radix)

**Files:** Modify `web/package.json` (add dep); Create `web/src/ui/Tooltip.tsx`, `web/src/ui/Tooltip.test.tsx`.

- [ ] **Step 1: Add dependency**
Run: `cd web && npm install @radix-ui/react-tooltip@^1.1.0`
(Match the Radix major already used; pick the version npm resolves that is compatible with React 18. Verify it appears in `package.json` dependencies and `package-lock.json` updates.)

- [ ] **Step 2: Failing test** `web/src/ui/Tooltip.test.tsx`
```tsx
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { Tooltip } from './Tooltip'

test('shows its content on focus', async () => {
  render(<Tooltip content="Copy value"><button>icon</button></Tooltip>)
  await userEvent.tab() // focus the trigger
  // Radix renders the content (possibly duplicated for a11y); assert at least one.
  expect(await screen.findAllByText('Copy value')).not.toHaveLength(0)
})
```

- [ ] **Step 3: Implement** `web/src/ui/Tooltip.tsx` — wrap Radix, token-styled panel (spec §4.5). Provide a single app-level `TooltipProvider` export too if needed, or use Radix's `Provider` inline with a small `delayDuration`.
```tsx
import type { ReactNode } from 'react'
import * as RT from '@radix-ui/react-tooltip'

export function Tooltip({ content, children }: { content: string; children: ReactNode }) {
  return (
    <RT.Provider delayDuration={300}>
      <RT.Root>
        <RT.Trigger asChild>{children}</RT.Trigger>
        <RT.Portal>
          <RT.Content sideOffset={6} className="rounded bg-ink px-2 py-1 text-[11.5px] text-card shadow-pop select-none">
            {content}
            <RT.Arrow className="fill-ink" />
          </RT.Content>
        </RT.Portal>
      </RT.Root>
    </RT.Provider>
  )
}
```

- [ ] **Step 4: Run → PASS**, typecheck, no-raw-palette, and `npm run build` (confirms the new dep bundles).
- [ ] **Step 5: Commit** `feat(web): Tooltip primitive (Radix) + dependency`

---

## Task 5: friendly error mapping

**Files:** Modify `web/src/lib/api.ts`; Create/extend `web/src/lib/api.test.ts`.

- [ ] **Step 1: Failing test**
```tsx
import { ApiError, errorMessage } from './api'
test('maps known codes to friendly text', () => {
  expect(errorMessage(new ApiError(404, 'not_found', 'x'))).toMatch(/not found/i)
  expect(errorMessage(new ApiError(429, 'rate_limited', 'x'))).toMatch(/too many/i)
})
test('passes through curated 403/409 messages', () => {
  expect(errorMessage(new ApiError(403, 'forbidden', 'You lack permission to do X'))).toMatch(/permission/i)
})
test('hides internals for 5xx', () => {
  expect(errorMessage(new ApiError(500, 'internal', 'stacktrace'))).not.toMatch(/stacktrace/)
})
```

- [ ] **Step 2: Run → FAIL**
- [ ] **Step 3: Implement** `errorMessage(e: unknown, fallback = 'Request failed.'): string` in `api.ts`. Keep existing `apiErrorTitle` (or re-implement it in terms of `errorMessage`). Map codes: `validation`→"Please check your input.", `not_found`→"Not found.", `conflict`→"That conflicts with an existing item.", `rate_limited`→"Too many attempts — try again shortly.", `forbidden`→pass server message through (curated). For 5xx or unknown → `fallback`. For 403/409 keep passing the server's curated `message` (as today).
- [ ] **Step 4: Run → PASS**, typecheck.
- [ ] **Step 5: Commit** `feat(web): friendly error-envelope message mapping`

---

## Task 6: adopt kit in forms

**Files:** Modify `web/src/structure/CreateForms.tsx`, `web/src/auth/ChangePassword.tsx`, and their tests.

- [ ] **Step 1:** Read both files + their tests. Replace bare `<input>`/`<select>`/`<button>` with `Input`/`Select`/`Button`. Wire submit buttons to `loading={mutation.isPending}`. Surface mutation errors via `errorMessage(...)` in the field `error` prop or an inline `text-danger` line (ChangePassword currently shows raw `ApiError.message` — route it through `errorMessage`). Preserve all existing labels/aria so existing tests keep passing; update selectors only where markup changed.
- [ ] **Step 2:** Run the affected tests → adjust → PASS. Full `npx vitest run`.
- [ ] **Step 3: Commit** `refactor(web): adopt kit in CreateForms + ChangePassword`

---

## Task 7: toasts on mutations

**Files:** Modify the mutation call-sites listed in spec §5.2 (structure creates, tokens, users, members, transit). Add a representative test.

- [ ] **Step 1:** For each mutation currently lacking feedback, add `onSuccess` → `toast({ title: '…', tone: 'success' })` and `onError` → `toast({ title: errorMessage(err), tone: 'danger' })`. Titles describe the action ("Project created", "Token revoked") — NEVER a secret value. Reuse the existing `useToast()`.
- [ ] **Step 2: Test** (representative) — e.g. creating a project shows a success toast; a failing mutation shows a danger toast with the mapped message. `cd web && npx vitest run`.
- [ ] **Step 3: Commit** `feat(web): success/error toasts on all mutations`

---

## Task 8: skeleton adoption + sidebar tooltips

**Files:** Modify load sites (secret editor `Loading…`, and any list still using text) to render `Skeleton`; add `Tooltip` to sidebar/topbar icon-buttons.

- [ ] **Step 1:** Replace `<p>Loading…</p>` style pending states with `Skeleton` blocks sized to the content (mirror ProjectsList's skeleton pattern). Wrap icon-buttons that only have `aria-label` (sidebar `＋`, etc.) in `Tooltip content={...}` so sighted users get labels too (keep the `aria-label`).
- [ ] **Step 2:** Update/add tests as needed; full `npx vitest run`, typecheck, no-raw-palette.
- [ ] **Step 3: Commit** `feat(web): skeleton loaders + icon-button tooltips`

---

## Task 9: styled in-app unsaved-changes confirm

**Files:** Modify `web/src/secrets/SecretEditor.tsx` (+ test).

- [ ] **Step 1: Failing test** — when the editor is dirty and the user triggers in-app navigation away (e.g. clicks a nav link / the breadcrumb), a styled confirm ("Discard unsaved changes?") appears; confirming proceeds, cancelling stays. Use the existing `ConfirmDialog`.
- [ ] **Step 2: Implement** using react-router's navigation blocking (`useBlocker` if available in the installed v6, else intercept nav via a guard around link clicks) + `ConfirmDialog`. Keep the existing `beforeunload` handler for full-page unloads.
- [ ] **Step 3: Run → PASS**, full `npx vitest run`.
- [ ] **Step 4: Commit** `feat(web): styled in-app unsaved-changes guard in editor`

---

## Final verification

- [ ] `cd web && npm run typecheck && npx vitest run && npm run build && npm run smoke` — all green, dual-theme.
- [ ] Rebuild dev container: `docker compose up -d --build janus && ./scripts/dev-unseal.sh`.
