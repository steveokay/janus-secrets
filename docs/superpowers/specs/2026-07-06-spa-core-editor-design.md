# React SPA — Core Editor (Phase 2 · sub-project B, milestone 1) — Design

**Status:** approved design, pre-plan. **Package(s):** new `web/` (React SPA) +
new `internal/web` (embed/serve). **Phase:** 2 ("Transit + UI"), sub-project **B**
(the React SPA). Sub-project A (transit) shipped; C (OIDC) and D (usage metrics)
are separate later cycles.

## Goal

Ship a usable, self-hosted web UI that replaces the CLI for the **core secrets
loop**: log in, navigate Project → Environment → Config, and edit secrets with
masked values, per-key audited reveal, and batched dirty-state "Save as vN"
config versions. Built as a Vite React-TS app, embedded in the Go binary via
`go:embed`, served same-origin by the existing chi server. No Node in production.

## Scope

The full SPA (per CLAUDE.md Phase 2) is large: dashboard, secret editor,
config-version diff, audit viewer, token/member management, transit UI. It is
**sliced** into milestones. **This milestone (B1) is the "core editor first"
slice.**

**In scope (B1):**

- Foundation: Vite + React + TS + Tailwind app; TanStack Query; React Router v6;
  a thin typed `fetch` API client; Vitest + React Testing Library + MSW tests;
  `internal/web` embed package + chi SPA-fallback route; Dockerfile/Makefile
  build wiring; dev via Vite proxy.
- App shell: project-centric layout (top bar with seal status + user menu; left
  sidebar with project switcher + env/config tree; main content).
- Auth: password/session login, `/auth/me` bootstrap, logout, change password.
- Sealed-state: in-UI **unseal screen** (Shamir share entry with progress; KMS
  auto-unseal handled).
- Navigation of existing projects/environments/configs, plus **lightweight
  create** of project, environment, and config (simple forms).
- **Secret editor** (flagship): masked list with `origin` badges; per-key audited
  reveal + reveal-all; edit of the config's own raw values; add/delete keys; a
  client-side dirty buffer; batched **Save as vN** (one config version) with an
  optional message; unsaved-changes guard.

**Explicitly out of scope (later B-slices, not this milestone):**

- Config-version **history browser, diff viewer, and rollback** (saves *create*
  versions here; browsing/comparing/reverting them is deferred).
- **Audit viewer** (event list, filters, chain-verify badge, export).
- **Token** and **member/RBAC** management UIs.
- Project **dashboard** and usage metrics ("Reads 24h" — needs sub-project D).
- **Transit** UI.
- Structure **rename / soft-delete / destroy** from the UI.
- Server **initialization** (`janus init`) — stays a CLI/operator step; the UI
  detects an uninitialized server and points the operator at the CLI.
- Playwright/browser e2e (component + MSW tests only this milestone).
- OIDC login button (sub-project C).

Nav entries for the out-of-scope areas (Audit, Tokens, Members, Transit,
Settings) render visible **"Coming soon" placeholders** so nothing dead-ends.

## Locked decisions

1. **Navigation model = project-centric (Doppler-style).** The left sidebar is
   the project switcher + environment/config tree (the primary spine); project-
   scoped Audit/Tokens/Members sit beneath it; Transit + Settings are small
   top-level entries. Chosen over a Vault-style sectioned nav because the flagship
   is the secret editor and the data model is Doppler-shaped, so mirroring it
   makes the most-used flow the most direct.
2. **Sealed server → in-UI unseal screen** (not a CLI-only banner). The operator
   is the user in a self-hosted tool; mirrors Vault's UI.
3. **Include lightweight structure creation** (project/env/config) so the UI
   stands alone; exclude rename/delete/destroy of structure.
4. **Architecture approach = conventional React SPA** (React Router v6 + TanStack
   Query + hand-written typed fetch client), over TanStack Router or a no-router
   build. Standard-first, minimal dependencies, fastest to a maintainable UI.

## Architecture

Same-origin in both dev and prod, so the session cookie works with no CORS.

```
web/                              # Vite + React + TS + Tailwind (new)
  index.html, vite.config.ts, tailwind.config.js, tsconfig.json, package.json
  vitest.config.ts
  src/
    main.tsx, App.tsx             # entry + router + providers
    lib/api.ts                    # thin typed fetch client (one fn per endpoint)
    lib/queryClient.ts            # TanStack Query client + global error routing
    shell/                        # TopBar, Sidebar, AppLayout
    auth/                         # LoginPage, AuthProvider, useMe
    unseal/                       # UnsealPage, seal-status polling
    secrets/                      # project/env/config nav + SecretEditor + dirty buffer
    structure/                    # create project/env/config forms
    test/                         # MSW handlers + Vitest setup

internal/web/
  embed.go                        # //go:embed dist/** ; Handler() serves assets + SPA fallback + CSP
  embed_dev.go                    # build-tag stub when dist/ is absent (dev builds)
  embed_test.go                   # asserts SPA fallback vs /v1 vs real assets
```

