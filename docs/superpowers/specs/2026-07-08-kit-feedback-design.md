# Component Kit (§4) + Feedback (§5) — Design

Two coupled FE punch-list slices from `fe-improvements.md`. §4 builds the reusable
primitive kit; §5 systematizes loading/error/success feedback on top of it. Visual
authority: `docs/design/ui-redesign-mockup.html` §08 (Component Kit) + the existing
token system. No visual invention — primitives render mockup markup from tokens.

## Goal

Stop re-styling primitives per screen. Ship a formal kit (`web/src/ui/`) and wire
consistent feedback (skeletons, toasts, friendly errors, styled unsaved-guard).

## §4 — Primitives (all in `web/src/ui/`, token classes only, both themes)

Existing kit already present: `Pill`, `EmptyState`, `Sheet`, `ConfirmDialog`,
`Toast`, `Brand`, Radix `DropdownMenu` (UserMenu), `cn`. New:

1. **`Button`** (`Button.tsx`) — mockup `.btn`. Props: `variant?: 'primary' | 'secondary' | 'ghost' | 'danger'` (default `primary`), `size?: 'md' | 'sm'` (default `md`), `block?: boolean`, `loading?: boolean`, plus native `<button>` attrs. Mockup mapping:
   - primary: `bg-brand text-white shadow-card hover:bg-brand-deep`
   - secondary: `bg-card text-ink border border-line hover:border-brand-line`
   - ghost: `bg-transparent text-muted hover:bg-brand-soft hover:text-brand-text`
   - danger: `bg-transparent text-danger border border-line hover:bg-danger-soft`
   - base: `inline-flex items-center gap-[7px] rounded font-semibold text-[13px] px-3.5 py-2 focus-visible:outline focus-visible:outline-2 focus-visible:outline-brand focus-visible:outline-offset-2`; `sm` = `text-[12px] px-2.5 py-1.5`; `block` = `w-full justify-center py-2.5`.
   - `loading`: sets `disabled`, shows a spinner (lucide `Loader2` with `animate-spin`) before children; `disabled` → `opacity-40 cursor-not-allowed` (respect existing patterns).
2. **`Input`** (`Input.tsx`), **`Textarea`** (`Textarea.tsx`), **`Select`** (`Select.tsx`) — mockup `.inp` + `field-label`. Shared styling `rounded border border-line bg-card px-3 py-1.5 text-[12.5px] text-ink placeholder:text-faint focus:border-brand focus-visible:outline-2 focus-visible:outline-brand`. Each accepts optional `label`, `error?: string` (renders `text-[11.5px] text-danger` + sets `aria-invalid` and `aria-describedby`), `id` (auto-generated via `useId` when omitted so label/field associate), and native attrs. `Textarea` renders `<textarea>`; `Select` renders `<select>` with `children` options.
3. **`Card`** (`Card.tsx`) — the ubiquitous surface: `rounded-card border border-line bg-card` + optional `className`, `as` element defaulting to `div`. Thin; standardizes the pattern used across ~10 screens.
4. **`Skeleton`** (`Skeleton.tsx`) — `animate-pulse rounded bg-line-soft` block with `className` for sizing; `aria-hidden`. Replaces ad-hoc `bg-line-soft` blocks.
5. **`Tooltip`** (`Tooltip.tsx`) — **Radix `@radix-ui/react-tooltip`** (new dep; shadcn-lean decision permits Radix primitives). Wraps a trigger child; `content: string`; token-styled panel `rounded bg-ink px-2 py-1 text-[11.5px] text-card shadow-pop`, small delay, `sideOffset`. Used for icon-buttons.

**Dropped / deferred:** `Tabs` — YAGNI (no screen uses tabs; env tabs became the R3 board). `Badge` — `Pill` already covers it. Broad "optimistic UI" — deferred (risky); rely on explicit saving/saved states.

## §5 — Feedback (built on the kit)

1. **Skeletons on list/table loads.** Replace remaining `"Loading…"` / bare text with `Skeleton` where a list or table is pending: secret editor (`Loading…`), and audit/token/member/transit lists that use text. Match the existing skeleton look (e.g. ProjectsList's card skeletons).
2. **Toasts on every mutation.** `Toast` infra exists (`useToast()({title, tone})`). Wire success/failure toasts on mutations currently silent: create project / environment / config, token mint & revoke, user create & disable, member put & delete, transit create/rotate/config/trim/delete. NEVER put secret values in a toast title.
3. **Error-envelope → friendly message.** Extend `web/src/lib/api.ts`: today `apiErrorTitle` only passes 403/409 messages through. Add a `code → friendly message` map (e.g. `validation`, `not_found`, `conflict`, `forbidden`, `rate_limited`) and a helper `errorMessage(e, fallback)` used by toasts + inline error surfaces (e.g. `ChangePassword`, which currently shows the raw message). Never surface raw internals for 5xx.
4. **Styled unsaved-changes guard.** The secret editor's `beforeunload` stays (browser-level), but in-app navigation away while dirty should trigger a styled `ConfirmDialog` ("Discard unsaved changes?") rather than nothing. Keep existing guard behavior; add the styled in-app confirm.

## §4 adoption scope (explicit boundary)

Build all primitives, then adopt them where it most matters this slice — **not** a
blanket sweep of every button/input in the app (that mechanical retrofit is a
follow-up, and the primitives are available for it):
- Migrate `structure/CreateForms.tsx` and `auth/ChangePassword.tsx` onto
  `Input`/`Textarea`/`Select`/`Button` (forms are where labels/error-text pay off;
  also sets up §6 auth polish).
- Add `Tooltip` to sidebar icon-buttons (the §1 deferral).
- Adopt `Skeleton` at the load sites listed in §5.1.
Other screens keep their current (already token-correct) markup; converting them is
out of scope here and tracked as a mechanical follow-up.

## Testing

- Each primitive: a focused vitest test (variants/props render expected token
  classes & roles; `Button` loading disables + shows spinner; `Input`/`Select`
  error sets `aria-invalid` + associates label; `Tooltip` shows content on hover/focus).
- §5: tests that a representative mutation fires a toast on success and on error;
  `errorMessage` maps codes correctly (unit test); ChangePassword shows a mapped
  message; editor in-app-dirty navigation prompts the styled confirm.
- Gates unchanged: `no-raw-palette.test.ts` (token-only), `typecheck`, full
  `vitest`, `build`, dual-theme `smoke`.

## Task decomposition (TDD, subagent-driven)

1. `Button` + test.
2. `Input` + `Textarea` + `Select` + tests.
3. `Card` + `Skeleton` + tests.
4. Add `@radix-ui/react-tooltip`; `Tooltip` + test.
5. `errorMessage` map in `api.ts` + unit test; extend/keep `apiErrorTitle`.
6. Adopt kit in `CreateForms.tsx` + `ChangePassword.tsx` (Input/Select/Button + mapped errors) + update tests.
7. Toasts on all listed mutations (+ tests for a representative success/error).
8. Skeleton adoption at load sites + sidebar icon-button Tooltips.
9. Styled in-app unsaved-changes confirm in the secret editor + test.

Frontend-only slice; no backend changes. Each task token-only, dual-theme safe.
