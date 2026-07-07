# Janus Web UI — Dark Redesign (design)

- **Status:** APPROVED 2026-07-07 (mockup reviewed and accepted by Steve).
- **Canonical mockup:** [`docs/design/ui-redesign-mockup.html`](../../design/ui-redesign-mockup.html) —
  open in a browser; theme toggle top-right (default **dark**). When any "what
  should this look like?" question arises, the mockup wins over prose. This
  mockup **supersedes** `docs/design/ui-mockup.html` as the source of truth.
- **Supersedes:** the light-only direction + the P1 dark-mode note (§7) of
  [`2026-07-06-ui-visual-design.md`](2026-07-06-ui-visual-design.md). The token
  roles, mono-for-secrets rule, env color coding, and security invariants from
  that spec **carry forward unchanged**; only the theme model and the shell/board
  layout change.
- **Scope:** visual/theme + shell/IA layer for the React SPA (`web/`). Does NOT
  change API usage, data flow, RBAC, or security invariants (masked-by-default,
  audited reveals, ephemeral plaintext, shares never logged).
- **Ordering:** this redesign ships BEFORE the B5 transit UI. B5 is then built
  directly in the new system.

## Direction (one paragraph)

A **dark-first**, Doppler-calibrated theme with a real **light/dark toggle**. Near
-black neutral surfaces with a faint violet-cool bias, exactly one accent (violet
`#6A5CF5`, lifted to `#A79CFF` for text on dark), semantic colors reserved strictly
for state, and env color coding (dev=blue / staging=amber / prod=red) as the
signature. Monospace is used **only** for secret keys/values and config names.
Dev-focused and self-hosted single-tenant: **no SaaS/paid chrome** anywhere (no
billing, plans, tenant/workspace switcher, "Try Team for Free", Refer/Community/
Share-Secret/Support, Change-Requests approval workflow, or external analytics).

## Theming architecture (the crux)

**CSS variables swapped under a `.dark` class on `<html>` (the shadcn pattern).**

- Today's tokens are hardcoded hex in `tailwind.config.js`. Convert each token to a
  CSS variable and point the Tailwind theme at the variable, e.g.
  `page: 'var(--page)'`, `ink: 'var(--ink)'`, `brand: { DEFAULT: 'var(--brand)', … }`.
- One `web/src/theme.css` (imported once) defines the two value sets:
  `:root { … light … }` and `html.dark { … dark … }`. Light values are
  **byte-identical** to the current hex, so light mode has **zero visual
  regression**; dark is purely additive.
- Because every existing component already uses semantic token classes
  (`bg-page`, `text-ink`, `border-line`, `bg-brand`, …), dark mode lights up across
  the **entire app** with near-zero component edits. This is the whole leverage of
  the approach.
- `ThemeProvider` (new, `web/src/theme/`) sets `class="dark"` on
  `document.documentElement`, persists the choice to `localStorage` (`janus.theme`
  = `light|dark|system`), and defaults to `system` (reads `prefers-color-scheme`,
  and live-updates on the media query while in `system` mode). A no-flash inline
  script in `index.html` applies the class before first paint.
- **Rejected — `dark:` variants per component:** thousands of brittle edits, no
  single source of truth. **Rejected — JS theme object:** discards Tailwind, heavier
  runtime.

### Palette gate

`web/src/test/no-raw-palette.test.ts` currently bans raw Tailwind palette classes
AND hex literals in `web/src`. It must be updated to (a) keep banning raw palette
classes/hex in **components** (`web/src/**` except the token source), and (b)
**allow** hex only inside `theme.css` (the one file that legitimately defines the
raw values). No component may read a raw hex or a `dark:` palette class.

## Design tokens

Token **roles** are unchanged from the approved system; only values gain a dark
variant. Radii (8px controls, 10px cards, 999px pills), the type scale, and the
system font stacks (sans for chrome, mono for secret material only) are unchanged.

