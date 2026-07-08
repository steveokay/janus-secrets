# Front-End Improvements — Janus Web UI

> **Status:** ideas / backlog tracker. Nothing here is built yet unless checked.
> **Visual design: APPROVED 2026-07-06.** Canonical mockup:
> [`docs/design/ui-mockup.html`](docs/design/ui-mockup.html) · spec (tokens + hard
> rules): [`docs/superpowers/specs/2026-07-06-ui-visual-design.md`](docs/superpowers/specs/2026-07-06-ui-visual-design.md).
> All slices below implement that mockup — agents must not restyle ad hoc
> (enforced via the "Web UI visual system" section in CLAUDE.md).
> **Direction (locked):** a modern, polished, *Doppler-inspired* light theme —
> clean, appealing, product-grade — with an optional dark mode. The current
> milestone-1 SPA is function-first (white-on-white, hairline borders, tiny glyph
> buttons, one-line gray empty states). This doc is the punch-list to take it from
> "empty paper" to something that looks like a real SaaS secrets manager.

## How to use this file

Each item has a priority and a checkbox. We turn a batch of `P0`/`P1` items into a
brainstorm → spec → plan slice (same flow as the rest of the project) and check
them off as they ship. Priorities:

- **P0** — foundational or highest-impact "it looks blank/cheap" fixes. Do first.
- **P1** — makes it feel like a real product; do once P0 lands.
- **P2** — polish, delight, nice-to-have.

---

## Dark redesign (2026-07-07 — in progress)

Canonical: `docs/design/ui-redesign-mockup.html` + spec
`docs/superpowers/specs/2026-07-07-dark-redesign-design.md`. Slices:
- [x] **R1** dual-theme foundation (CSS-var tokens, ThemeProvider, toggle, dark palette) — delivers the long-deferred dark-mode P1 item.
- [x] **R2** shell & ⌘K command palette (Doppler primary nav + contextual project tree; global ⌘K fuzzy search over projects/configs/secret key names + nav actions; topbar theme toggle; instance /audit route)
- [x] **R3** projects list & env-columns project board & create-project modal (searchable/sortable card grid with grid⇄list toggle + config counts; env-column board with inheritance nesting, add-config/add-env, breadcrumb + `janus run` hint; polished create-project modal)
- [x] **R4** screen polish pass (editor, version history, audit, tokens, members, auth/unseal) — dark-AA `text-brand-deep`→`text-brand-text` migration + guard test; project-board env-loading skeleton, sr-only h1, cycle-safe config tree; ⌘K palette a11y (`aria-activedescendant`, group roles, IME guard, reopen-clear). Dark-render audit of all six screens: clean.

### Remaining after R1–R4 + B5 (2026-07-07)

The dark redesign (R1–R4) and all feature slices B2–B5 are shipped. What's left in
this tracker, roughly by size:

- **§3 Secret editor redesign — DONE** (mockup §06): grid table, origin pills,
  per-row reveal/copy/edit/discard/revert/restore, dirty-bar (Review diff /
  Discard / Save as vN), key filter, Import .env, inherited→override. Remaining
  P2 follow-ups only: reveal-all toggle + auto-re-mask on blur/idle, editor
  **keyboard support** (`⌘S`, row nav), and the hand-rolled Review/Import dialogs
  want `aria-modal`/Escape/focus-trap (or compose the Sheet primitive) + a
  "no matches" state when filtered to empty.