**Serving.** `internal/web.Handler()` serves embedded static files; a request
that is neither a real asset nor under `/v1` returns `index.html` (client routing
owns the path). It mounts in `server.go` as the chi `NotFound`/catch-all **after**
`/v1`, so the API always wins. The handler emits a restrictive
`Content-Security-Policy: default-src 'self'` (plus whatever Vite's built assets
require; inline styles/scripts avoided or nonce'd).

**Build.** Multi-stage Dockerfile: a `web` stage runs `npm ci && npm run build`
→ `web/dist`; the Go stage copies `dist` into `internal/web/dist` before
`go build`, so the binary embeds the assets. `make build` mirrors this locally;
`make dev` runs Vite + the Go server (air) together (Vite proxies `/v1`);
`make test` adds `npm run test` (Vitest) to `go test ./...`.

**Dependencies** (minimal, no component library): runtime `react`, `react-dom`,
`react-router-dom`, `@tanstack/react-query`, `tailwindcss`; dev `vite`,
`typescript`, `vitest`, `@testing-library/react`, `@testing-library/user-event`,
`msw`. UI components are hand-rolled over Tailwind.

## App shell, IA & routing

- **Top bar:** Janus wordmark · seal-status pill (🔓 unsealed / 🔒 sealed) · user
  menu (email → change password, logout).
- **Left sidebar:** project switcher; the selected project's environment → config
  tree (root + branch configs, expandable); project-scoped Audit · Tokens ·
  Members (placeholders); divider; top-level Transit · Settings (placeholders).
- **Main content:** the routed view; a selected config opens the secret editor.

**Routes (React Router v6):**

```
/login                                    login page
/unseal                                   unseal screen (sealed servers)
/                                         redirect to first project (or empty-state picker)
/projects/:projectId                      env/config list (redirects to first config)
/projects/:projectId/configs/:configId    secret editor  ← flagship
/projects/:projectId/audit  ┐
/tokens                     ├ "Coming soon" placeholders this milestone
/members                    │
/transit, /settings         ┘
```

**Bootstrap & guards (on app load):** `GET /v1/sys/seal-status` — if
`initialized === false`, show a "run `janus init`" notice; if `sealed`, route to
`/unseal`. Else `GET /v1/auth/me` — `401` → `/login`; success → user into
`AuthProvider`, into the app.

## Auth & sealed-state flows

Endpoints already exist: `POST /v1/auth/login` (rate-limited, sets httpOnly
session cookie), `POST /v1/auth/logout`, `GET /v1/auth/me`,
`POST /v1/auth/password`; `GET /v1/sys/seal-status`, `POST /v1/sys/unseal`,
`POST /v1/sys/unseal/reset`.

**`seal-status`** returns `initialized`, `sealed`, `type` (`shamir`/`awskms`),
and for Shamir `threshold`, `shares`, and `progress` (shares submitted so far).

**Unseal screen (`/unseal`):**

- **Shamir:** show `threshold`/`shares` and a live "k of threshold submitted"
  progress bar; a single password-type **share input** → `POST /v1/sys/unseal
  {share}` per share; response advances progress; at threshold the server
  unseals → continue to `/login`. A **Reset** button (`POST /v1/sys/unseal/reset`)
  clears a botched attempt.
- **KMS (`awskms`):** the server auto-unseals; the screen polls seal-status and
  moves on — no share input.
- **Shares live only in the input field**, submitted immediately, cleared on
  submit — never localStorage, never Query cache, never logged.

**Login (`/login`):** email + password → `POST /v1/auth/login`; `429` → friendly
rate-limit message; `401` → generic "invalid email or password" (no user
enumeration); then `/me` → into the app.

**Session:** cookie is httpOnly, so the SPA never reads it — `/auth/me` is the
source of truth. `AuthProvider` holds the user + role. Logout → `/login`. Change
password → `POST /v1/auth/password`.

**Global status handling (all `/v1` calls):** `503` (re-sealed mid-session) →
`/unseal`; `401` (session expired) → `/login`; `403` → inline "forbidden" state
(deny-by-default, no detail leak); `{error:{code,message}}` → typed `ApiError`.

## Secret editor (flagship)

**Masked list (default, not audited).** `GET /v1/configs/:cid/secrets` (masked)
→ table `KEY · VALUE(••••••) · ORIGIN · version · actions`. **ORIGIN** reuses
M11's `own` / `inherited` / `overridden`. Inherited keys are read-only in place
(override by adding the same key). Metadata-only read ⇒ no audit event
(per CLAUDE.md — masked list views do not audit).

**Reveal (per-key, audited).** The eye calls `GET /v1/configs/:cid/secrets/:key`
(resolved by default) and shows the value — this emits a **server-side
`secret.reveal` audit event** (CLAUDE.md: revealing a value in the UI is an
audited read). "Reveal all" uses bulk `?reveal=true`. Reference/resolution
failures on reveal (`403`/`409`/`422`) surface inline on that row.

**Editing = raw own-values.** Editing operates on the config's **own stored (raw)
values** — the editable truth — so the edit field loads via `?raw=true`
(unresolved `${...}` shown verbatim). Inherited values are not edited in place;
adding their key creates an override. Values are masked password-inputs with a
per-field reveal toggle and multiline support (certs). No plaintext to disk.

**Dirty buffer + batched save.** Add / change / delete accumulate **client-side
only** — nothing hits the server until Save. A live pending summary
(`+N added · M changed · K removed`) and per-row markers (edited / removed /
new). **Save as vN** sends the whole batch as one `PUT /v1/configs/:cid/secrets`
(+ optional message) = **one config version**; the client maps the buffer to the
existing changes/tombstone contract. On success: Query cache invalidated, buffer
cleared, new version shown. A dirty buffer arms an **unsaved-changes navigation
guard**.

**Structure creation.** Lightweight forms: create project (`POST /v1/projects`),
environment (`POST /v1/projects/:pid/environments`), config
(`POST /v1/projects/:pid/environments/:eid/configs`, with optional
`inherits_from` from a same-environment base — the server enforces the
same-environment rule per M11).

## Data flow & API client

- **`lib/api.ts`** — thin typed `fetch` wrapper: `credentials:"include"`, JSON
  headers, parses the error envelope into `ApiError{status,code,message}`. One
  function per endpoint, grouped `auth` / `sys` / `projects` / `environments` /
  `configs` / `secrets`, with interfaces mirroring the Go JSON. A single place
  routes `401 → /login`, `503 → /unseal`.
- **TanStack Query** — stable keys (`['config', cid, 'secrets', 'masked']`,
  etc.); masked lists/structure are `useQuery`; **save** is a `useMutation` that
  invalidates the config's masked-list + version. Cursor pagination honored in
  list clients.
- **Revealed plaintext is never cached** — the eye action is an imperative fetch
  whose result lives in **ephemeral component state**, cleared on unmount. Same
  for Shamir shares.

## Testing

- **Vitest + React Testing Library + MSW** (mocks `/v1` with the real envelope
  shapes). Core cases: dirty-buffer batches and maps to the `PUT` payload; save
  invalidates + clears; reveal hits the audited endpoint while the masked list
  never does; unseal advances `k of threshold` and transitions; guards redirect
  on `401`/`503`; login handles `401`/`429`.
- **Go:** `internal/web` test — deep links return the `index.html` shell, `/v1/*`
  is untouched, real assets get correct content-types, CSP header present.
- `make test` runs both suites.

## Security

- Session cookie stays httpOnly + SameSite + conditional-Secure (as M5 built);
  the SPA never reads it.
- **Secret plaintext & Shamir shares:** ephemeral React state only — never
  localStorage/sessionStorage, never Query cache, never logged.
- React auto-escaping (no `dangerouslySetInnerHTML` for values) → XSS-safe;
  same-origin embedded → no CORS surface.
- The Go static handler emits `Content-Security-Policy: default-src 'self'`.
- Masked-by-default; every reveal is explicit and audited server-side (no new
  server work — the API already records it).

## Error model (cross-cutting)

Typed `ApiError`; global handling for `401`/`503`/network; inline handling for
`403`/validation/`409`/`422`; a toast for anything unexpected. Errors never leak
internals (the server already returns `{error:{code,message}}` with safe copy).

## Assumptions

- The server is already **initialized** (via CLI `janus init`); the UI only
  detects the uninitialized state and points at the CLI.
- Existing `/v1` endpoints (auth, sys, projects, environments, configs, secrets
  masked/reveal/write) are sufficient for B1 — **no new server endpoints** are
  required, only the `internal/web` static handler + fallback route and CSP.
