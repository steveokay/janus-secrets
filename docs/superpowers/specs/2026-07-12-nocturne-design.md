# Janus Web UI — Nocturne Redesign (design)

This is the design spec for the **Nocturne** redesign — phase N1 of the redo
program documented in [`ui-redo.md`](../../../ui-redo.md). Nocturne is direction
**A** (layered depth) with the home **command center** layout, restyling the app
and designing the missing screens (`gaps.md` §1–2) in one new visual language.

**This spec supersedes** [`docs/superpowers/specs/2026-07-07-dark-redesign-design.md`](2026-07-07-dark-redesign-design.md)
(the "dark redesign"), which in turn superseded the earlier light-only direction.
The old spec + its mockup are kept in-tree for history but are no longer
authoritative.

- **Canonical mockup (source of truth for look & feel):** [`docs/design/ui-nocturne-mockup.html`](../../design/ui-nocturne-mockup.html) —
  open in a browser; dark-first + light toggle. It shows the app shell, the
  secret editor, the home command center, and both themes. When any "what should
  this look like?" question arises, the mockup wins over prose.
- **Program doc:** [`ui-redo.md`](../../../ui-redo.md).
- **Per-phase rollout (N1–N7):** [`ui-redo.md`](../../../ui-redo.md) §5.

---

## Tokens

All tokens live as CSS variables in `web/src/theme.css` — the **only** file that
may contain raw hex. Components consume the token *roles* via Tailwind classes
that resolve to these variables. The table below transcribes the current values;
`theme.css` remains the authority if it drifts.

| Role | Light (`:root`) | Dark (`html.dark`) |
| --- | --- | --- |
| **Canvas** | | |
| `--canvas` | `linear-gradient(160deg, #FAFAFC 0%, #F4F2FA 100%)` | `linear-gradient(160deg, #0B0A14 0%, #12101F 60%, #150F26 100%)` |
| `--canvas-base` (solid; smoke reads this) | `#F6F6FA` | `#0B0A14` |
| **Surfaces** (translucent layers) | | |
| `--surface-1` | `rgba(20,17,35,.02)` | `rgba(255,255,255,.02)` |
| `--surface-2` | `rgba(20,17,35,.03)` | `rgba(255,255,255,.03)` |
| `--surface-3` | `rgba(20,17,35,.05)` | `rgba(255,255,255,.05)` |
| **Ink scale** | | |
| `--ink-hi` | `#17151F` | `#FFFFFF` |
| `--ink` (DEFAULT) | `#211D35` | `#E9E5FF` |
| `--ink-body` | `#3F3F50` | `#C8C4DE` |
| `--ink-mute` | `#6E6A85` | `#8D87AB` |
| `--ink-faint` | `#8A86A3` | `#6F6A8E` |
| **Brand** | | |
| `--brand` | `#6A5CF5` | `#8B5CF6` |
| `--brand-deep` | `#5546E0` | `#6D28D9` |
| `--brand-text` (AA foreground) | `#5546E0` | `#C4B5FD` |
| `--brand-soft` | `#EFECFE` | `rgba(139,92,246,.16)` |
| `--brand-line` | `#D8D2FB` | `rgba(139,92,246,.30)` |
| `--brand-grad` | `linear-gradient(135deg, #7C3AED, #6D28D9)` | `linear-gradient(135deg, #8B5CF6, #7C3AED)` |
| `--on-brand` (text on gradient) | `#FFFFFF` | `#FFFFFF` |
| `--nav-active` | `linear-gradient(90deg, rgba(124,58,237,.14), rgba(124,58,237,.03))` | `linear-gradient(90deg, rgba(139,92,246,.22), rgba(139,92,246,.05))` |
| `--nav-rail` | `#7C3AED` | `#8B5CF6` |
| **Dirty state** | | |
| `--dirty-wash` | `linear-gradient(90deg, rgba(180,83,9,.10), transparent)` | `linear-gradient(90deg, rgba(245,158,11,.08), transparent)` |
| `--dirty-rail` | `#B45309` | `#F59E0B` |
| **Semantic** | | |
| `--ok` / `--ok-soft` | `#178A50` / `#E4F5EC` | `#4ADE80` / `rgba(74,222,128,.14)` |
| `--warn` / `--warn-soft` | `#B45309` / `#FCF0DF` | `#FCD34D` / `rgba(245,158,11,.16)` |
| `--danger` / `--danger-soft` | `#C92A2A` / `#FBE9E9` | `#FB7185` / `rgba(244,63,94,.16)` |
| `--info` / `--info-soft` | `#2563EB` / `#E7EFFD` | `#7DB2FF` / `rgba(56,132,255,.16)` |
| **Elevation & glow** | | |
| `--elev-1` | `0 1px 2px rgba(33,29,53,.05), 0 4px 16px rgba(33,29,53,.06)` | `0 8px 24px rgba(0,0,0,.40)` |
| `--elev-2` | `0 4px 10px rgba(33,29,53,.08), 0 16px 40px rgba(33,29,53,.12)` | `0 10px 30px rgba(0,0,0,.50)` |
| `--glow-brand` | `0 0 14px rgba(124,58,237,.20)` | `0 0 14px rgba(139,92,246,.50)` |
| `--glow-brand-soft` | `0 4px 14px rgba(124,58,237,.16)` | `0 4px 14px rgba(139,92,246,.40)` |

