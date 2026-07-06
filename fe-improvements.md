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

## 0. Design foundations (P0 — everything else builds on this)

A shared design system so screens stop looking ad-hoc. Today there are no tokens
at all — every component picks raw Tailwind grays.

- [ ] **P0** Define a **color palette**: slate neutral ramp for surfaces/text, one
      accent (indigo/violet — Doppler leans violet), plus semantic colors
      (success/green, warning/amber, danger/red, info/blue). Wire as Tailwind
      theme tokens in `tailwind.config.js` (e.g. `brand`, `surface`, `muted`) so
      components reference roles, not raw `gray-400`.
- [ ] **P0** **Typography scale**: a proper sans (Inter or system UI stack),
      defined sizes/weights for page title / section / body / caption; a mono
      stack (JetBrains Mono / ui-monospace) reserved for secret keys & values.
- [ ] **P0** **Surfaces & depth**: a page background tint (`slate-50`), white
      **cards** with `rounded-lg`, a hairline border **and** a soft shadow so
      content sits *on* the page instead of bleeding into it.
- [ ] **P0** **Spacing & radius rhythm**: consistent padding scale, consistent
      corner radius, consistent gaps. Pick one and apply everywhere.
- [ ] **P1** **Dark mode**: `class`-based Tailwind dark variant + a top-bar toggle,
      persisted to `localStorage`. Design tokens above should make this mostly free.
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

- [ ] **P0** **Brand mark + wordmark** in the top bar — Janus = the two-faced
      Roman god (fitting for a symmetric encrypt/decrypt product). An inline SVG
      mark + styled wordmark. Also a **favicon** and page `<title>` per route.
- [ ] **P0** **Top bar redesign**: logo left; center/left **breadcrumb**
      (Project › Environment › Config); right side a **seal-status pill**
      (green "Unsealed" / red "Sealed") and a **user menu** (avatar/initials →
      dropdown: email, change password, dark-mode toggle, log out) instead of two
      loose buttons.
- [ ] **P1** **Sidebar redesign**: section headers ("PROJECTS", "INSTANCE"),
      **icons** on each nav item, clear **active-item** highlight (accent bar +
      tint), hover states, and grouping. Replace the tiny `＋` glyphs with proper
      icon-buttons with tooltips.
- [ ] **P1** **Environment tabs** (Doppler signature): show dev/staging/prod as
      color-coded tabs/pills (prod = red-tinted as a "danger" cue) rather than a
      plain nested list.
- [ ] **P2** **Collapsible sidebar** for narrow screens; remember state.

---

## 2. Landing / dashboard — kill the blank page (P0)

The #1 "empty paper" complaint: after login you land on
`"Select or create a project to begin."` — one gray sentence on white.

- [ ] **P0** **Real landing state**: a welcoming hero with the Janus mark, a short
      value line, and prominent **"Create your first project"** / "Open recent"
      CTAs. Never a lone sentence.
- [ ] **P1** **Project overview dashboard** (once a project is selected): cards for
      each environment→config with secret counts, last-updated, and a
      **"Reads 24h"** stat (ties into Phase 2 sub-project D usage metrics),
      recent activity from the audit log, and quick actions.
- [ ] **P1** **Richer empty states everywhere** (no configs, no secrets, no
      tokens): a small illustration/icon + heading + one-line explainer + a CTA
      button. Reusable `<EmptyState>` component.