### Light (unchanged)

`page #F6F6FA` · `sidebar/topbar/card/elevated #FFFFFF` · `border #E5E3F0` ·
`border-soft #EEECF6` · `ink #211D35` · `muted #6E6A85` · `faint #9B97B0` ·
`brand #6A5CF5` · `brand-deep/brand-text #5546E0` · `brand-soft #EFECFE` ·
`brand-line #D8D2FB` · `success #178A50`/soft `#E4F5EC` · `warning #B45309`/soft
`#FCF0DF` · `danger #C92A2A`/soft `#FBE9E9` · `info #2563EB`/soft `#E7EFFD`.

### Dark (new — calibrated to the Doppler near-black reference)

| Role | Value |
|------|-------|
| `page` | `#0B0B10` |
| `sidebar`, `topbar` | `#08080C` |
| `card` | `#15151C` |
| `elevated` (config cards, popovers, ⌘K palette, dialogs) | `#1B1B24` |
| `border` | `#26262F` |
| `border-soft` | `#1E1E27` |
| `ink` | `#ECECF2` |
| `muted` | `#9C9AAB` |
| `faint` | `#6C6A7C` |
| `brand` (fills) | `#6A5CF5` |
| `brand-text` (accent text on dark) | `#A79CFF` |
| `brand-soft` | `rgba(106,92,245,0.16)` |
| `brand-line` | `rgba(106,92,245,0.30)` |
| `success` / soft | `#3FBE7A` / `rgba(23,138,80,0.16)` |
| `warning` / soft | `#E0A253` / `rgba(180,83,9,0.18)` |
| `danger` / soft | `#F0685F` / `rgba(201,42,42,0.18)` |
| `info` / soft | `#5B8DEF` / `rgba(37,99,235,0.18)` |

Dark elevation uses **borders + surface-lightness steps**, not drop shadows
(`shadow-card` collapses to a near-invisible hairline on dark). New surface roles
`sidebar`, `topbar`, and `elevated` are added (in light they all equal `#FFFFFF`,
so no light change).

## Screen treatments (see the mockup for each)

1. **App shell** — near-black sidebar (`sidebar`), Janus split-hexagon mark +
   wordmark, dev-focused primary nav (**Projects · Activity · Members · Tokens ·
   Settings**) with 16px stroke icons, active item = `brand-soft` fill + 3px
   `brand` left rail; a divider then muted secondary links (Docs, Status). Top bar
   (`topbar`): a prominent centered **⌘K** command-palette entry, right cluster =
   theme toggle, seal-status pill (success Unsealed / danger Sealed), notifications,
   avatar-initials menu (email, change password, **theme toggle**, log out).
2. **Projects list** — page title + "New project" primary; toolbar with project
   search, sort, and a **grid/list toggle**; grid of project cards (name,
   one-line description, footer = "N configs" pill + env dots). Replaces today's
   plain Landing/overview.
3. **⌘K command palette** — `elevated` overlay, fuzzy search across projects,
   configs, secrets (key NAMES only — never values), and quick actions; grouped
   results with uppercase labels, keyboard nav (↑↓ / ↵ / esc), one highlighted row.
4. **Project board (signature screen)** — breadcrumb `Projects / <project>` with a
   copy icon, a `janus run` CLI hint; horizontal **environment columns**
   (Development=info / Staging=warning / Production=danger, each with an accent bar
   + "N configs" pill); each column has a dashed "+ Add config" button and config
   cards (mono config name + lock glyph); branch/inherited configs (e.g.
   `dev_personal`) render indented under their parent with an inheritance
   connector. The board scrolls horizontally inside its own container.
5. **Create-project modal** — `elevated` centered dialog: name input, optional
   description, plain-language helper, full-width primary "Create project".
6. **Secret editor (flagship)** — unchanged structure, dark treatment: card table
   (sticky header), masked mono values with hover reveal/copy, origin pills
   (own/inherited/overridden), pending-change rails (green added / amber edited /
   red removed + struck-through), dirty-state bar with Review diff / Discard /
   "Save as vN".
