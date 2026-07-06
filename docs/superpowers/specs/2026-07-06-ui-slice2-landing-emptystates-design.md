# UI Slice 2 — Landing, EmptyState, Project Overview (design)

- **Status:** APPROVED 2026-07-06 (scope + design approved by Steve in-session).
- **Scope decision:** landing hero + reusable `<EmptyState>` + project-overview
  dashboard. "Reads 24h" ships as a visibly-disabled placeholder until Phase-2D
  usage metrics exist. Recent-activity feed stays in slice B3; onboarding
  checklist stays P2.
- **Visual authority:** [`2026-07-06-ui-visual-design.md`](2026-07-06-ui-visual-design.md)
  + [`docs/design/ui-mockup.html`](../../design/ui-mockup.html). Landing and
  overview are not in the mockup, so per hard rule 4 they compose from tokens
  and stay *quieter* than the editor screen; this doc pins their exact
  treatment so implementers don't improvise.
- **Tracker:** `fe-improvements.md` §2 (P0 landing, P1 dashboard, P1 empty states).

## Data approach (decided)

**Client-side aggregation over existing endpoints — no backend changes.**
Envs via `useEnvironments(pid)`; configs per env via `useQueries` on the
Sidebar-shared key `['configs', pid, eid]`; per-config key counts + last-change
via `useQueries` on the SecretEditor-shared key `['config', cid, 'masked']`
(`endpoints.maskedSecrets`). Masked metadata reads are **unaudited by design**
(they reveal nothing) — the dashboard must never call reveal/raw endpoints.
A `GET /v1/projects/:id/summary` endpoint is a possible later optimization
(note only; do not build).

## Units

### 1. `web/src/ui/EmptyState.tsx`

```
EmptyState({ icon?: ReactNode, title: string, hint?: string, action?: ReactNode })
```

- Container: `mx-auto mt-16 flex max-w-sm flex-col items-center gap-3 text-center`
- Icon wrap (only when `icon` given): `flex h-12 w-12 items-center justify-center rounded-full bg-brand-soft text-brand-deep`
- Title: `text-[15px] font-semibold text-ink`; hint: `text-[12.5px] text-muted`
- `action` renders last, unwrapped (callers pass their own button).
- Reused by every future slice (B2–B5) — keep it dumb and presentational.

### 2. `web/src/home/Landing.tsx` (route `/`)

Replaces the "Select or create a project to begin." div.

- **No projects yet:** hero — `mx-auto mt-20 flex max-w-md flex-col items-center gap-4 text-center`;
  `<Brand markOnly size={40} />`; h1 `text-[22px] font-semibold tracking-tight`
  **"Your secrets, sealed and audited"**; value line
  `text-[13.5px] text-muted`: "Projects, environments and configs — encrypted
  end-to-end, every reveal audited."; primary CTA button (`bg-brand` primary
  style from slice 1) **"Create your first project"** → opens the existing
  `CreateProjectForm`; on create, navigate to `/projects/<id>` (same as Sidebar).
- **Projects exist:** heading `text-[17px] font-semibold` **"Open a project"**;
  vertical list (`flex w-full max-w-md flex-col gap-2`) of link-cards
  (`flex items-center justify-between rounded-card border border-line bg-card
  px-4 py-3 shadow-card hover:border-brand-line`) showing project name
  (`font-semibold`) and slug (`text-[11.5px] text-faint`), linking to
  `/projects/<id>`; below, a secondary-style **"New project"** button.
  *(Refinement from the approved design: cards show name + slug, not env
  count — an env count would cost one extra request per project for a
  cosmetic number.)*
- Loading: three skeleton rows (slice-1 `animate-pulse`-free shimmer is Slice 3;
  here plain `bg-line-soft` rounded blocks suffice, no animation).
- Title: default ("Janus") — no `useTitle` call.

### 3. `web/src/home/ProjectOverview.tsx` (route `/projects/:projectId`)

Replaces the "Select a config from the sidebar." div.

- Header: project name h3 `text-[17px] font-semibold tracking-tight` +
  sub `text-[12.5px] text-faint` "N environments · M configs";
  right side: `<Pill tone="muted">Reads 24h · soon</Pill>` (the placeholder —
  becomes real in Phase-2D).
