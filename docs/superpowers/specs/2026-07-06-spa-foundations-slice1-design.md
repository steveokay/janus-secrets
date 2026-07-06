# SPA Slice 1 — Visual Foundations + Shell + Core Kit (implementation design)

> **RECONCILED 2026-07-06 — superseded by the executing plan.** This doc was
> authored in a parallel session; the single source of truth for Slice 1
> execution is
> [`docs/superpowers/plans/2026-07-06-ui-slice1-tokens-shell.md`](../plans/2026-07-06-ui-slice1-tokens-shell.md)
> (branch `milestone-13-ui-slice1`, in progress). Merge outcome:
> **Adopted from this doc:** the stronger enforcement gate — hex-literal ban in
> `src/`, file:line reporting, tests scanned too, gate at
> `web/src/test/no-raw-palette.test.ts`; Brand mark SVG via
> `currentColor`/`text-brand` (no hex literals).
> **Not adopted:** token naming `border`/`border-soft` and `rounded-control`
> (Task 1 already shipped `line`/`line-soft` and radius `DEFAULT`=8px per the
> visual spec's mapping note); §4 component kit and §2 empty states stay in
> Slices 3/2 per the tracker's rollout.
> **Still valid:** the banked B2 decisions below (Sheet drawer, per-version
> diff, rollback-with-confirm) carry forward as input to the B2 slice.

- **Status:** design approved 2026-07-06 (scope + mechanism approved by Steve).
- **Visual authority:** this slice *implements* the locked
  [`2026-07-06-ui-visual-design.md`](2026-07-06-ui-visual-design.md) and
  [`docs/design/ui-mockup.html`](../../design/ui-mockup.html). Where look & feel is
  in question, the mockup/spec win; this doc only covers *how* Slice 1 realizes
  them. CLAUDE.md's "Web UI visual system (locked)" rules bind every task here.
- **Tracker:** [`fe-improvements.md`](../../../fe-improvements.md) (§0 foundations,
  §1 shell, parts of §2 empty states, §4 kit).

## Goal

Establish the design-token foundation, an enforcement gate, the shell restyle to
the mockup, and the core component kit — so every subsequent slice (B2–B5 feature
screens, and the deeper Slice 3/4 restyles) is built *to the approved design*
using shared tokens and kit, never ad-hoc styling.

## Non-goals for this slice (deferred, by the approved rollout)

- The **deep, mockup-fidelity secret-editor** redesign (sticky header, row-hover
  icon actions, dirty-state bar, Import .env, filter) → **Slice 3**.
- The **auth/unseal deep restyle** + full a11y sweep → **Slice 4**.
- **Feature slices B2–B5** (version history drawer, audit viewer, token/member,
  transit UI) → after foundations. *B2 functional decisions already banked:
  slide-over `Sheet` drawer, per-version "what changed" diff, rollback-with-confirm.*
- **Dark mode** (spec §7) → later (P1); tokens are authored so it drops in cleanly,
  but no dark implementation in this slice.

In this slice, the editor/auth/unseal/create-forms/placeholder screens receive
**only a mechanical, semantics-preserving token migration** (raw palette class →
equivalent token) so the enforcement gate passes and the app stays visually
coherent — not their full redesign.

## Architecture / approach

Layered, so each unit is independently testable:

1. **Tokens** (`tailwind.config.js` + `index.css`) — no component logic.
2. **Enforcement gate** (a Vitest test) — pure static scan of source.
3. **`cn()` util + kit primitives** (`web/src/ui/`) — presentational, prop-driven.
4. **Shell** (`web/src/shell/*`) — composes tokens + kit; reads existing hooks/auth.
5. **Migration** of remaining existing screens to tokens — no behavior change.

### 1. Design tokens

Extend (never replace) the Tailwind theme in `tailwind.config.js` with the spec's
values. Color roles as `theme.extend.colors` so classes read `bg-page`,
`text-muted`, `border-border`, `bg-brand`, `text-brand-deep`, `bg-brand-soft`,
`bg-success-soft`, `text-danger`, etc. Semantic colors carry a `DEFAULT` + `soft`
(e.g. `colors.success = { DEFAULT: '#178A50', soft: '#E4F5EC' }`).

| Concern | Wiring |
|---|---|
| Colors | `page #F6F6FA`, `card #FFFFFF`, `border #E5E3F0`, `border-soft #EEECF6`, `ink #211D35`, `muted #6E6A85`, `faint #9B97B0`, `brand #6A5CF5`, `brand-deep #5546E0`, `brand-soft #EFECFE`, `brand-line #D8D2FB`; semantic `success/warning/danger/info` + `.soft` fills (spec table). |
| Fonts | `fontFamily.sans` = system stack; `fontFamily.mono` = `ui-monospace, "Cascadia Code", "SF Mono", Menlo, Consolas, monospace` (mono reserved for secret material). |
| Radius | `borderRadius`: `control: 8px`, `card: 10px` (`999px` = existing `rounded-full`). |
| Shadow | `boxShadow.card` and `boxShadow.pop` per spec. |
| Type scale | Font sizes exposed as utilities where useful; page-title/section/body/caption/section-label sizes applied in components per the spec table. `tabular-nums` via a small utility class or `font-variant-numeric`. |

`index.css` (after the `@tailwind` directives): set `body` to `bg-page text-ink`
+ sans font + antialiasing; a global `*:focus-visible` → 2px `brand` outline, 2px
offset (spec §Shape/Focus).

### 2. Enforcement gate — `web/src/test/no-raw-palette.test.ts`

A Vitest test that reads every `web/src/**/*.{ts,tsx,css}` and **fails** if it
finds a violation. Exclusions are explicit and minimal: the gate test file itself
(it contains the palette color-name regex, which would self-match) and
`tailwind.config.js` (which legitimately holds the hex token values — and lives
outside `src/` anyway). All other files, including tests and MSW handlers, are
scanned (they contain no palette classes). It **fails** if it finds:

- a raw Tailwind palette color class — regex on
  `\b(bg|text|border|ring|from|to|via|fill|stroke|divide|outline|ring-offset|placeholder|caret|accent|decoration|shadow)-(gray|slate|zinc|neutral|stone|red|orange|amber|yellow|lime|green|emerald|teal|cyan|sky|blue|indigo|violet|purple|fuchsia|pink|rose)-(50|100|200|300|400|500|600|700|800|900|950)\b`;
- a hex color literal `#[0-9a-fA-F]{3,8}` inside a `.tsx`/`.ts` string (the Janus
  mark SVG uses `currentColor`/token classes, not literals; `tailwind.config.js`
  legitimately holds the hex token values and is **not** scanned).

The test reports offending file + line + match so failures are actionable. This
runs in `npm run test` (the existing gate) — no ESLint tooling added. A companion
assertion confirms the scan actually covers a known-token file (guard against the
glob silently matching nothing).

### 3. `cn()` + component kit → `web/src/ui/`

shadcn conventions: `web/src/ui/cn.ts` = `clsx` + `tailwind-merge`; `cva` for
variants. Each primitive is one file, token-only classes, `aria`-correct.

- **Radix-backed** (a11y-hard): `Dialog`, `Sheet` (right slide-over — B2's drawer),
  `DropdownMenu`, `Toast` + `<Toaster/>` + a `toast()` helper, `Tooltip`.
- **Hand-rolled token'd:** `Button` (variants: primary/secondary/ghost/danger +
  sizes + loading), `Input`, `Select`, `Badge`/`Pill` (variants for origin +
  semantic + env), `Card`, `Tabs`, `EmptyState` (icon + heading + text + CTA),
  `Skeleton` (reduced-motion-safe shimmer).
- **Icons:** `lucide-react` (tree-shaken), 1.7px stroke to match the mockup; the
  Janus split-hexagon mark is a small inline SVG component (`ui/BrandMark.tsx`)
  copied verbatim from the mockup, colored via `currentColor`/tokens.

New deps (dev + runtime as appropriate), pinned: `@radix-ui/react-dialog`,
`@radix-ui/react-dropdown-menu`, `@radix-ui/react-toast`, `@radix-ui/react-tooltip`,
`class-variance-authority`, `clsx`, `tailwind-merge`, `lucide-react`.

Each kit primitive gets a focused test (render, variant class presence, and for
Radix ones: opens/closes, `Esc` closes, focus trap present, `aria` wired).

### 4. Shell restyle (to the mockup)

- **`TopBar`**: `BrandMark` + "Janus" wordmark; breadcrumb `project / env / config`
  derived from the route (reuse the `matchPath` approach already used in Sidebar —
  no `useParams` at shell level); right side: seal pill (`success` "Unsealed" /
  `danger` "Sealed"), and an avatar-initials `DropdownMenu` (email, Change
  password, Log out; dark-mode toggle placeholder disabled/omitted this slice).
- **`Sidebar`**: white `card` surface; `PROJECT` / `ENVIRONMENTS` / `INSTANCE`
  uppercase section labels (`faint`); project selector as a bordered control; env
  groups with color dots (dev=info, staging=warning, prod=danger; custom→info) and
  config children; **active config** = `brand-soft` fill + 3px `brand` left rail;
  INSTANCE items (Audit/Tokens/Members/Transit/Settings) with lucide icons. Keep
  the existing create-form affordances and `useActiveProjectId` logic; restyle
  only.
- **`AppLayout`**: `bg-page`, spacing/rhythm per mockup; main content in the
  content column.
- **Landing / empty states**: replace the two lone-sentence routes ("Select or
  create a project", "Select a config") with `EmptyState` (branded, icon +
  heading + explainer + CTA). Add `EmptyState` to obviously-empty lists
  encountered by the shell (no projects yet, no configs yet).

Shell behavior (routing, auth, seal gating, create flows) is unchanged — this is a
restyle over the existing structure; existing shell tests keep passing (updated
only where markup/labels they assert on legitimately change).

### 5. Migrate remaining existing screens to tokens

Mechanical, semantics-preserving pass over `SecretEditor`, `LoginPage`,
`ChangePassword`, `UnsealPage`, `CreateForms`, `Placeholder`, and any other
`web/src` component still using raw palette classes: swap each raw class for its
token equivalent (`text-gray-400`→`text-faint`, `bg-blue-600 text-white`→`Button`
primary or `bg-brand text-white`, origin badge fills → `success`/`brand-soft`/
`warning` soft-fills, red→`danger`, etc.). No structural/behavioral change; their
deep redesigns remain Slice 3/4. Goal: the palette gate passes repo-wide and the
app is visually consistent.

## File structure

```
web/
  tailwind.config.js         # + theme.extend tokens (colors/fonts/radius/shadow)
  src/
    index.css                # base page/ink/font + focus-visible ring
    ui/
      cn.ts                  # clsx + tailwind-merge
      BrandMark.tsx          # Janus split-hexagon SVG (verbatim from mockup)
      Button.tsx Input.tsx Select.tsx Badge.tsx Card.tsx Tabs.tsx
      EmptyState.tsx Skeleton.tsx
      Dialog.tsx Sheet.tsx DropdownMenu.tsx Toast.tsx Tooltip.tsx
      *.test.tsx             # per-primitive tests
    shell/
      TopBar.tsx Sidebar.tsx AppLayout.tsx   # restyled to mockup
    test/
      no-raw-palette.test.ts # the enforcement gate
    (secrets|auth|unseal|structure)/*        # token-migrated in place
```

## Error handling & security

- No API/route/data-flow changes; **security invariants unchanged** (masked by
  default, reveal/copy audited, revealed plaintext ephemeral in component state
  only, no plaintext in storage, shares never logged). The `Toast` copy affordance
  (added to the kit) is wired for real use only in later slices; where used, a
  reveal/copy of a value remains the existing audited path.
- Empty/error/loading states use `EmptyState`/`Skeleton`/inline token'd text — the
  "every list gets an empty state; every mutation gets feedback" rule starts here.

## Testing & gates

- **Kit:** per-primitive Vitest + RTL tests (render, variants; Radix: open/close,
  `Esc`, focus trap, `aria`).
- **Gate:** `no-raw-palette.test.ts` green (i.e., the whole `web/src` is
  token-clean) — this is the milestone's definition-of-done for the migration.
- **Regression:** all existing web tests still pass (updated only where restyle
  legitimately changes asserted markup/labels); `npm run typecheck` clean.
- **Go:** `go build ./...`, `go vet ./...`, `go test ./internal/web ./internal/api`;
  `make build` produces the embedded binary with the restyled assets.
- **Security scanners** (`gosec`, `govulncheck`) unaffected (no Go changes beyond
  possibly none); crypto/authz/audit coverage untouched.

## Rollout note

On completion, update `fe-improvements.md` (check off §0 tokens, §1 shell, the
§2 empty-state item, §4 kit primitives built), `docs/web.md` (note the design
system + kit location), and `status.md`. Next milestone after this: **B2** —
config version history drawer, built on the `Sheet` + `Dialog` + `Toast` kit and
the tokens, per the banked functional design.
