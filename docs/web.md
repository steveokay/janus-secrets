# Web UI

**Status:** Phase 2, sub-project B, milestone 1 (**core editor**) — implemented.
The React SPA now ships embedded in the `janus` binary. Spec:
[`superpowers/specs/2026-07-06-spa-core-editor-design.md`](superpowers/specs/2026-07-06-spa-core-editor-design.md).

A single-page React app served **same-origin** from the Go server. It replaces
the CLI for the core secrets loop: unseal, sign in, navigate Project →
Environment → Config, and edit secrets with masked values, audited reveal, and
batched "Save as vN" config versions.

## Architecture

```
web/                    Vite + React + TS + Tailwind app (source)
  src/lib/              api client (fetch + ApiError), typed endpoints, query client
  src/auth/             AuthProvider, login, change-password
  src/unseal/           unseal screen
  src/shell/            TopBar, AppLayout, Sidebar, Placeholder
  src/secrets/          nav hooks, SecretEditor, pure dirty-buffer
  src/structure/        create-forms (project/env/config)
internal/web/           Go: //go:embed dist + Handler() (assets + SPA fallback + CSP)
```

The store is unchanged and the server exposes **no new `/v1` endpoints** for the
UI — the SPA consumes the existing auth/sys/projects/environments/configs/secrets
routes. The only server-side additions are the `internal/web` static handler, a
narrowing of the seal gate (below), and the CSP header.

### Serving & the SPA fallback

`internal/web.Handler()` serves the embedded static assets; any path that is not
a real asset and not under `/v1` returns `index.html`, so client-side routing
owns deep links (`/projects/x/configs/y` loads the shell and React Router takes
over). It mounts as the chi router's `NotFound` fallback (via `Server.MountUI`,
wired in `Boot`) **after** the `/v1` routes, so the API always wins. Every
response carries a restrictive `Content-Security-Policy: default-src 'self'`
(plus `X-Content-Type-Options: nosniff`, `X-Frame-Options: DENY`).

### Seal gate

The server starts sealed and `/v1` secret operations return `503` until it is
unsealed. `RequireUnsealed` gates only `/v1/*` API paths (except `/v1/sys/*`) —
**non-`/v1` paths, i.e. the SPA and its assets, are always served**, so the UI
can load and present the unseal screen while the server is sealed.

## Flows

- **Bootstrap:** on load the app reads `GET /v1/sys/seal-status`. If the seal is
  not initialized it shows a "run `janus init`" notice (initialization stays a
  CLI/operator step). If sealed → the **unseal screen**. Otherwise it reads
  `GET /v1/auth/me`; a `401` → the **login** page, success → the app.
- **Unseal:** Shamir servers show a "k of threshold submitted" progress bar and a
  password-type share input (one share per `POST /v1/sys/unseal`), plus a Reset
  button (`POST /v1/sys/unseal/reset`). KMS servers auto-unseal; the screen polls
  seal-status. **Shares live only in the input field and are cleared on submit —
  never persisted, cached, or logged.**
- **Navigation** (project-centric): the sidebar is a project switcher + the
  selected project's environment → config tree; a config opens the editor.
  Lightweight create forms add projects, environments, and configs (a config may
  inherit from a **same-environment** base — the server enforces this).
- **Secret editor** (the flagship): the masked list (`GET …/secrets`, not
  audited) shows each key with an `origin` badge (`own` / `inherited` /
  `overridden`, from the inheritance feature). The eye reveals one value
  (`GET …/secrets/{key}`, an **audited** `secret.reveal`). Editing works on the
  config's **own raw values** (`?reveal=true&raw=true`) — inherited rows are not
  editable in place. Edits accumulate in a client-side dirty buffer (add / edit /
  delete) with a live pending summary; **Save as vN** posts the whole batch as
  one `PUT …/secrets` = one config version. A dirty buffer arms an
  unsaved-changes guard.

**Security:** revealed secret plaintext and Shamir shares live only in ephemeral
React state — never in the TanStack Query cache, `localStorage`, or logs. The
session cookie is httpOnly (the SPA never reads it; `GET /v1/auth/me` is the
source of truth). React auto-escaping + the CSP mitigate XSS; same-origin
embedding means no CORS surface.

## Scope

**In this milestone:** app shell, unseal, login, change-password, project/env/
config navigation + create, and the secret editor (masked list, reveal, raw-value
editing, batched save).

**Deferred to later B-slices** (visible as "Coming soon" placeholders): config
version history / diff / rollback browsing, the audit viewer, token and member
management, the project dashboard + usage metrics, and the transit UI. Structure
rename/delete/destroy and OIDC login are also out of this milestone.

## Build & develop

- **Production:** the multi-stage `Dockerfile` builds `web/` (`npm ci && npm run
  build`) and copies `web/dist` into `internal/web/dist` before `go build`, so the
  single binary embeds the UI. No Node in production.
- **`make build`** mirrors this locally (builds the SPA, stages it under
  `internal/web/dist`, builds the binary). The committed
  `internal/web/dist/index.html` is a placeholder a real build overwrites — do not
  commit built assets (the root `.gitignore`'s `dist/` rule keeps them out).
- **`make dev`** documents the two-terminal dev flow: `cd web && npm run dev`
  (Vite on `:5173`, proxying `/v1` → `:8200`) alongside `make dev-up` (the Go
  server + Postgres on `:8200`). The Vite proxy keeps the app same-origin in dev,
  so the session cookie works exactly as in production.
- **`make test`** runs `go test ./...` and the web suite (`npm run test`).

## Testing

- **Web:** Vitest + React Testing Library + MSW (mocks `/v1` with the real
  envelope shapes). Covers the dirty-buffer logic, editor reveal/save, unseal
  progress, login (401/429), and the bootstrap guards.
- **Go:** `internal/web` tests assert the SPA fallback (deep links → shell,
  `/v1` untouched, real assets served, CSP present); `internal/api` asserts the
  UI mounts as a fallback and the seal gate serves static assets while sealed.

## Operations console (`/operations`)

A cross-project console for the three Phase-3 engines — **rotation**,
**sync**, and **dynamic credentials** — that are otherwise API/CLI-only.
The page fans out over every project you can see (silently skipping ones
where you lack the engine's role) and shows unified tables with a Project
filter and three tabs:

- **Rotation** — policies with status/next-run; actions: rotate-now,
  pause/resume, edit interval, delete.
- **Sync** — targets with provider/destination/status; actions: sync-now,
  pause/resume, edit interval, delete.
- **Dynamic** — roles (admin/owner: listing needs `dynamic:manage`);
  actions: issue credentials, view/renew/revoke leases, delete role.

The console **cannot create** resources — creating a policy/target/role
requires entering privileged admin DSNs, PATs, k8s tokens, or SQL
templates, which stays in the CLI (`janus rotation|sync|dynamic … create`).
No secret is ever rendered except a freshly **issued** dynamic password,
which is shown once in an ephemeral dialog and never cached.