- **§4 kit primitives — DONE (PR #37):** **Button** variants, **Input/Select/Textarea**,
  **Tooltip** (Radix)/**Card**/**Skeleton**. `Tabs` dropped (YAGNI); `Badge` = `Pill`.
  *(Dialog, Toast, Dropdown, Pill already shipped.)*
- **§5 feedback — mostly DONE (PR #37):** **skeletons** (editor + version history),
  **toasts on all mutations**, **error-envelope → friendly message** (`errorMessage`,
  403/409 curated-first). Deferred: broad optimistic UI; styled in-app
  unsaved-changes guard (needs a data-router migration for `useBlocker` —
  `beforeunload` guard remains).
- **§6 auth/unseal — DONE (PR #40):** branded `AuthCard` shell; login + unseal
  re-skinned onto the kit; share-progress **segments** (green-filled, per mockup —
  not a "ring"); share-clear-before-await preserved. First-login auto-prompt
  deferred (needs a backend first-login signal).
- **§7 a11y & responsive — DONE (PR #41):** secret-table horizontal-scroll
  containment (`overflow-x-auto` + `min-w`) + editor `Esc`-to-cancel. Focus-ring
  (global `:focus-visible`), contrast (dark-AA guard), Radix menu/dialog
  keyboarding, and the `min-w-0` shell overflow-guard were already in place.
- **Lower-priority P2s:** §0 motion/reduced-motion, §1 collapsible sidebar, §2
  onboarding checklist.
- **§8:** the **usage-metrics dashboard — DONE** (sub-project **D**, PR #35): on-demand
  "Reads 24h" total + top configs/tokens from `secret.reveal` audit events; instance
  strip on the Projects list + project-scoped row on the board; dual-scope
  `/v1/metrics/reads-24h` routes; names+counts only, hides on 403.

---

## 0. Design foundations (P0 — everything else builds on this)

A shared design system so screens stop looking ad-hoc. Today there are no tokens
at all — every component picks raw Tailwind grays.

- [x] **P0** Define a **color palette**: slate neutral ramp for surfaces/text, one
      accent (indigo/violet — Doppler leans violet), plus semantic colors
      (success/green, warning/amber, danger/red, info/blue). Wire as Tailwind
      theme tokens in `tailwind.config.js` (e.g. `brand`, `surface`, `muted`) so
      components reference roles, not raw `gray-400`. *(Slice 1 — plus a vitest
      gate banning raw palette classes AND hex literals in `web/src`.)*
- [x] **P0** **Typography scale**: a proper sans (Inter or system UI stack),
      defined sizes/weights for page title / section / body / caption; a mono
      stack (JetBrains Mono / ui-monospace) reserved for secret keys & values.
      *(Slice 1 — system stacks, mono reserved for secret material.)*
- [x] **P0** **Surfaces & depth**: a page background tint (`slate-50`), white
      **cards** with `rounded-lg`, a hairline border **and** a soft shadow so
      content sits *on* the page instead of bleeding into it. *(Slice 1 —
      `page`/`card`/`line` tokens + `shadow-card`/`shadow-pop`.)*
- [x] **P0** **Spacing & radius rhythm**: consistent padding scale, consistent
      corner radius, consistent gaps. Pick one and apply everywhere. *(Slice 1 —
      radius 8px controls / 10px cards / pills full.)*
- [x] **P1** **Dark mode**: `class`-based Tailwind dark variant + a top-bar toggle,
      persisted to `localStorage`. Design tokens above should make this mostly free.
      *(R1 — dual-theme CSS-var tokens in `web/src/theme.css`, `ThemeProvider`
      (light/dark/system, `localStorage janus.theme`), top-bar + user-menu toggle,
      no-flash boot script; smoke asserts both themes.)*
- [ ] **P2** **Motion**: subtle 150ms transitions on hover/focus/expand; respect
      `prefers-reduced-motion`.

**Open decision:** hand-rolled Tailwind components vs. adopting **shadcn/ui**
(Radix primitives + Tailwind, copied into the repo — no runtime dep, accessible
by default). shadcn would accelerate buttons/dialogs/menus/toasts/tooltips and
raise quality fast; worth a short discussion given the "minimal deps" ethos.

> **Decision (2026-07-06, Slice 1 planning): shadcn-lean.** Adopt the pattern,
> not the whole kit: Radix `DropdownMenu` + `lucide-react` + `cn()`
> (clsx + tailwind-merge) ship in Slice 1; Dialog/Toast/Tooltip primitives are
> adopted in Slice 3 when the component kit (§4) is built.

---

## 1. App shell & branding (P0)

The frame the user sees on every screen. Right now: a bare top bar with the word
"Janus" and two outlined buttons.

- [x] **P0** **Brand mark + wordmark** in the top bar — Janus = the two-faced
      Roman god (fitting for a symmetric encrypt/decrypt product). An inline SVG
      mark + styled wordmark. Also a **favicon** and page `<title>` per route.
      *(Slice 1 — split-hexagon mark via `currentColor`, favicon.svg, `useTitle`.)*
- [x] **P0** **Top bar redesign**: logo left; center/left **breadcrumb**
      (Project › Environment › Config); right side a **seal-status pill**
      (green "Unsealed" / red "Sealed") and a **user menu** (avatar/initials →
      dropdown: email, change password, dark-mode toggle, log out) instead of two
      loose buttons. *(Slice 1 — dark-mode toggle deferred with dark mode itself.)*
- [x] **P1** **Sidebar redesign**: section headers ("PROJECTS", "INSTANCE"),
      **icons** on each nav item, clear **active-item** highlight (accent bar +
      tint), hover states, and grouping. Replace the tiny `＋` glyphs with proper
      icon-buttons with tooltips. *(Slice 1 — tooltips deferred to the §4 kit;
      icon-buttons have aria-labels.)*
- [x] **P1** **Environment tabs** (Doppler signature): show dev/staging/prod as
      color-coded tabs/pills (prod = red-tinted as a "danger" cue) rather than a
      plain nested list. *(R3 — env-columns project board with `envTone` color
      coding: dev=blue / staging=amber / prod=red accent bars + dots.)*
- [ ] **P2** **Collapsible sidebar** for narrow screens; remember state.

---

## 2. Landing / dashboard — kill the blank page (P0)

The #1 "empty paper" complaint: after login you land on
`"Select or create a project to begin."` — one gray sentence on white.

- [x] **P0** **Real landing state**: a welcoming hero with the Janus mark, a short
      value line, and prominent **"Create your first project"** / "Open recent"
      CTAs. Never a lone sentence. *(Slice 2 — hero when no projects, link-card
      list when projects exist.)*
- [x] **P1** **Project overview dashboard** (once a project is selected): cards for
      each environment→config with secret counts, last-updated, and a
      **"Reads 24h"** stat (ties into Phase 2 sub-project D usage metrics),
      recent activity from the audit log, and quick actions. *(Slice 2 — env
      cards with key counts + last-change from masked metadata; "Reads 24h" is
      a placeholder pill until Phase-2D; recent activity stays in B3.)*
- [x] **P1** **Richer empty states everywhere** (no configs, no secrets, no
      tokens): a small illustration/icon + heading + one-line explainer + a CTA
      button. Reusable `<EmptyState>` component. *(Slice 2 — `<EmptyState>`
      shipped; applied to overview zero-envs + editor zero-keys; remaining
      screens adopt it as they land.)*
- [ ] **P2** Lightweight onboarding checklist ("create project → add environment →
      add secrets → run `janus run`") for first-time users.

---

## 3. Secret editor — the flagship screen (P0/P1)

This is where users spend their time; it should feel best. Today it's a plain
list with masked dots and a save button.

- [x] **P0** **Table layout**: aligned columns (Key · Value · Origin · Ver · Actions),
      sticky header, hover rows, monospace keys & values. *(§3 redesign — mockup §06 grid table.)*
- [x] **P0** **Origin badges as pills**: `own` / `inherited` / `overridden` as
      colored `<Pill>`s (own=green, inherited=muted, overridden=violet). *(§3 redesign.)*
- [x] **P0** **Per-row actions**: reveal (eye, audited) + **copy-to-clipboard** on
      hover; edit / discard / revert / restore icon-buttons by row state. *(§3 redesign.)*
- [x] **P1** **Dirty-state bar**: bottom bar summarizing pending changes
      ("+N added · N changed · N removed") with **Save as vN**, **Discard**, and
      **Review diff**, plus color-coded left rails + change chips per row. *(§3 redesign.)*
- [x] **P1** **Diff preview on save**: **Review diff** modal lists pending key
      names by change type before committing (value-free surface). *(§3 redesign.)*
- [x] **P1** **Search / filter** secrets by key; **`.env` bulk paste/import** into
      the dirty buffer. *(§3 redesign — toolbar filter + Import .env modal.)*
- [x] **P1** **Add-secret UX**: added keys render as visible, editable, discardable
      pending rows; inherited rows edit-to-override. *(§3 redesign.)*
- [ ] **P2** **Reveal ergonomics**: reveal-all toggle + auto-re-mask on blur/idle
      still TODO; "copied" toast + ephemeral-only plaintext DONE in §3. *(P2 follow-up.)*
- [x] **P2** **Version history drawer**: list config versions with author/time and
      one-click rollback (there's an API for this; today it's a placeholder).
      *(B2 — Sheet drawer with key-name-only diffs (zero values on this
      surface), confirm-gated audited rollback, disabled + visibly hinted while
      the editor is dirty.)*
- [ ] **P2** Keyboard support: `⌘/Ctrl+S` to save, arrow/enter navigation between
      rows, `Esc` to cancel an edit.

---

## 4. Reusable component kit (P1)

Stop re-styling primitives per screen; build once, use everywhere.

- [x] **P1** **Button** variants (primary/secondary/ghost/danger) + sizes + loading
      state, with proper focus rings. *(PR #37 — `ui/Button.tsx`.)*
- [x] **P1** **Input / Select / Textarea** with consistent styling, labels, error
      text, and disabled states. *(PR #37 — shared `Field` wrapper, `useId`,
      `aria-invalid`/`aria-describedby`.)*
- [x] **P1** **Modal/Dialog** (replaces the current bare create/change-password
      forms): focus-trapped, `Esc`-to-close, backdrop, header/body/footer.
      *(B2 — `ConfirmDialog` (Radix AlertDialog) + `Sheet` slide-over shipped;
      migrating CreateForms/ChangePassword onto them is Slice 3.)*
- [x] **P1** **Toast/notification** system for save success, errors, copied, etc.
      (currently no feedback surface at all). *(B2 — app-level `ToastProvider` +
      `useToast`; rollback flows use it; editor save/copy toasts land in Slice 3.)*
- [x] **P1** **Tooltip** (Radix), **Card**, **Skeleton** loaders. *(PR #37. `Badge` =
      existing `Pill`; `Tabs` dropped — YAGNI, no screen uses it.)*
- [x] **P2** **Dropdown menu** (Radix/headless) for the user menu and row actions.
      *(Slice 1 `UserMenu` on `@radix-ui/react-dropdown-menu`; reused for the
      transit `KeyActions` row menu in B5.)*

---

## 5. Feedback, loading & error states (P1)

- [x] **P1** **Skeleton loaders** for lists/tables instead of `"Loading…"` text.
      *(PR #37 — editor + version history; other lists already had skeletons.)*
- [x] **P1** **Toasts** on mutations (save, delete, token create, errors). *(PR #37
      — success/error toasts wired across all mutations; secrets never in titles.)*
- [x] **P1** **Inline error surfaces**: map the API `{error:{code,message}}`
      envelope to friendly, actionable messages (not raw codes). *(PR #37 —
      `errorMessage`, 403/409 curated-first so guardrail messages survive.)*
- [ ] **P1** **Optimistic UI** where safe; clear "saving…"/"saved vN" state on the
      save action. *(Deferred — risky; editor shows saving/saved state today.)*
- [ ] **P2** **Unsaved-changes guard** polish (styled confirm dialog, not the
      browser default) — keep the existing guard behavior. *(Deferred — needs
      data-router migration for react-router `useBlocker`; `beforeunload` remains.)*

---

## 6. Auth & unseal screens (P1)

First impression before the app even loads.

- [x] **P1** **Login page**: centered branded card (logo, heading, styled fields,
      primary button, clear error). *(PR #40 — `AuthCard` + kit `Input`/`Button`.)*
- [x] **P1** **Unseal screen**: focused branded layout — **share-progress segments**
      (green-filled, per mockup §07, not a ring) for "k of threshold shares", share
      input, reset. Shares stay ephemeral, cleared before the network await. *(PR #40.)*
- [ ] **P2** **Change-password / first-login**: friendly flow prompting the
      one-time-password change after the init ceremony. *(Deferred — needs a backend
      first-login/OTP signal; `ChangePassword` reachable via user menu today.)*

---

## 7. Accessibility & responsiveness (P1/P2)

- [x] **P1** Visible **focus rings** (global `:focus-visible` ring in `index.css`),
      **color contrast** (dark-AA guard test), `aria` labels/roles on icon-buttons +
      dialogs (Radix + tooltips). *(Ongoing since Slice 1 / R4; confirmed in §7.)*
- [x] **P1** Keyboard operability (Radix menus/dialogs; editor `Esc`-to-cancel via
      PR #41). *(Deep editor nav — `⌘S`/row arrows — remains in §3 P2 below.)*
- [x] **P2** Responsive layout to tablet widths; horizontal-scroll containment for
      the secret table (`overflow-x-auto` + `min-w`, PR #41); shell `min-w-0` guard.

---

## 8. Fills-in-later placeholders (P2 — align with other Phase-2 slices)

These currently show "Coming soon" and their own specs are separate Phase-2 work;
listed here so the *visual* treatment stays consistent when they land: **audit
viewer** *(SHIPPED — B3: chain-verify badge, filterable paginated table over new
`/v1/audit/events`, audited JSONL/CSV export downloads)*, **token management**
*(SHIPPED — B4: scoped service-token mint with show-once raw value + cascading
config/env/transit scope picker, list with scope pills, confirm-gated revoke)*,
**member management** *(SHIPPED — B4: role management at instance/project/env
scope with confirm-gated changes and server-guardrail surfacing — delegation
ceiling / last-owner / self-disable; users section with show-once one-time
password on create and disable guardrails)*, **transit UI** *(SHIPPED — B5:
`/transit` key console — create/rotate/configure `min_decryption_version`/trim/delete
with 403/409 guardrails, type/version/deletion cues — plus a plaintext-free crypto
playground: encrypt/rewrap for aes256-gcm, sign/verify for ed25519; NO decrypt/datakey,
crypto results ephemeral (local state only). Built in the dark system)*,
**usage-metrics dashboard** *(SHIPPED — D: on-demand "Reads 24h" total + top
configs/tokens from `secret.reveal` audit events; instance strip on Projects list +
project-scoped board row; dual-scope `/v1/metrics/reads-24h`; names+counts only,
hides on 403)*. Give each a
designed empty/loaded state using the component kit above rather than bespoke
markup.

---

## Suggested rollout (slices)

1. **Slice 1 — Foundations + shell** (P0 §0–§1): design tokens, typography,
   surfaces, top-bar/branding, sidebar. Instantly kills the "blank/cheap" feel.
   **→ SHIPPED** (branch `milestone-13-ui-slice1`): [`docs/superpowers/plans/2026-07-06-ui-slice1-tokens-shell.md`](docs/superpowers/plans/2026-07-06-ui-slice1-tokens-shell.md).
   Covered: §0 tokens/typography/surfaces/rhythm, §1 brand+top bar+sidebar, the
   no-raw-palette+hex test gate, plus pulled-forward token conversions of §6
   auth/unseal cards and §3/§5 dialogs+editor classes (mechanical only — their
   full redesigns stay in Slices 2–4).

   **Slice 1 review follow-ups** (from per-task code reviews; fold into the
   slices noted):
   - *Slice 3:* wrap the secret-editor table in a `div` for radius clipping
     (`overflow-hidden rounded-*` on a bare `<table>` is engine-dependent and
     breaks under `border-collapse`); replace the editor's inline badge map with
     `<Pill>` so origin tones can't desync from the tone table.
   - *P2 a11y:* mark the Brand icon `aria-hidden` when the wordmark is present
     (screen readers currently hear "Janus" twice); give Breadcrumb proper
     `<ol>/<li>` structure per the WAI-ARIA breadcrumb pattern.
   - *P2:* `envTone()` matches env *name* prefixes — a prod env renamed e.g.
     "Live" silently loses its red danger cue; consider matching on slug.
   - *B3 slice:* sidebar Audit link dead-clicks to `/` when no project is
     selected (pre-existing; give it a real home when the audit viewer lands).
   - *§5 errors:* ChangePassword surfaces `ApiError.message` verbatim — fold
     into the inline-error-surfaces work.
2. **Slice 2 — Landing + empty states** (P0 §2): dashboard/landing + reusable
   `<EmptyState>`. Kills the literal empty page.
   **→ SHIPPED** (branch `milestone-14-ui-slice2`):
   [`docs/superpowers/plans/2026-07-06-ui-slice2-landing-overview.md`](docs/superpowers/plans/2026-07-06-ui-slice2-landing-overview.md).
   Also fixed in this slice: two **API-shape mock drifts** that unit tests
   couldn't catch (`/v1/auth/me` returns `{kind,id,name}` not `{email}` —
   crashed the whole authed shell to a blank page in real browsers; sealed
   `seal-status.progress` is `{submitted,required}` not a number). Rule going
   forward: verify msw mock shapes against the Go handlers, and smoke the
   built bundle in a real browser before calling a UI slice done.
3. **Slice 3 — Secret editor polish** (P0/P1 §3) on top of a **component kit**
   (§4) and **feedback/toasts** (§5).
4. **Slice 4 — Auth/unseal polish + a11y pass** (§6–§7).
5. **Ongoing** — apply the kit to placeholder screens as those features ship (§8).

Each slice: brainstorm (confirm the look with a mockup) → spec → plan →
subagent-driven implementation, verified against the existing gates
(`npm run test`, `typecheck`, Go build/embed) — same rhythm as every milestone.
