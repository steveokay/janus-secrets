# §7 A11y & Responsive Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: superpowers:subagent-driven-development. Steps use checkbox (`- [ ]`) syntax.

**Goal:** Close the remaining §7 gaps — secret-table horizontal-scroll containment, editor `Esc`-to-cancel, and a page-level responsive-overflow guard. Frontend-only.

**Tech Stack:** React 18 + TS, Tailwind tokens, vitest + @testing-library.

**Spec:** `docs/superpowers/specs/2026-07-08-a11y-responsive-design.md`. Focus rings (global `:focus-visible` in `index.css`), contrast (dark-AA guard), and Radix menu/dialog keyboarding are ALREADY done — do not redo them. Rules: token classes only (`no-raw-palette` gate); both themes; run npm from `web/`.

---

## Task 1: Secret-table scroll containment + `Esc`-to-cancel edit

**Files:** Modify `web/src/secrets/SecretTable.tsx`, `web/src/secrets/SecretEditor.test.tsx`

Current structure (`SecretTable.tsx`): the component returns a single card
`<div className="rounded-card border border-line bg-card overflow-hidden">` holding a
sticky header row and the data rows, all laid out with
`GRID = 'grid grid-cols-[1.3fr_1.5fr_108px_56px_92px] items-center gap-3 px-4'`. The
per-row edit `<input aria-label={`value for ${key}`} …>` currently has `onChange` but
no key handling.

- [ ] **Step 1: Write the failing tests** — add to `web/src/secrets/SecretEditor.test.tsx`
  (this file already seeds masked rows via a `seed()` helper and renders `<SecretEditor />`;
  mirror its existing "cancelling an in-progress edit" test):
```tsx
test('pressing Escape in an edit field cancels the edit', async () => {
  seed()
  renderApp(<SecretEditor />, { route: '/projects/p1/configs/c1', withAuth: false })
  await screen.findByText('DB_URL')
  await userEvent.click(screen.getByRole('button', { name: /edit db_url/i }))
  const input = screen.getByRole('textbox', { name: /value for db_url/i })
  await userEvent.type(input, '{Escape}')
  expect(screen.queryByRole('textbox', { name: /value for db_url/i })).toBeNull()
})

test('the secret table is wrapped in a horizontal-scroll container', async () => {
  seed()
  const { container } = renderApp(<SecretEditor />, { route: '/projects/p1/configs/c1', withAuth: false })
  await screen.findByText('DB_URL')
  expect(container.querySelector('.overflow-x-auto')).not.toBeNull()
})
```

- [ ] **Step 2: Run → the Escape test FAILs** `cd web && npx vitest run src/secrets/SecretEditor.test.tsx`

- [ ] **Step 3: Implement in `SecretTable.tsx`**
  1. Wrap the returned card in an `overflow-x-auto` container and give the card a
     `min-w` so it scrolls (not squishes) on narrow screens. Change:
```tsx
  return (
    <div className="rounded-card border border-line bg-card overflow-hidden">
```
     to:
```tsx
  return (
    <div className="overflow-x-auto">
    <div className="min-w-[720px] rounded-card border border-line bg-card overflow-hidden">
```
     and add the matching extra closing `</div>` at the end of the component's returned
     JSX (close the new outer wrapper).
  2. Add an Escape handler to the per-row edit `<input>` (the one with
     `aria-label={`value for ${key}`}`): add
     `onKeyDown={(e) => { if (e.key === 'Escape') onRevert(key) }}`
     alongside its existing `onChange`. (`onRevert` is already a prop — it's what the
     cancel X button calls.)

- [ ] **Step 4: Run → PASS** `cd web && npx vitest run src/secrets/SecretEditor.test.tsx`, then `npm run typecheck && npx vitest run src/test/no-raw-palette.test.ts`.

- [ ] **Step 5: Commit**
```bash
git add web/src/secrets/SecretTable.tsx web/src/secrets/SecretEditor.test.tsx
git commit -m "feat(web): secret-table horizontal-scroll containment + Esc-to-cancel edit"
```

---

## Task 2: Page-level responsive-overflow guard

**Files:** Modify `web/src/shell/*` (the app layout) as needed.

- [ ] **Step 1:** Read the app shell layout (`web/src/shell/` — the component that
  lays out sidebar + topbar + main content, likely `Shell.tsx`/`AppShell.tsx` or
  similar). Identify the main content flex/grid container.
- [ ] **Step 2:** Ensure no horizontal BODY overflow at tablet width: the main content
  column that holds routed screens (including the now-scrollable secret table and the
  already-scrollable board columns) must contain its own overflow. The most common
  cause is a flex child without `min-w-0` refusing to shrink — add `min-w-0` to the
  main content flex child if it lacks it, and/or `overflow-x-hidden` on the shell's
  outermost container so a stray wide child can't force a body scrollbar. Keep changes
  minimal and targeted — NO layout redesign, no visual change at desktop width.
- [ ] **Step 3: Verify** `cd web && npx vitest run && npm run typecheck && npx vitest run src/test/no-raw-palette.test.ts && npm run build && npm run smoke`. Full suite green (was 195 + Task 1's 2 = 197), build + dual-theme smoke pass.
- [ ] **Step 4: Commit**
```bash
git add web/src/shell
git commit -m "fix(web): contain horizontal overflow at tablet width (min-w-0 / overflow guard)"
```

---

## Final verification
- [ ] `cd web && npm run typecheck && npx vitest run && npm run build && npm run smoke` — all green, dual-theme.
- [ ] Rebuild dev container: `docker compose up -d --build janus && ./scripts/dev-unseal.sh`.
