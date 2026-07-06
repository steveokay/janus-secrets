# Janus Web UI — Visual Design System (approved)

- **Status:** APPROVED 2026-07-06 (mockup reviewed and accepted by Steve).
- **Canonical mockup:** [`docs/design/ui-mockup.html`](../../design/ui-mockup.html) — open it in a browser. When any question of "what should this look like?" arises, the mockup wins over prose.
- **Scope:** visual/theme layer for the React SPA (`web/`). It does not change routes, data flow, API usage, or the security invariants (masked-by-default, audited reveals, ephemeral plaintext).
- **Tracker:** [`fe-improvements.md`](../../../fe-improvements.md) holds the punch-list; this spec is the visual authority those slices implement.

## Direction (one paragraph)

A modern, polished, Doppler-inspired **light-first** theme with a P1 dark mode. Violet-biased neutrals, exactly one accent (violet `#6A5CF5`), semantic colors reserved strictly for state. Monospace type is used **only** for secret keys/values — the mono-vs-sans texture contrast is the product's typographic signature. Content sits on a tinted page in white cards with hairline borders and soft shadows. Production always reads as "danger-adjacent" via env color coding.

## Design tokens

These are wired into `tailwind.config.js` as theme tokens in Slice 1. **Components reference token roles, never raw Tailwind palette classes.**

### Color — neutrals & brand

| Token      | Hex       | Use |
|------------|-----------|-----|
| `page`     | `#F6F6FA` | app/page background (violet-tinted, never pure white) |
| `card`     | `#FFFFFF` | card & table surfaces |
| `border`   | `#E5E3F0` | hairline borders |
| `border-soft` | `#EEECF6` | row separators, subtle dividers |
| `ink`      | `#211D35` | primary text |
| `muted`    | `#6E6A85` | secondary text |
| `faint`    | `#9B97B0` | tertiary text, placeholders, section labels |
| `brand`    | `#6A5CF5` | THE accent: primary buttons, active nav rail, focus rings, links |
| `brand-deep` | `#5546E0` | accent text on soft backgrounds, hover |
| `brand-soft` | `#EFECFE` | accent tint fills (active nav, `overridden` pill) |
| `brand-line` | `#D8D2FB` | accent-tinted borders (dirty bar) |

### Color — semantic (state only, never decoration)

| Token     | Hex       | Soft fill | Use |
|-----------|-----------|-----------|-----|
| `success` | `#178A50` | `#E4F5EC` | unsealed pill, `own` pill, added rows, chain-verified badge |
| `warning` | `#B45309` | `#FCF0DF` | edited rows, staging env |
| `danger`  | `#C92A2A` | `#FBE9E9` | sealed pill, removed rows, prod env, destructive actions |
| `info`    | `#2563EB` | `#E7EFFD` | dev env, inheritance hints |

### Environment colors (Doppler signature)

`development` = info blue · `staging` = warning amber · `production` = danger red. Repeated consistently: sidebar env dot, config page-header pill, destructive confirms. Custom envs beyond these get assigned from the same semantic set at creation (default: info).

### Typography

System stacks only — no webfonts, nothing to load or embed.

- Sans (all chrome): `-apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, "Helvetica Neue", Arial, sans-serif`
- Mono (secret keys/values ONLY): `ui-monospace, "Cascadia Code", "SF Mono", Menlo, Consolas, monospace`

| Role | Size / weight | Notes |
|------|---------------|-------|
| page title | 17px / 600 | letter-spacing −0.01em |
| section heading | 14px / 600 | |
| body | 13–13.5px / 400 | |
| caption / meta | 12–12.5px / 400 | color `faint` |
| section label | 10.5px / 700 | UPPERCASE, letter-spacing .12em, color `faint` |
| secret key | mono 12.5px / 600 | |
| secret value | mono 12.5px / 400 | masked dots get letter-spacing .08em |

Numbers that align in columns (versions, counts) use `tabular-nums`.

### Shape, depth, motion