7. **Auth / unseal** — centered branded card on the page bg; Sealed pill, share
   -progress segments, masked share input, "held in memory only, never logged."
8. **Feature pages (Audit / Members / Tokens)** — inherit the token treatment for
   free; targeted spacing/elevation refinement only.

## Decomposition (4 slices — each its own plan → subagent cycle)

1. **R1 — Dual-theme foundation.** Token→CSS-var migration (light unchanged),
   dark palette, `ThemeProvider` + no-flash script + user-menu/topbar toggle +
   `localStorage` + system default, palette-gate update. **Exit:** working
   light/dark toggle across the whole existing app, no layout change; `npm run
   smoke` passes in BOTH themes.
2. **R2 — Shell & ⌘K palette.** Re-theme Sidebar to the dev-focused nav, TopBar
   with the ⌘K entry + theme toggle, and the command palette (fuzzy search over
   projects/configs/secrets + quick nav, keyboard-driven, key names only).
3. **R3 — Projects & project board.** Projects list (grid/list, config-count
   pills, search/sort), the env-columns project board (config cards, add-config,
   inheritance nesting, env accent bars), create-project modal. Absorbs today's
   Landing + ProjectOverview.
4. **R4 — Screen polish pass.** Editor, version history, audit, tokens, members,
   auth/unseal refined to the dark-first look; verify env-board integration points.

**Then:** B5 (transit UI) built directly in the new system.

## Hard rules for implementation agents

1. **Tokens only.** No raw Tailwind palette classes, no hex literals, and no
   `dark:` palette variants in `web/src` components. Hex lives ONLY in `theme.css`.
   The palette gate enforces this.
2. **Mono is for secret keys/values (and config names) only.** Never chrome.
3. **Exactly one accent** (`brand`). Semantic colors express STATE only. Env
   coding dev=info / staging=warning / prod=danger.
4. **No SaaS/paid chrome.** Single-tenant self-hosted: no billing, plans, tenant
   switcher, upgrade/trial, Refer/Community/Share-Secret/Support, Change-Requests,
   or external analytics.
5. **Match the mockup** (`ui-redesign-mockup.html`) before inventing.
6. **Both themes are first-class.** Every slice's gates include `npm run smoke`
   in BOTH light and dark; a screen that only works in one theme is a defect.
7. **Security invariants unchanged:** masked-by-default, audited reveals,
   ephemeral revealed plaintext (state only, never `localStorage`), shares never
   logged, secret VALUES never in the ⌘K index or any list/log.
8. **A11y floor:** visible `brand` focus rings, `aria-label`s on icon buttons,
   WCAG AA contrast in both themes, keyboard-operable dialogs/menus/palette,
   respect `prefers-reduced-motion`.
9. **No new runtime UI deps without discussion** beyond the existing Radix/shadcn
   -lean kit already in the repo.

## Docs & pointers to update (part of the rollout)

- `CLAUDE.md` "Web UI visual system (locked)" section: repoint the mockup +
  spec references from `ui-mockup.html` / `2026-07-06-ui-visual-design.md` to
  `ui-redesign-mockup.html` / this spec, and add the dark-first + no-`dark:`
  -in-components rule. (Do this in R1 so every subsequent agent is bound to the
  new authority.)
- `fe-improvements.md`: add a redesign section tracking R1–R4; mark the dark-mode
  P1 item as being delivered here.
- Leave `2026-07-06-ui-visual-design.md` in place as historical record with a
  banner noting it's superseded by this spec.

## Out of scope

- Backend/API changes (this is frontend-only; B5 and later handle new surfaces).
- New RBAC/auth behavior; the redesign only re-skins existing flows.
- Per-secret search inside the ⌘K palette returning VALUES (names/paths only).
- Transit UI (that's B5, after the redesign).
