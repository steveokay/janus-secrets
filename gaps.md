# Janus — Gap Analysis

_Audited 2026-07-12 (main @ d36add8) by a 4-agent sweep: UI depth, backend-vs-spec, UI↔backend parity, ops/DX. Conflicting findings were re-verified against the code before inclusion._

Severity: **HIGH** = missing capability users will hit; **MED** = real friction or spec drift; **LOW** = polish.

---

## 1. Web UI — missing screens & flows

The SPA covers the core loop (projects → envs → configs → secret editing) well, but several whole admin/ops surfaces exist only in the backend or CLI. This is the biggest reason the UI feels basic: it's an editor, not yet a control plane.

| # | Gap | Evidence | Sev |
|---|-----|----------|-----|
| 1.1 | **Settings page is a stub.** `/settings` renders `<Placeholder feature="Settings" />`. Natural home for OIDC config, seal/backup controls, theme default, instance info — none exist. | `web/src/App.tsx` route → `web/src/shell/Placeholder.tsx` | HIGH |
| 1.2 | **No OIDC login button.** Backend has the full Auth-Code+PKCE flow (`GET /v1/auth/oidc/login`, `/status`), but nothing in `web/src/auth` references OIDC — SSO users cannot log in via the UI at all. | grep `oidc` in `web/src/auth` → 0 matches | HIGH |
| 1.3 | **No OIDC provider admin UI.** `GET/PUT/DELETE /v1/sys/oidc` is API-only; configuring SSO requires curl. | `internal/api/server.go` routes vs. no UI caller in `web/src/lib/endpoints.ts` | HIGH |
| 1.4 | **No OIDC federation (CI trust bindings) admin UI.** `/v1/sys/oidc/federation` + `/bindings` are API-only. | same | HIGH |
| 1.5 | **No create flows in the Operations console.** Rotation policies, sync targets, and dynamic roles can be managed/acted on but not *created* from the UI (page literally says "Create one with `janus rotation create`"). This was a deliberate PR #55 scope cut — now the top candidate to close. | `web/src/operations/*Panel.tsx` | HIGH |
| 1.6 | **No backup/restore UI.** `GET /v1/sys/backup` / `POST /v1/sys/restore` are CLI/API-only; no download-backup button, no seal control in UI (`POST /v1/sys/seal` also unexposed). | `internal/api/backup_handlers.go`, no UI caller | MED |
| 1.7 | **No error boundary.** A render crash anywhere leaves a blank screen; mutations get toasts but rendering has no safety net. | `web/src/App.tsx` — no ErrorBoundary | MED |
| 1.8 | **No 404 page.** Unknown routes silently `Navigate to="/"` with zero feedback. | `web/src/App.tsx` catch-all route | MED |
| 1.9 | ~~**No export-config button.**~~ **[DONE 2026-07-14, editor-depth]** — delivered via selection-bar bulk **Copy .env** / **Download .env** (audited per-key raw reveal, confirm dialog before writing plaintext to disk). | editor pages vs `secrets_handlers.go` | ~~MED~~ |
| 1.10 | **Soft-delete restore is invisible.** `POST /v1/{projects,environments,configs}/{id}/restore` endpoints exist; UI never calls them — a deleted config is gone as far as the UI knows, despite the spec's "soft delete with undelete". | routes vs `web/src/lib/endpoints.ts` | MED |
| 1.11 | **Per-key value history not surfaced.** `GET /v1/configs/{cid}/secrets/{key}/history` (two-level versioning's per-key trace) is never called; VersionHistory only shows config-level versions. | endpoint unused in `web/src` | MED |
| 1.12 | **No session management.** Logout only; no active-session list / logout-all. | `web/src/shell/UserMenu.tsx` | LOW |
| 1.13 | **No onboarding / first-run flow.** Empty instance gives no "create project → add secrets → janus run" checklist (known deferred item; needs a backend first-login signal). | `fe-improvements.md` §2 | LOW |
| 1.14 | **No notifications center.** Transient toasts only; mockup shows a bell icon that was never built. Fine until rotation/sync failures deserve surfacing in-app. | `web/src/shell/TopBar.tsx` | LOW |

_Corrected during verification: user create/disable **does** exist in the UI (`web/src/members/MembersPage.tsx` — create-user sheet with one-time password via RevealOnce, disable with confirm dialog), and the Reads-24h strips **are** wired into ProjectsList and ProjectBoard. Two agent reports claimed otherwise; both claims were checked and rejected._

## 2. Web UI — thin/basic existing screens

These exist and work but are shallow versions of what the mockup/product implies. Grouped by page, most impactful first.

### 2.1 Secret editor (`web/src/secrets/SecretEditor.tsx`, `SecretTable.tsx`) — flagship, MED-HIGH

**Update 2026-07-14 (editor-depth):** shipped the behavioral-depth batch — sortable columns (key/origin/updated/version), multi-row select + selection bar, bulk delete / reveal / copy-.env / download-.env (audited, inherited-skip), keyboard row nav (↑/↓/e/`/`/x), Import .env preview (value-free add/update badges), "Changed only" toggle, and a zero-match empty state. Remaining open items kept below.

- ~~No column sorting, no multi-row select, no bulk operations~~ **[DONE]** — sortable headers + row checkboxes + selection bar (bulk delete/reveal/copy/download, all audited per-key; bulk delete skips inherited keys).
- ~~No keyboard row navigation (↑/↓/Enter/Del)~~ **[DONE]** — `useRowNav` (↑/↓ move, `e` edit, backtick reveal, `x` remove, `/` focuses filter).
- ~~No "show only changed/overridden rows" toggle; ... no zero-match empty state~~ **[DONE]** — "Changed only" toggle + distinct zero-match / no-changed-keys empty states.
- ~~Import .env has no preview step~~ **[DONE]** — value-free preview classifies each key as add/update before commit.
- No per-row revert — Discard is all-or-nothing; no undo after discard. _(per-row revert exists; **still open:** no undo after a full discard.)_
- Review-diff is value-free by design but shows no old→new for *non-secret* metadata either (e.g. key renames read as delete+add). _(still open — no run-history / key-rename diff.)_

### 2.2 Operations console (`web/src/operations/`) — MED

**Update 2026-07-14 (ops-console-depth):** shipped the depth batch across two PRs — **PR #69** added durable run-history backend (`rotation_runs`/`sync_runs` tables, same-tx atomic recording, 100-cap per entity, value-free `GET …/runs` endpoints); **PR 2** (this branch) added the frontend depth. Remaining open items kept below.

- No create flows (see 1.5) — biggest single item. _(still open)_
- ~~Tables: no column sorting, no bulk pause/resume/delete~~ **[DONE]** — sortable headers on all three panels; row checkboxes + selection bar with bulk pause/resume/rotate-or-sync-now/delete (rotation·sync) and bulk delete-role + lease bulk-revoke (dynamic), each a `Promise.allSettled` fan-out over the audited per-id endpoints. Truncated "last error" already expands inline (`LastError`).
- ~~No run-history timeline per policy/target (last N runs, success/fail, duration)~~ **[DONE]** — per-row "Runs" drill-in Sheet (when/status/duration/config-version/attempt, value-free); dynamic reuses the existing leases Sheet.
- ~~No health overview (success rate, failing count) above the tabs~~ **[DONE]** — current-state `HealthStrip` (active·failing·paused per engine, dynamic = role count) above the tabs; failing renders in the danger token and the segment jumps to that tab.
- Dynamic panel: role update (PATCH) exists in backend but not in UI. _(still open)_
- Per-role lease-health aggregate on the dynamic health segment deliberately deferred (would be an N-query fan-out per page load). _(open, low priority)_

### 2.3 Audit viewer (`web/src/audit/AuditPage.tsx`) — MED
- No click-to-expand event detail (rows are summary-only).
- No event-count timeline/histogram; no saved filter presets (e.g. "failures, last 7d").
- Infinite scroll only — no page-size control; header not sticky on long scrolls.

### 2.4 Projects list / board (`web/src/home/`) — MED
- Cards lack metadata the mockup shows: last-modified, author, project glyph.
- Sort is A–Z only (no by-activity/created); no quick-action menu on cards.
- Board: env column headers are read-only (no rename/describe), no clone-environment, inheritance shown as bare indentation without connectors.

### 2.5 Tokens (`web/src/tokens/TokensPage.tsx`) — LOW-MED
- No search/filter/sort; no last-used timestamp (backend doesn't record it — backend gap); no stale-token highlighting; no revoke+remint rotation flow.

### 2.6 Members (`web/src/members/MembersPage.tsx`) — LOW-MED
- Has create/disable/roles (verified), but: no user search in add-member picker (unmanageable past ~30 users), no last-login column, no RBAC matrix view (users × scopes grid).

### 2.7 Transit (`web/src/transit/`) — LOW
- Playground omits decrypt and datakey ops (decrypt arguably deliberate; datakey reasonable to add for parity).
- No key-version history viewer; trim has no preview of what gets dropped; no per-key usage counts.

### 2.8 Login/Unseal (`web/src/auth/`, `web/src/unseal/`) — LOW
- Login: no OIDC button (see 1.2), no instance name/branding slot.
- Unseal: fine; could link a "lost shares" recovery doc.

### 2.9 Command palette (`web/src/palette/`) — LOW
- Navigation-only: no action commands (create project, export audit, toggle theme), no recents, no `project:` / `config:` scoped syntax.

## 3. Web UI — cross-cutting polish

| Gap | Evidence | Sev |
|-----|----------|-----|
| Sticky headers missing on audit/tokens/ops tables (secret table has it) | `AuditPage.tsx` plain `<table>` | MED |
| No collapsible/responsive sidebar under 640px (known P2) | `web/src/shell/Sidebar.tsx` | MED |
| No route-level loading state (Suspense/skeleton on navigation) | `web/src/App.tsx` | LOW |
| Motion/transitions stubbed but not applied (150ms hover/expand, reduced-motion respected) — known P2 §0 | `web/src/theme.css` | LOW |
| No table virtualization (fine now; audit/leases can grow) | all tables | LOW |
| Modal top-right close (X) missing/unstyled (Esc+backdrop work) | `web/src/ui/Modal.tsx` | LOW |
| Copy buttons give toast but no inline "Copied!" state | various | LOW |
| No keyboard-shortcut help overlay ("?") | shell | LOW |
| Optimistic save states ("saving… / saved vN") deferred — known P1§5, needs data-router migration for `useBlocker` unsaved-guard too | `SecretEditor.tsx` | LOW |
| Mockup deltas: sidebar Docs/Status links, bell icon, project glyphs, config-card metadata, breadcrumb copy-name icon | `docs/design/ui-redesign-mockup.html` vs `web/src/shell` | LOW |

## 4. Backend — spec gaps (CLAUDE.md promises not met)

| # | Gap | Evidence | Sev |
|---|-----|----------|-----|
| 4.1 | **No master-key or project-KEK rotation surface.** Spec: "rotating a project KEK re-wraps DEKs lazily; rotating the master key re-wraps all project KEKs (online operation)". No endpoint, no CLI; design TODO still open. | `internal/crypto/keyring.go:50` TODO(rotation); no route in `server.go` | HIGH |
| 4.2 | **Cursor pagination missing on most list endpoints.** Spec: "List endpoints: cursor pagination." Only audit events/export paginate; projects, environments, configs, secrets, tokens, users, transit keys return everything. | `internal/api/projects_handlers.go` et al. | HIGH |
| 4.3 | **No `Idempotency-Key` support.** Spec: "Mutations: idempotency via client-supplied Idempotency-Key where destructive." Not parsed anywhere. | grep across `internal/api` | MED |
| 4.4 | **HTTP server hardening:** only `ReadHeaderTimeout` set — no `ReadTimeout`/`WriteTimeout`, no `MaxBytesReader` body limits (restore/import paths accept unbounded bodies). | `internal/api/server.go` (ListenAndServe) | MED |
| 4.5 | No account lockout / progressive backoff beyond per-IP rate limit (10/min). Admin disable exists. | `internal/api/ratelimit.go` | LOW |
| 4.6 | DB pool uses pgx defaults (no max-conns/lifetime tuning); shutdown grace fixed at 10s. | `internal/store/store.go` | LOW |

What **passed** (worth stating): route surface is complete for all shipped features (~90 endpoints); error envelope consistent; leak tests, gosec+govulncheck, 100% crypto coverage, `-race`, and testcontainers integration tests are all in place; only one TODO exists in production code (4.1).

## 5. Backend — smaller functional gaps

| Gap | Evidence | Sev |
|-----|----------|-----|
| No `GET /v1/sys/version` build-info endpoint (CLI has `janus version`; UI/monitoring can't read server version) | no route | LOW |
| Token `last_used` not tracked → UI can never show stale-token warnings | `service_tokens` schema | LOW |
| User `last_login` not tracked → Members page can't show it | `users` schema | LOW |
| Rotation/sync run history not persisted (only last_error/last_run) → no timeline UI possible | `internal/rotation`, `internal/sync` stores | LOW |
| Audit events not push-notifiable (no webhook on failure events; rotation has its own notify URL) | `internal/audit` | LOW |

## 6. CLI gaps

| Gap | Evidence | Sev |
|-----|----------|-----|
| No project/env/config management commands (`janus projects create` etc.) — bootstrap requires UI or curl | `cmd/janus/main.go` command list | MED |
| No token management commands (mint/list/revoke) | same | MED |
| Rotation/sync: CLI has create+list but **no** update/pause/rotate-now/sync-now/delete; dynamic: list only — **no** create/issue/renew/revoke (inverse of the UI gap 1.5: each surface got half the verbs) | `cmd/janus/rotation_commands.go`, `sync_commands.go`, `dynamic_commands.go` | MED |
| No shell completion (`janus completion` — cobra gives this nearly free) | not wired | LOW |
| No `janus whoami`; no `janus secrets diff` | — | LOW |

## 7. Ops / DX / docs

| # | Gap | Evidence | Sev |
|---|-----|----------|-----|
| 7.1 | **Web tests + smoke not in CI.** `.github/workflows/ci.yml` is Go-only; `npm test` (200+ tests) and `web/scripts/smoke.mjs` (dual-theme check demanded by CLAUDE.md) never run on PRs. | `ci.yml` | HIGH |
| 7.2 | **No OpenAPI spec** — ~90 endpoints documented only in scattered docs; blocks generated clients & API discoverability. | no spec file | MED |
| 7.3 | **No LICENSE** at root (README says "not yet chosen"); blocks any release. | repo root | MED |
| 7.4 | No release machinery: no goreleaser/tags/CHANGELOG/versioned artifacts — self-hosters build from main. | repo root, `.github/` | MED |
| 7.5 | No production deployment guide (reverse-proxy TLS example, resource sizing, monitoring hooks); server is intentionally TLS-less. | `docs/operations.md` scope | MED |
| 7.6 | No Prometheus `/metrics`; no `JANUS_LOG_LEVEL`/format config. Reads-24h is the only metric. | `internal/api` | MED |
| 7.7 | docker-compose has healthchecks but no resource limits; no WAL-archiving/pg-backup guidance beyond app-level backup doc. | `docker-compose.yml`, `docs/ops/backup-restore.md` | LOW |
| 7.8 | No CONTRIBUTING.md. | repo root | LOW |

---

## Suggested priority order

1. **CI: add web tests + smoke** (7.1) — cheap, protects everything else.
2. **UI: Settings surface** (1.1) hosting **OIDC provider + federation admin** (1.3, 1.4) and **seal/backup controls** (1.6) — turns three API-only features into product.
3. **UI: OIDC login button** (1.2) — SSO is currently unusable from the browser.
4. **UI: Operations create flows** (1.5) + matching CLI verb completion (§6 row 3) — finish both halves.
5. **Backend: KEK/master-key rotation** (4.1) — last unimplemented crypto-spec promise.
6. **Backend: pagination + idempotency + server timeouts** (4.2–4.4) — spec debt, mechanical.
7. **Editor depth pass** (2.1): sorting, bulk ops, keyboard nav, import preview — the single page users live in.
8. **Error boundary + 404 + restore/undelete UI + per-key history** (1.7, 1.8, 1.10, 1.11).
9. **Release hygiene**: LICENSE, OpenAPI, goreleaser, deployment guide (7.2–7.5).