- Radius: **8px** controls/inputs, **10px** cards, **999px** pills. Nothing else.
- Shadows: `card` = `0 1px 2px rgba(33,29,53,.05), 0 4px 16px rgba(33,29,53,.05)`; `pop` (dialogs, toasts) = `0 4px 10px rgba(33,29,53,.08), 0 16px 40px rgba(33,29,53,.12)`.
- Motion: 150ms ease on hover/focus/expand only; respect `prefers-reduced-motion`.
- Focus: visible 2px `brand` outline with 2px offset on every interactive element.

## Screen treatments (see mockup for each)

1. **Top bar:** split-hexagon Janus mark (SVG in the mockup — reuse it verbatim) + wordmark, breadcrumb `project / env / config`, right side seal pill (`success` Unsealed / `danger` Sealed) + notifications + avatar-initials menu (email, change password, dark-mode toggle, log out).
2. **Sidebar:** white surface, sections PROJECT / ENVIRONMENTS / INSTANCE with uppercase labels; project selector as a bordered control; env groups with color dots and config children; active config = `brand-soft` fill + 3px `brand` left rail; INSTANCE nav items with 16px stroke icons.
3. **Secret editor (flagship):** card table (sticky header row on `#FBFBFD`), columns Key · Value · Origin · Ver · actions; origin pills (`own`=success, `inherited`=muted gray, `overridden`=brand); row-hover icon actions (reveal / copy / edit / delete); pending-change rows get a 3px left rail (green added / amber edited / red removed) plus pill; removed rows strike through at 45% opacity; dirty-state bar below the table (`brand-line` border) with "+n added · n changed · n removed", Review diff, Discard, **Save as vN** primary button; toolbar with key filter, Import .env, History.
4. **Feedback:** dark toast (`#262238`) bottom-right for save/copy/errors — copy events say "read audited"; skeleton loaders (shimmer, reduced-motion-safe) instead of "Loading…" text.
5. **Auth/unseal:** centered 330px branded cards on a soft violet-graded stage; unseal card shows share-progress segments ("2 of 3 shares"), `danger` Sealed pill, and the line "Shares are held in memory only and never logged."
6. **Empty states:** icon + heading + one-line explainer + CTA button via a reusable `<EmptyState>`; never a lone gray sentence.
7. **Dark mode (P1):** class-based Tailwind variant, user-menu toggle, persisted to `localStorage`. Surfaces `#141221` page / `#1D1A2F` card / `#2C2944` border, ink `#EAE8F4`, muted `#8A86A3`, accent lifted to `#A79CFF` for text on dark, semantic soft-fills become ~18% alpha overlays (see mockup's dark section).

## Hard rules for implementation agents

1. **No raw Tailwind palette classes in `web/src`** (`gray-400`, `blue-600`, `slate-*`, hex literals, …). Use theme tokens only. Slice 1 adds a lint/test gate for this.
2. **Mono is for secret material only.** Never for buttons, nav, headings, or metadata.
3. **Semantic colors express state**, never decoration. The only decorative color is `brand`.
4. **Match the mockup** (`docs/design/ui-mockup.html`) before inventing. If a screen or component isn't in the mockup, compose it from the tokens + component kit and keep it quieter than the mockup screens, not louder.
5. **Security invariants unchanged:** values masked by default, reveal/copy audited, revealed plaintext ephemeral (state only), no plaintext in localStorage, shares never logged.
6. **Every list gets an empty state; every mutation gets feedback** (toast or inline).
7. **A11y floor:** visible focus rings, `aria-label`s on icon buttons, WCAG AA contrast (the old `text-gray-400`-on-white pattern is what we're deleting), keyboard-operable dialogs/menus.
8. **No new runtime UI dependencies without discussion.** Exception noted below.

## Open decision (carried from fe-improvements §0)

Hand-rolled Tailwind components vs **shadcn/ui** (Radix primitives, copied into repo, no runtime dep beyond Radix). Recommendation: shadcn/ui for Dialog/DropdownMenu/Toast/Tooltip; decide at Slice 1 planning. Icons: lucide-react (tree-shaken) or inline SVGs matching the mockup's 1.7px-stroke style.

## Rollout

Implemented via the slices in `fe-improvements.md` (§ "Suggested rollout"): Slice 1 tokens+shell → Slice 2 landing/empty states → Slice 3 editor+kit+feedback → Slice 4 auth/a11y. Each slice: spec'd plan → subagent implementation → existing gates (`npm run test`, typecheck, Go build/embed).