Glows are at ~40% strength on light per §3.8 of the mockup.

**Legacy role aliases** (kept so existing components keep resolving; new work
should prefer the roles above): `--page`→canvas-base, `--sidebar`/`--topbar`/
`--card`→surface layers, `--elevated` (solid popover surface), `--border`/
`--border-soft`, `--muted`→ink-mute, `--faint`→ink-faint, `--shadow-card`→elev-1,
`--shadow-pop`→elev-2.

### Radii

Radii are token scale values (rounded-token classes in Tailwind); components use
the token classes, never ad-hoc pixel radii. Corner radius follows the mockup's
card/control/pill scale — see `web/tailwind.config` / `theme.css` for the exact
`--radius-*` bindings and the mockup for applied usage.

---

## Hard rules

Carried forward from the dark-redesign spec, with Nocturne additions:

1. **Tokens only.** All color / type / radius / shadow come from CSS variables in
   `web/src/theme.css`. Components use token *classes* only — **never** raw
   palette classes (`gray-400`, `blue-600`), **never** `dark:` palette variants,
   **never** hex literals outside `theme.css`. Enforced by
   `web/src/test/no-raw-palette.test.ts`.

2. **Brand foreground.** Never use the class `text-brand-deep` as a foreground —
   it fails AA on dark. Use `text-brand-text` for brand-colored text. Enforced by
   `web/src/test/dark-aa.test.ts`. Primary and gradient buttons put their label in
   `text-on-brand` (white, AA in both themes on the violet gradient).

3. **Dual theme.** Both light and dark must render correctly. Checked by
   `npm run smoke` (reads `--canvas-base` in both themes). Every UI change is
   verified in both.

4. **One accent.** A single violet `brand` accent. Semantic green / amber / red
   express **state only**, never decoration. Environment color coding:
   **dev = blue, staging = amber, prod = red.** Exception: project glyph
   gradients (`--glyph-a…d`) are *identity* coding, not state — they may use
   non-brand hues but must never reuse the danger family.

5. **Monospace scope.** Monospace is reserved for secret keys / values **and
   audit action codes**. All chrome uses sans.

6. **Glow budget.** Max ~3 glowing elements per screen. Glow signals focus /
   primary action / live state — it is not ambient decoration.

7. **Density.** Every card and every row carries a metadata line — version /
   count / relative-time / actor — so no surface reads as empty. Nocturne is a
   dense, information-forward console, not marketing whitespace.

When a visual question isn't answered by the mockup or this spec, ask — don't
improvise a new style.

---

## Pointers

- **Canonical mockup:** `docs/design/ui-nocturne-mockup.html`
- **Program doc:** `ui-redo.md`
- **Per-phase rollout N1–N7:** `ui-redo.md` §5
- **Superseded (kept for history):** `docs/design/ui-redesign-mockup.html` +
  `docs/superpowers/specs/2026-07-07-dark-redesign-design.md`
