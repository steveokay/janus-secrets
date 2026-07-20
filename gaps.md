# Janus — Gap Analysis

> **⚠️ Superseded (2026-07-19) by [`docs/roadmap.md`](docs/roadmap.md).** Most
> gaps below have since shipped (settings hub, trash/restore, audit depth,
> RBAC matrix, board depth, ops console depth, promotion approval, CLI control
> plane, release hygiene, API hardening, master-key/KEK rotation, integrations
> hub, typed secrets, filename-style keys, and the Svelte "Atrium" UI rewrite).
> `docs/roadmap.md` is the current state-of-the-system summary and forward
> roadmap; `status.md` is the (now largely historical) phase-by-phase build
> log. Kept in-tree for history — do not treat unstruck rows below as still
> open without checking the code first.

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
| 1.10 | ~~**Soft-delete restore is invisible.**~~ **[DONE 2026-07-15, PR #81]** — new `GET /v1/trash` (value-free, per-item authz-filtered) + `/trash` page (restore + typed-confirm destroy) + ConfirmDialog-gated soft-delete on project/env/config views. | ~~routes vs `web/src/lib/endpoints.ts`~~ | ~~MED~~ |
| 1.11 | ~~**Per-key value history not surfaced.**~~ **[DONE 2026-07-15, PR #81]** — version chip in the secret editor opens `KeyHistorySheet` (value-free list + ephemeral audited historical reveal via `?version=N`). | ~~endpoint unused in `web/src`~~ | ~~MED~~ |
| 1.12 | **No session management.** Logout only; no active-session list / logout-all. | `web/src/shell/UserMenu.tsx` | LOW |
| 1.13 | **No onboarding / first-run flow.** Empty instance gives no "create project → add secrets → janus run" checklist (known deferred item; needs a backend first-login signal). | `fe-improvements.md` §2 | LOW |
| 1.14 | **No notifications center.** Transient toasts only; mockup shows a bell icon that was never built. Fine until rotation/sync failures deserve surfacing in-app. | `web/src/shell/TopBar.tsx` | LOW |
| 1.15 | ~~**No Integrations hub.**~~ **[DONE 2026-07-16]** — new `/integrations` catalog (Phase 1: frontend-only): three connector cards by external system (GitHub = Actions sync + CI federation; Kubernetes = sync; OIDC = SSO login) with best-effort, **403-tolerant** status (reuses `useSync('all')` + `getFederationConfig`/`oidcLoginStatus`) that deep-links into `/operations?tab=sync` and `/settings?section=oidc|federation`. Sidebar item (`Blocks` icon, above Operations) + ⌘K "Go to Integrations". No backend, no moved config. Spec `docs/superpowers/specs/2026-07-16-integrations-hub-design.md`, plan `docs/superpowers/plans/2026-07-16-integrations-hub.md`. Phase 2 (absorb config into the hub) remains a possible future spec. | `web/src/integrations/` | ~~MED~~ |

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

### 2.3 Audit viewer (`web/src/audit/AuditPage.tsx`) — MED — **[DONE 2026-07-18]**
- ~~No click-to-expand event detail (rows are summary-only).~~ **[DONE 2026-07-15, PR #81]** — click/keyboard row expand shows the full event incl. `prev_hash → hash` chain (one open at a time; Enter/Space/Esc; `aria-expanded`). Client-side date grouping (Today/Yesterday/`YYYY-MM-DD`) added too.
- ~~No event-count timeline/histogram; no saved filter presets (e.g. "failures, last 7d").~~ **[DONE 2026-07-18, feat/audit-depth]** — `GET /v1/audit/histogram` (new store aggregate, value-free bucket counts) backs a click-to-zoom `AuditHistogram` bar chart; saved filter presets (name + filter draft, `localStorage`-backed) let common queries (e.g. "failures, last 7d") be applied in one click.
- ~~Infinite scroll only — no page-size control; header not sticky on long scrolls.~~ **[DONE 2026-07-18, feat/audit-depth]** — explicit page-size control alongside the existing sticky header/row-expand/date-grouping.

### 2.4 Projects list / board (`web/src/home/`) — MED
**Update 2026-07-17 (board-depth):** shipped the depth batch — project cards now show a **glyph** + a **recency line** ("active X ago" / "created X ago", backed by a new `last_activity_at` aggregate over config versions) and a **quick-action menu (Rename)**; a **sort control** (Newest/Oldest/Recently active, by `created_at`/`last_activity_at`) replaces A–Z-only; the board's env-column headers gained an **⋯ menu (Rename / Clone environment / Delete)** plus a per-column **recency subline**; inheritance is now shown with **connector lines** instead of bare indentation. Backend: `PATCH` rename (name-only, admin+) for projects and environments, `POST` clone-environment (admin+, deep-copies the config tree + secrets, value-free `env.clone` audit, authorized against the source env's real project), and `last_activity_at` exposed on project/environment list responses.
- ~~Cards lack metadata the mockup shows: last-modified, project glyph.~~ **[DONE]** — glyph + recency line.
- ~~Sort is A–Z only (no by-activity/created); no quick-action menu on cards.~~ **[DONE]** — Newest/Oldest/Recently-active sort + card quick-action menu (Rename).
- ~~Board: env column headers are read-only (no rename/describe), no clone-environment, inheritance shown as bare indentation without connectors.~~ **[DONE]** — env-header ⋯ menu (Rename/Clone environment/Delete) + recency subline; inheritance connector lines.
- **Non-goal:** author metadata (no column) — not tracked anywhere in the data model; out of scope.
- Possible follow-up: a `janus env clone` CLI verb (clone-environment is currently web/API-only).

### 2.5 Tokens (`web/src/tokens/TokensPage.tsx`) — LOW-MED
- ~~No search/filter/sort~~ **[DONE 2026-07-17, list-ergonomics]** — name/scope-kind search + click-to-sort columns (Name/Access/Created/Expires/Status) via the shared `web/src/ui/table/` primitive. Still open: no last-used timestamp (backend doesn't record it — backend gap); no stale-token highlighting; no revoke+remint rotation flow.

### 2.6 Members (`web/src/members/MembersPage.tsx`) — LOW-MED
- Has create/disable/roles (verified) + **[DONE 2026-07-17, list-ergonomics]** search + sort on the role-bindings and Users tables (role sorts by privilege rank, not alphabetically). ~~no user search in the add-member *picker*, no RBAC matrix view (users × scopes grid)~~ **[DONE 2026-07-18, rbac-matrix]** — read-only RBAC matrix (users × scopes grid, Instance + project columns with "+N env" env-aware cells, explicit bindings only, 403-tolerant fan-out) with a List|Matrix view toggle, plus search on the add-member picker. Still open: no last-login column (backend gap, see §5).

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
| 4.1 | ~~**No master-key or project-KEK rotation surface.**~~ **[DONE]** — project-KEK rotation (PR #71/#72) + master-key rotation (PR #77, `8f89232`, 2026-07-15): eager-atomic 5-table re-wrap + Shamir rekey ceremony vs KMS single-call, owner-only `sys:master-key`, value-free, migration 000019. The `keyring.go` `TODO(rotation)` is removed. | ~~`internal/crypto/keyring.go:50` TODO(rotation)~~ → `internal/masterkeys`, `internal/projectkeys` | ~~HIGH~~ |
| 4.2 | **Cursor pagination missing on most list endpoints.** Spec: "List endpoints: cursor pagination." Only audit events/export paginate; projects, environments, configs, secrets, tokens, users, transit keys return everything. | `internal/api/projects_handlers.go` et al. | HIGH |
| 4.3 | **No `Idempotency-Key` support.** Spec: "Mutations: idempotency via client-supplied Idempotency-Key where destructive." Not parsed anywhere. | grep across `internal/api` | MED |
| 4.4 | **HTTP server hardening:** only `ReadHeaderTimeout` set — no `ReadTimeout`/`WriteTimeout`, no `MaxBytesReader` body limits (restore/import paths accept unbounded bodies). | `internal/api/server.go` (ListenAndServe) | MED |
| 4.5 | No account lockout / progressive backoff beyond per-IP rate limit (10/min). Admin disable exists. | `internal/api/ratelimit.go` | LOW |
| 4.6 | DB pool uses pgx defaults (no max-conns/lifetime tuning); shutdown grace fixed at 10s. | `internal/store/store.go` | LOW |

What **passed** (worth stating): route surface is complete for all shipped features (~90 endpoints); error envelope consistent; leak tests, gosec+govulncheck, 100% crypto coverage, `-race`, and testcontainers integration tests are all in place; the sole production TODO (4.1 `keyring.go` rotation) is now resolved and removed.

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
| ~~No project/env/config management commands (`janus projects create` etc.) — bootstrap requires UI or curl~~ **[DONE 2026-07-16]** — `janus project/env/config create/list/delete/restore` (soft-delete; slug addressing, binding-aware parents, `--json`, TTY-confirm) | `cmd/janus/{project,env,config}_commands.go` | MED |
| ~~No token management commands (mint/list/revoke)~~ **[DONE 2026-07-16]** — `janus token mint/list/revoke`; mint prints the raw token once to stdout (capturable), summary to stderr; list is metadata-only | `cmd/janus/token_commands.go` | MED |
| ~~Rotation/sync: CLI has create+list but no update/pause/rotate-now/sync-now/delete; dynamic: list only~~ **[STALE — verbs already existed]** — `janus sync` has get/update/delete/sync-now; `janus rotation` has the run/pause verbs; `janus dynamic` has `roles`/`leases` subgroups (issue/renew/revoke). Added during Phase 3 / ops-console work | `cmd/janus/{rotation,sync,dynamic}_commands.go` | — |
| ~~No shell completion (`janus completion`)~~ **[DONE 2026-07-16]** — `janus completion bash\|zsh\|fish\|powershell` | `cmd/janus/completion.go` | LOW |
| ~~No `janus whoami`; no `janus secrets diff`~~ **[DONE 2026-07-16]** — `janus whoami` (`GET /v1/auth/me`) and value-free `janus secrets diff <vA> <vB>` (key names only) | `cmd/janus/whoami.go`, `secrets_diff.go` | LOW |

> **§6 CLI control plane — DONE 2026-07-16 (PR pending).** The `janus`
> CLI is now self-sufficient for bootstrap + identity: stand up a
> project, add environments/configs, and mint a scoped CI token entirely
> from the command line, over existing REST endpoints (no server/API/
> migration changes). See
> [docs/guides/managing-secrets.md § Via the CLI](docs/guides/managing-secrets.md#via-the-cli).

## 7. Ops / DX / docs

| # | Gap | Evidence | Sev |
|---|-----|----------|-----|
| 7.1 | **Web tests + smoke not in CI.** `.github/workflows/ci.yml` is Go-only; `npm test` (200+ tests) and `web/scripts/smoke.mjs` (dual-theme check demanded by CLAUDE.md) never run on PRs. | `ci.yml` | HIGH |
| 7.2 | ~~**No OpenAPI spec**~~ **[DONE 2026-07-16]** — hand-authored `docs/openapi.yaml` (OpenAPI 3.1) covers all 125 `/v1` routes; kept in sync by a route-walking drift test (`internal/api/openapi_drift_test.go`). Value-free examples (secret fields `writeOnly` + placeholder). | `docs/openapi.yaml` | MED |
| 7.3 | ~~**No LICENSE**~~ **[DONE 2026-07-16]** — Apache-2.0 (`LICENSE` + `NOTICE`); README updated; vendored MPL-2.0 Shamir reconciled. | repo root | MED |
| 7.4 | ~~No release machinery~~ **[DONE 2026-07-16]** — `.goreleaser.yaml` (multi-arch binaries + GHCR image), `CHANGELOG.md` (0.1.0), `internal/version` build-info via ldflags + `GET /v1/sys/version`, `.github/workflows/release.yml` on tag + PR `goreleaser check`. | repo root, `.github/` | MED |
| 7.5 | ~~No production deployment guide~~ **[DONE 2026-07-16]** — `docs/guides/production-deployment.md` (reverse-proxy TLS, `JANUS_*` config, unseal, image, sizing, backups, upgrades, monitoring). | `docs/guides/production-deployment.md` | MED |
| 7.6 | No Prometheus `/metrics`; no `JANUS_LOG_LEVEL`/format config. Reads-24h is the only metric. | `internal/api` | MED |
| 7.7 | docker-compose has healthchecks but no resource limits; no WAL-archiving/pg-backup guidance beyond app-level backup doc. | `docker-compose.yml`, `docs/ops/backup-restore.md` | LOW |
| 7.8 | No CONTRIBUTING.md. | repo root | LOW |

---

## Suggested priority order

_Updated 2026-07-15: items 1–5, plus editor depth (2.1) and ops-console depth (2.2), env→env promotion, and modal-solidify are all shipped. Remaining, re-ranked:_

1. ~~**CI: web tests + smoke** (7.1)~~ **[DONE]** PR #60.
2. ~~**UI: Settings surface** (1.1/1.3/1.4/1.6)~~ **[DONE]** Nocturne N5, PR #65.
3. ~~**UI: OIDC login button** (1.2)~~ **[DONE]** N5, PR #65.
4. ~~**UI: Operations create flows** (1.5)~~ **[DONE]** N6, PR #66.
5. ~~**Backend: KEK/master-key rotation** (4.1)~~ **[DONE]** PR #71/#72 (KEK) + PR #77 (master). Crypto-spec promises complete.
6. ~~**Editor depth pass** (2.1)~~ **[DONE]** PR #68. ~~**Ops-console depth** (2.2)~~ **[DONE]** PR #69/#70.

**Next up (nothing crypto/spec-critical left; these are the top remaining):**

7. ~~**Backend: pagination + idempotency + server hardening** (4.2–4.4)~~ **[DONE 2026-07-15, PR #79]** — opt-in cursor pagination on 7 table-backed list endpoints (keyset `created_at DESC, id DESC`, backward-compatible), generic status-only `Idempotency-Key` middleware over all mutating verbs (replaces the promotion-specific one; value-safe by construction), and `JANUS_HTTP_*` read/idle/write timeouts + `MaxBytesReader` body cap (restore exempt). Migration 000020. (Also fixed the pre-existing `internal/crypto` 100%-coverage CI gate red since #77 — PR #80.)
8. ~~**UI depth: restore/undelete** (1.10) + **per-key value history** (1.11) + **audit viewer expand/timeline** (2.3)~~ **[DONE 2026-07-15, PR #81]** — Trash surface (value-free per-item-authz `GET /v1/trash` + restore/typed-confirm-destroy + soft-delete on active views), per-key `KeyHistorySheet` with ephemeral audited historical reveal, and audit row-expand (full event + hash chain) with client-side date grouping. Frontend-heavy; one backend endpoint; no migration.
9. ~~**CLI control plane** (§6) — `janus projects/env/config create`, token mint/list/revoke, and the missing rotation/dynamic verbs; makes the CLI self-sufficient for bootstrap + ops.~~ **[DONE 2026-07-16]** — project/env/config CRUD + token mint/list/revoke + `whoami`/`completion`/`secrets diff` shipped (the rotation/sync/dynamic verbs turned out to already exist). CLI-only, over existing endpoints; no migration.
10. ~~**Release hygiene** (7.2–7.5): LICENSE, OpenAPI spec, goreleaser/CHANGELOG, production deployment guide — needed before any tagged release.~~ **[DONE 2026-07-16]** — Apache-2.0 license, hand-authored OpenAPI 3.1 spec (drift-guarded), goreleaser (binaries + GHCR) + CHANGELOG + version wiring + `GET /v1/sys/version`, and a production deployment guide. Repo is now tag-able. Remaining §7: 7.6 (Prometheus/log config), 7.7 (compose limits/pg-backup), 7.8 (CONTRIBUTING).
11. ~~**Phase B: promotion approval workflow** — a separate future spec (request→review→approve for users without `secret:promote`); noted in [[env-promotion-progress]].~~ **[DONE 2026-07-16]** — capability-gap model: users lacking `secret:promote` on the target file a value-free `promotion:request` (developer+, source-env scoped) pinning the source version + key NAMES/actions; holders of `secret:promote` on the target approve (applies immediately, four-eyes: approver ≠ requester) / reject / and requesters cancel. Mark-on-success (Apply then atomic CAS `pending→applied`) so an applied row always maps to a real promotion. Migration 000021; REST (`/v1/promote/requests…`) + `janus promote request/requests/approve/reject/cancel` CLI + `/approvals` web surface with nav badge. Value-free throughout; audit `promotion.request.{create,approve,reject,cancel}`.
12. ~~**UI depth: projects list / board** (2.4) — card metadata, sort, quick-actions, env-header management, inheritance connectors.~~ **[DONE 2026-07-17]** — card glyph + recency line (new `last_activity_at` aggregate) + created/activity sort + card quick-action menu (Rename); board env-column ⋯ menu (Rename/Clone environment/Delete) + recency subline; inheritance connector lines. Backend: name-only rename (admin+) for projects/environments, deep-copy clone-environment (admin+, value-free audit). Author metadata is a non-goal; `janus env clone` CLI is a possible follow-up.