- [ ] **P2** Lightweight onboarding checklist ("create project → add environment →
      add secrets → run `janus run`") for first-time users.

---

## 3. Secret editor — the flagship screen (P0/P1)

This is where users spend their time; it should feel best. Today it's a plain
list with masked dots and a save button.

- [ ] **P0** **Table layout**: aligned columns (Key · Value · Origin · actions),
      sticky header, zebra/hover rows, monospace keys & values, comfortable row
      height. Reads like a real key/value grid.
- [ ] **P0** **Origin badges as pills**: `own` / `inherited` / `overridden` as
      colored, tooltipped pills (inherited = muted, overridden = accent) so the
      inheritance model is legible at a glance.
- [ ] **P0** **Per-row actions**: reveal (eye) with a clear audited affordance,
      **copy-to-clipboard**, edit, delete — as icon-buttons that appear/emphasize
      on row hover.
- [ ] **P1** **Dirty-state bar**: a sticky footer/banner summarizing pending
      changes ("2 changed · 1 added · 1 removed") with **Save as vN** and
      **Discard**, plus color-coded row markers for added/edited/removed.
- [ ] **P1** **Diff preview on save**: before committing a config version, show a
      before→after diff of the batch (leverages the existing versions/diff API).
- [ ] **P1** **Search / filter** secrets by key; **`.env` bulk paste/import** into
      the dirty buffer (huge Doppler-parity quality-of-life win).
- [ ] **P1** **Add-secret UX**: an inline "add row" with key+value that validates
      key format, instead of a bare input pair.
- [ ] **P2** **Reveal ergonomics**: reveal-all toggle, auto-re-mask on blur/idle,
      "copied!" confirmation, never persist revealed plaintext (keep the current
      security invariant — ephemeral state only).
- [ ] **P2** **Version history drawer**: list config versions with author/time and
      one-click rollback (there's an API for this; today it's a placeholder).
- [ ] **P2** Keyboard support: `⌘/Ctrl+S` to save, arrow/enter navigation between
      rows, `Esc` to cancel an edit.

---

## 4. Reusable component kit (P1)

Stop re-styling primitives per screen; build once, use everywhere.

- [ ] **P1** **Button** variants (primary/secondary/ghost/danger) + sizes + loading
      state, with proper focus rings.
- [ ] **P1** **Input / Select / Textarea** with consistent styling, labels, error
      text, and disabled states.
- [ ] **P1** **Modal/Dialog** (replaces the current bare create/change-password
      forms): focus-trapped, `Esc`-to-close, backdrop, header/body/footer.
- [ ] **P1** **Toast/notification** system for save success, errors, copied, etc.
      (currently no feedback surface at all).
- [ ] **P1** **Badge**, **Tooltip**, **Card**, **Tabs**, **Skeleton** loaders.
- [ ] **P2** **Dropdown menu** (Radix/headless) for the user menu and row actions.

---

## 5. Feedback, loading & error states (P1)

- [ ] **P1** **Skeleton loaders** for lists/tables instead of `"Loading…"` text.
- [ ] **P1** **Toasts** on mutations (save, delete, token create, errors).
- [ ] **P1** **Inline error surfaces**: map the API `{error:{code,message}}`
      envelope to friendly, actionable messages (not raw codes).
- [ ] **P1** **Optimistic UI** where safe; clear "saving…"/"saved vN" state on the
      save action.
- [ ] **P2** **Unsaved-changes guard** polish (styled confirm dialog, not the
      browser default) — keep the existing guard behavior.

---

## 6. Auth & unseal screens (P1)

First impression before the app even loads.

- [ ] **P1** **Login page**: centered branded card (logo, heading, styled fields,
      primary button, clear error), not a bare form. This is the literal first
      screen a user sees.
- [ ] **P1** **Unseal screen**: a focused, slightly "high-security" branded layout
      — progress ring for "k of threshold shares", clear share input, reset — so
      unsealing feels intentional. Keep shares in ephemeral state only.
- [ ] **P2** **Change-password / first-login**: friendly flow prompting the
      one-time-password change after the init ceremony.

---

## 7. Accessibility & responsiveness (P1/P2)

- [ ] **P1** Visible **focus rings**, adequate **color contrast** (esp. the current
      `text-gray-400` links — too low), proper `aria` labels/roles on icon-buttons,
      dialogs, and tabs.
- [ ] **P1** Keyboard operability end-to-end (menus, dialogs, editor).
- [ ] **P2** Responsive layout down to tablet widths; horizontal-scroll containment
      for the secret table on small screens.

---

## 8. Fills-in-later placeholders (P2 — align with other Phase-2 slices)

These currently show "Coming soon" and their own specs are separate Phase-2 work;
listed here so the *visual* treatment stays consistent when they land: **audit
viewer** (chain-verify badge + export), **token management**, **member
management**, **transit UI**, **usage-metrics dashboard**. Give each a designed
empty/loaded state using the component kit above rather than bespoke markup.

---

## Suggested rollout (slices)

1. **Slice 1 — Foundations + shell** (P0 §0–§1): design tokens, typography,
   surfaces, top-bar/branding, sidebar. Instantly kills the "blank/cheap" feel.
   **→ PLANNED:** [`docs/superpowers/plans/2026-07-06-ui-slice1-tokens-shell.md`](docs/superpowers/plans/2026-07-06-ui-slice1-tokens-shell.md)
   (branch `milestone-13-ui-slice1`). Covers: §0 tokens/typography/surfaces/rhythm,
   §1 brand+top bar+sidebar, the no-raw-palette test gate, plus pulled-forward
   token conversions of §6 auth/unseal cards and §3/§5 dialogs+editor classes
   (mechanical only — their full redesigns stay in Slices 2–4). Items below get
   checked off when the slice merges.
2. **Slice 2 — Landing + empty states** (P0 §2): dashboard/landing + reusable
   `<EmptyState>`. Kills the literal empty page.
3. **Slice 3 — Secret editor polish** (P0/P1 §3) on top of a **component kit**
   (§4) and **feedback/toasts** (§5).
4. **Slice 4 — Auth/unseal polish + a11y pass** (§6–§7).
5. **Ongoing** — apply the kit to placeholder screens as those features ship (§8).

Each slice: brainstorm (confirm the look with a mockup) → spec → plan →
subagent-driven implementation, verified against the existing gates
(`npm run test`, `typecheck`, Go build/embed) — same rhythm as every milestone.
