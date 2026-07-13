# UI Redo — "Nocturne" redesign instructions

_Decided 2026-07-12 via visual brainstorm (mockups persisted in `.superpowers/brainstorm/1307-1783872022/content/`). Owner picked: direction **A — Nocturne** (layered depth) over Vault-grade and Porcelain; home layout **1 — Command center**; scope **restyle + new screens** (the missing surfaces from `gaps.md` get designed once, in the new language)._

This document is the redo instruction set. It supersedes the visual direction of `docs/design/ui-redesign-mockup.html` + `docs/superpowers/specs/2026-07-07-dark-redesign-design.md` once phase N1 lands (see §7 for the authority handover). Companion docs: `gaps.md` (what's missing), `fe-improvements.md` (old punch-list — folded in or retired by this).

---

## 1. What stays (do not rebuild)

- **Token architecture.** All color/type/radius/shadow via CSS variables in `web/src/theme.css`; components use token classes only. `no-raw-palette.test.ts` and the dark-AA guard test stay and get updated expectations, not deleted.
- **Dual theme.** Light theme remains a first-class variant (CLAUDE.md requirement; `npm run smoke` checks both). Nocturne is dark-first; §3.8 defines the light derivation.
- **Security behaviors — invariant, re-verify after every phase:**
  - Secrets masked by default; editor never reveals on mount; plaintext only in ephemeral state, cleared on unmount/close.
  - Every reveal (per-key or bulk) hits the audited raw endpoints.
  - Command palette indexes key *names* only. Review-diff stays value-free.
  - Once-shown credentials (minted tokens, initial passwords, issued dynamic creds) only via `RevealOnce` / `IssuedCredsModal` patterns.
- **Data layer.** TanStack Query hooks, `web/src/lib/endpoints.ts`, msw mock-drift rule (mocks mirror Go wire shapes exactly).
- **Behavioral test suite** (207+ tests). Restyling must not change semantics; tests asserting classes/colors update to tokens, tests asserting behavior must keep passing untouched.

## 2. Why (diagnosis being fixed)

The current UI reads "basic" for four reasons, all addressed here: flat single-layer surfaces with no elevation language; near-zero information density (cards/tables show names only); zero motion; and a design authority (the old mockup) that itself aimed low. This redo raises the ceiling *and* fills the depth gaps from `gaps.md` §1–2 in the same pass so screens aren't restyled twice.

## 3. The Nocturne design language

Reference renders: `visual-direction.html` (card A) and `nocturne-home.html` (card 1) in the brainstorm session directory. Phase N1 turns this section into `web/src/theme.css` tokens + a canonical mockup page.

### 3.1 Canvas & layering
- App canvas: gradient, not flat — `linear-gradient(160deg, #0b0a14 0%, #12101f 60%, #150f26 100%)` (token: `--canvas`). Fixed attachment; content scrolls over it.
- Surfaces are translucent layers over the canvas, not opaque grays:
  - `--surface-1: rgba(255,255,255,.02)` (sidebar, rails)
  - `--surface-2: rgba(255,255,255,.03)` (cards, tables)
  - `--surface-3: rgba(255,255,255,.05)` (pills, inputs, hover fills)
- Borders: `--line-1: rgba(255,255,255,.06)`, `--line-2: rgba(255,255,255,.08)`. Hairline 1px everywhere; no 2px borders except state rails.

### 3.2 Elevation & glow
- `--elev-1: 0 8px 24px rgba(0,0,0,.4)` (cards) · `--elev-2: 0 10px 30px rgba(0,0,0,.5)` (floating bars, popovers) · modals add a backdrop blur (`backdrop-filter: blur(8px)` on overlay).
- Glow is the signature: active/branded elements carry a soft violet bloom — `--glow-brand: 0 0 14px rgba(139,92,246,.5)` (logo mark, primary buttons at rest use a weaker `0 4px 14px rgba(139,92,246,.4)`); prod chips may carry `0 0 10px rgba(244,63,94,.15)`. Use sparingly: max ~3 glowing elements per screen.

### 3.3 Accent & state
- Brand: violet gradient, never flat — `linear-gradient(135deg, #8b5cf6, #7c3aed)` for primary buttons, `#6d28d9` deep end for marks. Text-on-dark accents: `#c4b5fd` / `#e9e5ff`.
- Active nav item: gradient wash `linear-gradient(90deg, rgba(139,92,246,.22), rgba(139,92,246,.05))` + `inset 2px 0 0 #8b5cf6` rail.
- Dirty/edited state: amber — row wash `linear-gradient(90deg, rgba(245,158,11,.08), transparent)` + `inset 2px 0 0 #f59e0b`.
- Semantic: ok `#4ade80`, warn `#fcd34d`, danger `#fb7185` (dark theme values).
- Env coding (unchanged rule, new execution): translucent chip + tinted border, dev `rgba(56,132,255,…)`/`#7db2ff`, staging `rgba(245,158,11,…)`/`#fcd34d`, prod `rgba(244,63,94,…)`/`#fb7185`.

### 3.4 Text hierarchy (dark values)
`--ink-hi: #ffffff` (headings, emphasized values) · `--ink: #e9e5ff` (primary content) · `--ink-body: #c8c4de` · `--ink-mute: #8d87ab` · `--ink-faint: #6f6a8e` (metadata, labels). Labels: 9–10px uppercase, letter-spacing 1–1.5px. Monospace stays reserved for secret keys/values and audit action codes.

### 3.5 Shape
Radius scale: 12px cards/modals, 10px floating bars, 7–8px buttons/nav items, 99px pills/chips. Logo mark: 22px rounded-square (7px radius) with brand gradient + glow.

### 3.6 Motion (finally shipping §0)
- Global: 150ms ease-out on background, border, box-shadow, color, transform. Hover on cards/rows: surface tint shift (`rgba(139,92,246,.05)` for interactive rows) — no scale on tables; cards may lift `translateY(-1px)` + elevation step.
- Enter animations: panels/modals fade+4px rise, 180ms. Save-bar slides up when dirty.
- All inside `@media (prefers-reduced-motion: no-preference)`; the stub in `theme.css` becomes real.

### 3.7 Density
Comfortable-dense: table rows ~34–36px, cards padded 12–14px, 12px base font in data surfaces, 13px in chrome. Every card/row carries a metadata line (`--ink-faint`, 10px): version, count, relative time, actor — this is the density fix from gaps.md §2, it is not optional decoration.

### 3.8 Light theme derivation
Same layering *logic* on a light canvas: canvas `linear-gradient(160deg,#fafafc,#f4f2fa)`, surfaces `rgba(20,17,35,.02/.03/.05)`, lines `rgba(20,17,35,.07/.10)`, ink scale inverts to `#17151f → #8a86a3`, glows drop to 40% alpha, semantic/env chips use the tinted-fill+border pattern with darkened text (AA on light). Dark-AA guard test gains light-theme assertions. Both themes smoke-checked per phase.

## 4. Screen-by-screen instructions

### 4.1 NEW — Home: Command center (route `/`)
The landing page after login (reference: `nocturne-home.html` card 1):
- Header: time-of-day greeting + instance line (project count, secret count, audit chain `✓ verified` from `GET /v1/audit/verify`).
- Four stat cards: **Reads 24h** (`/v1/metrics/reads-24h`; the endpoint returns per-config/per-token totals, not hourly buckets — render total + top-3 configs mini-bars from what it returns today; a true hourly sparkline needs a small backend addition and is optional), **Rotations** (healthy/failing count + next due), **Syncs** (health + failing target one-liner), **Leases** (active count + soonest expiry). Reuse the ops console's 403-tolerant aggregation (`useAggregated`); cards hide (not error) on 403 like the existing ReadsStrip.
- Project cards: glyph (deterministic color from slug hash, brand-gradient family), name, key count, relative freshness, per-env version chips (`dev v9 · staging v11 · prod v14`) linking straight into the board.
- Recent activity: last ~8 audit events, `action` in mono + resource path + actor + relative time, result-colored dot. **Names/paths only — never values** (same constraint as the audit viewer).
- ProjectsList moves to `/projects`; sidebar gains a Home item above Projects.

### 4.2 Projects (`/projects`) & Project board
- Projects: enriched cards (glyph, description, env chips, last-modified, quick-action menu: open/board/members/delete), sort by activity | name, grid/list persisted.
- Board: config cards gain version, key count, author, freshness; branch configs get a tinted left rail + connector to their root; prod column cards get the subtle red border tint; rotation/sync health chips on configs that have policies/targets. Env column headers get inline rename/describe.

### 4.3 Secret editor (flagship — restyle only in this pass)
Nocturne treatment per `visual-direction.html` card A: layered table card, hover row tint, amber dirty rails + gradient wash, origin/version pills in `--surface-3`, floating save-bar with violet border glow ("N changes · will create vN+1" / Review diff / Save as vN+1). Header adds the metadata line (version, key count, updated-by). Behavioral upgrades (sorting, bulk ops, keyboard nav, import preview) are **out of scope here** — they're gaps.md §2.1, tracked as a separate depth pass so the restyle stays low-risk.

### 4.4 Audit, Tokens, Members, Transit, Operations (restyle + small wins)
- All tables: Nocturne card treatment, sticky headers (the secret table pattern, applied everywhere), uppercase micro-labels for column heads.
- Audit: result-colored dots, mono action codes, chain-verified badge gets the glow treatment when green.
- Operations: status pills adopt semantic chips; truncated `last_error` becomes expandable inline. (Create flows: §4.6.)
- Tokens/Members: metadata lines (created, scope, role counts); no new behavior.

### 4.5 NEW — Settings hub (`/settings`, replaces the Placeholder)
Left subnav within the page: **Instance** (version/build once `GET /v1/sys/version` exists — render conditionally until then; seal status + Seal button with typed-confirm; backup download via `GET /v1/sys/backup`), **OIDC provider** (view/edit/delete `/v1/sys/oidc` — client secret write-only, never re-displayed), **CI federation** (config + trust bindings CRUD `/v1/sys/oidc/federation*`), **Appearance** (theme default). All sections 403-hidden per capability, same pattern as ops console. This closes gaps.md 1.1/1.3/1.4/1.6.

### 4.6 NEW — Operations create flows
Sheet-based create forms (reuse `Sheet`/kit): rotation policy (type postgres|webhook + type-specific fields), sync target (github|kubernetes), dynamic role (SQL templates in mono textareas with `{{name}}`/`{{password}}`/`{{expiration}}` hint chips). Secrets-bearing fields (DSNs, PATs, kubeconfigs) are write-only password inputs. Closes gaps.md 1.5 and removes the "use the CLI" copy.

### 4.7 NEW — resilience surfaces
- App-level ErrorBoundary (Nocturne card: "Something broke", error digest, reload + copy-details) wrapping the router outlet.
- Real 404 route (glyph, "Not found", back-home button) replacing the silent redirect.

### 4.8 Auth & unseal
AuthCard gets the Nocturne canvas + glowing logo mark; unseal keeps the share-segments progress (do not regress to a ring; share values still cleared before await). Add the OIDC login button **behind `GET /v1/auth/oidc/status`** — render only when a provider is configured. (Button is part of this redo; the backend flow already exists.)

### 4.9 Shell & palette
Sidebar per mockups: glyph logo with glow, gradient-wash active items, Home/Projects/Operations/Transit/Audit/Settings order. Top bar: breadcrumb, ⌘K pill, theme toggle, user menu. Palette: restyle to Nocturne; add action commands (New project, Export audit, Toggle theme, Go to Settings) — still names-only for secrets.

## 5. Rollout phases (each = one PR, gated)

Gates for every phase: `npm test` green, `npm run smoke` (both themes), no-raw-palette + dark-AA guards, tsc clean, security re-check of §1 invariants on touched screens.

- **N1 — Tokens, shell, motion.** New `theme.css` (dark+light Nocturne tokens), canvas gradient, elevation/glow utilities, motion system, sidebar/topbar/palette restyle, kit primitives (Button/Input/Card/Pill/Modal/Sheet/Toast) re-skinned. Author `docs/design/ui-nocturne-mockup.html` as the new canonical mockup (app shell, editor, home, both themes) and the spec update. **This is the authority-swap PR: update CLAUDE.md's design pointers in it.**
- **N2 — Home command center + `/projects` + board enrichment** (§4.1, §4.2). New route map (`/` = home, `/projects` = list). **N2 — DONE (2026-07-13):** home command center, /projects move, board ops chips. 4th stat card = audit chain (backend requires role_id for lease listing; instance-wide leases deferred).
- **N3 — Secret editor restyle** (§4.3) + version-history/diff surfaces. **N3 — DONE (2026-07-13):** secret editor restyle — layered table card, row washes + hover, semantic rails kept, metadata line (v/keys/updated-by), glow save-bar, kit-Button unification across toolbar+dialogs+history; riders: teal glyph-d + spec glyph carve-out, composed HomePage seam test + verify-dedupe pin.
- **N4 — Audit/Tokens/Members/Transit/Operations restyle** (§4.4) + sticky headers + error-expand. **N4 — DONE (2026-07-13):** all five admin/ops surfaces restyled to Nocturne tokens + kit primitives; page-scrolled tables (audit/tokens/members) got sticky headers (removed the `overflow-hidden` that was silently defeating `position:sticky`); the ops `OpsTable` stays non-sticky by design (its `overflow-x-auto` ancestor can't host a sticky header cheaply); transit's raw create/configure/trim dialogs moved to kit `Modal`; new inline-expandable last-error disclosure on the ops panels (replaces truncated icon+tooltip; renders backend-`sanitize()`d value-free error categories only); `timeAgo` folded onto `relativeTime` (`lib/time` deleted, N2-F4); shared `buttonClasses` recipe extracted so the `ConfirmDialog` Radix triggers match kit buttons.
- **N5 — Settings hub** (§4.5) + OIDC login button (§4.8 auth restyle rides along).
- **N6 — Ops create flows** (§4.6) + ErrorBoundary/404 (§4.7).
- **N7 — Polish sweep:** motion audit, light-theme AA audit across all screens, empty states with CTAs, copy-feedback microstates, mockup-parity check.

Suggested order rationale: N1 unlocks everything; N2 is the visible payoff; N5/N6 convert backend-only features into product (highest gap value); N7 catches drift.

## 6. Explicitly out of scope (tracked elsewhere)

Editor behavioral depth (sorting/bulk/keyboard/import-preview — gaps.md §2.1), audit timeline & detail expansion, onboarding checklist, notifications center/bell, session management UI, backup *restore* UI (restore requires an empty instance; CLI remains the path), any backend changes (pagination, KEK rotation, `/v1/sys/version` — gaps.md §4–5; Settings renders version conditionally until the endpoint exists).

## 7. Design-authority handover

After N1 merges: `docs/design/ui-nocturne-mockup.html` + the N1 spec become the source of truth; CLAUDE.md "Web UI visual system" section points at them; the old mockup/spec stay in-tree marked superseded. `fe-improvements.md` gets a header note: motion (§0), auth polish, and remaining P2s are absorbed by this program; anything left over migrates into gaps.md tracking.

## 8. Acceptance (what "done" looks like)

A user logging in lands on a dashboard that answers "is everything okay?" in one glance; every card and row carries real metadata; active elements glow and respond within 150ms; Settings, OIDC, and ops-create exist as product surfaces instead of curl commands; both themes pass AA and smoke; and zero security invariants from §1 have moved.
