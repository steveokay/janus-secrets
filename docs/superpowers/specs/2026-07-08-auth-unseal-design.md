# §6 — Auth / Unseal (branded) — Design

FE punch-list §6. Re-skin the login and unseal screens to the approved mockup §07
`.auth-card` treatment, using the new kit. First screen a user sees — must feel
intentional and branded. Frontend-only.

Visual authority: `docs/design/ui-redesign-mockup.html` §07 (Auth / unseal).

## Current state (baseline)

`web/src/auth/LoginPage.tsx` and `web/src/unseal/UnsealPage.tsx` already exist,
token-correct, and pass the dark audit. They use RAW `<input>`/`<button>` markup
(pre-kit) and a left-aligned card — not the mockup's centered/branded `.auth-card`.
UnsealPage already renders share-progress segments and correctly holds shares in
memory only (cleared before the await, never logged). This is a re-skin, not a
behavior change.

## Mockup §07 target

A compact, centered, branded card on the page background:
- `.auth-stage` — page-bg, centered, generous vertical padding.
- `.auth-card` — 340px, `bg-card` + `border-line` + `shadow-card`, radius 14px,
  **text-centered**, padding ~28px.
- `.auth-mark` — a 44px `bg-brand-soft` + `border-brand-line` rounded tile centered
  at top, holding the Janus hexagon mark.
- Heading (`h3`, ~18px), optional sub-label.
- Status `Pill` (Sealed=danger / Unsealed=success) centered.
- **Share-progress segments** (`.shares` / `.share-seg`): equal-width bars; filled
  segments use **success green** (`bg-success`, per mockup `--ok`), empty use
  `bg-line`. (NOTE: the punch-list said "progress ring" — the approved mockup uses
  SEGMENTS, which win. No ring.)
- Masked share `Input` (mono), a memory-only reassurance hint (`.auth-hint`, faint).
- Primary `block` `Button`.

## Components

### `web/src/auth/AuthCard.tsx` (new — shared shell)
The centered branded shell both screens compose, so the treatment lives in one place:
```
<div className="flex min-h-screen items-center justify-center bg-page px-4">
  <div className="w-[340px] max-w-full rounded-[14px] border border-line bg-card p-7 text-center shadow-card">
    <div className="mx-auto mb-4 flex h-11 w-11 items-center justify-center rounded-xl border border-brand-line bg-brand-soft">
      <BrandMark />   {/* hexagon only, no wordmark; brand-colored */}
    </div>
    {children}
  </div>
</div>
```
Props: `children`. Uses the existing Janus mark (extract the hexagon from `ui/Brand.tsx`
if it exposes a mark-only variant; otherwise render the mockup's inline hexagon SVG
via `currentColor`/`text-brand`, `aria-hidden`). No raw hex in the component — the
mark uses `text-brand` + `currentColor`.

### `web/src/auth/LoginPage.tsx` (re-skin)
Compose `AuthCard`: heading "Sign in to Janus", sub-label "Self-hosted secrets
manager", `Input` (email, `type="email"`, `label="Email"`), `Input` (password,
`type="password"`, `label="Password"`), a `block` primary `Button`
(`loading={busy}`, "Sign in"), and a centered `role="alert"` error line. Keep the
existing submit logic; keep the friendly messages (invalid credentials; 429
rate-limit). Preserve `aria-label="login"` on the form and existing test hooks.

### `web/src/unseal/UnsealPage.tsx` (re-skin)
Compose `AuthCard`: `Pill` (Sealed danger / — the card only shows while sealed),
heading "Unseal Janus", `k of threshold shares submitted` label, the share
segments (filled=`bg-success`), masked `Input` (mono, `label="Key share"`,
`autoComplete="off"`), a `block` primary `Button` ("Submit share", `loading={busy}`),
a secondary `Button` "Reset", and the memory-only hint. KEEP all existing behavior
verbatim: share cleared before the await, KMS auto-unseal poll + "Waiting for KMS
auto-unseal…" state, seal-status load + error. Segments keep `aria-label`
("Share progress: k of n").

### First-login (light)
The mockup/spec calls for a "friendly first-login" prompting the one-time-password
change after the init ceremony. The app has no first-login/OTP signal today (no flag
on `me`/seal-status), and adding one is backend work out of scope here. So this slice
does NOT implement automatic first-login detection; `ChangePassword` (already
kit-adopted) remains reachable via the user menu. First-login auto-prompt is noted as
a follow-up needing a backend signal.

## Testing

- `AuthCard`: renders the branded mark (aria-hidden) + children.
- `LoginPage`: existing tests updated to the kit markup (label association via the
  `Input` `label`); submit still posts + refreshes; error path shows the friendly
  message; `Button` shows loading state while busy.
- `UnsealPage`: existing tests updated; segments reflect `submitted`/`threshold`;
  share cleared after submit (security-critical — keep/strengthen this assertion);
  KMS state renders the waiting message; reset works.
- Gates unchanged: `no-raw-palette`, `typecheck`, full `vitest`, `build`, dual-theme
  `smoke`.

## Security (unchanged, must hold)

Shares live in local state only, cleared before the network await, never logged or
persisted. No secret/share value in any error message, toast, or DOM attribute. The
password/share `Input`s use `type="password"` + `autoComplete="off"` (share).

## Task decomposition (TDD, subagent-driven)

1. `AuthCard` shell (+ mark-only) + test.
2. Re-skin `LoginPage` onto AuthCard + `Input`/`Button` + update tests.
3. Re-skin `UnsealPage` onto AuthCard + `Input`/`Button` + success-green segments,
   preserve KMS/reset/share-clear behavior + update tests.