- Grid `grid gap-4 md:grid-cols-2`; one card per environment
  (`rounded-card border border-line bg-card shadow-card`):
  - Card header `flex items-center gap-2 px-4 py-2.5` — env dot
    (`h-[7px] w-[7px] rounded-[2px]` + `envDotClass[envTone(name)]`), env name
    `text-[12px] font-semibold text-muted uppercase tracking-[.08em]`,
    right-aligned config count `text-[11px] text-faint`.
  - Config rows: `<Link>` per config —
    `flex items-center justify-between border-t border-line-soft px-4 py-2.5 hover:bg-line-soft/50`;
    left: config name `text-[13px] font-medium text-ink` (+ existing `↳`
    inherits marker in `text-info` when `inherits_from`); right:
    `text-[11.5px] text-faint tabular-nums` "N keys · <timeAgo>".
  - Env with zero configs: single muted row "No configs yet" (`text-[12.5px] text-faint px-4 py-2.5 border-t border-line-soft`).
- Project with zero environments: `<EmptyState>` (lucide `Layers` icon,
  title "No environments yet", hint "Environments hold your configs — dev,
  staging, prod.", action = primary button "Create environment" opening the
  existing `CreateEnvironmentForm`; on create, list refreshes via the query
  invalidation the form already does).
- Per-card loading: two skeleton rows; per-card error: row
  `text-[12.5px] text-danger` "Couldn't load configs."
  Count/last-change queries that fail render "— keys" (`text-faint`), never
  block the row link.
  *(Amended post-review: failed count queries render "keys unavailable" in
  `text-danger/70` with a tooltip instead of "— keys", so an error is
  distinguishable from still-loading; the row link stays active either way.)*
- Title: `useTitle(project.name)` once resolved.
- **Security:** masked metadata only (`maskedSecrets`); never `revealAll`/
  `rawConfig`; no audit events from rendering this page.

### 4. `web/src/lib/time.ts`

`timeAgo(iso: string, now?: Date): string` — "just now" (<60s), "Nm ago",
"Nh ago", "Nd ago", falls back to `toLocaleDateString()` past 30 days. Pure,
unit-tested; `now` injectable for tests. Last-change per config =
max `created_at` across its masked entries; if a config has zero keys show
"0 keys" with no time.

### 5. `web/src/secrets/SecretEditor.tsx` (one addition)

When `rows.length === 0 && addedKeys.length === 0`: render
`<EmptyState title="No secrets yet" hint="Add your first key below — it's encrypted before it ever touches the database." />`
between the toolbar and the (kept) `AddKeyRow`. No other editor changes.

### App wiring

`web/src/App.tsx`: route `/` element → `<Landing />`; route
`/projects/:projectId` element → `<ProjectOverview />` (rendered inside the
matched Route, so `useParams` works there — unlike the shell components).
All other routes untouched.

## Error handling

Query errors are contained to their card/section with inline `text-danger`
text; the page never blanks. No toasts (Slice 3), no retry buttons (TanStack
default retry stays).

## Testing

- `EmptyState.test.tsx` — renders title/hint/icon/action; omits icon wrap when no icon.
- `time.test.ts` — table-driven `timeAgo` cases incl. boundary minutes/hours/days + injectable now.
- `Landing.test.tsx` (msw) — zero projects → hero + CTA opens create dialog; with projects → link-cards with correct hrefs + "New project" button.
- `ProjectOverview.test.tsx` (msw) — envs/configs/counts render ("2 keys ·"), zero-config env shows "No configs yet", zero-env project shows EmptyState, config rows link to `/projects/:pid/configs/:cid`.
- `SecretEditor.test.tsx` — ADD one test: empty config shows the EmptyState (existing tests untouched).
- Palette gate: all new files token-only (enforced automatically).

## Out of scope (do not build)

Recent-activity feed (B3) · real Reads-24h (Phase-2D) · onboarding checklist
(P2) · summary endpoint · toasts/skeleton-shimmer component (Slice 3) ·
dashboard quick-actions beyond the two create forms already reused.
