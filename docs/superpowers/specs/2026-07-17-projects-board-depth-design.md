# Projects & Board depth — design

_Date: 2026-07-17. Closes gaps.md §2.4 (Projects list / board — thin screen). Second slice of the "UI depth pass" program (after list-ergonomics, PR #88)._

## Problem

The projects list (`/projects`) and the per-project board (`/projects/:id`) are shallow versions of what the product implies:

- **Cards lack metadata.** `/projects` cards show name + slug + env dots + a config count. No glyph (the home dashboard has one), no created/last-active recency, no per-card actions beyond a hover Delete.
- **Sort is A–Z only.** No "newest", "oldest", or "recently active".
- **Board columns are read-only.** Environment headers can only be deleted — no rename, no clone-environment. Config inheritance renders as a bare `↳` + left-margin indent, with no visual connectors.

The audit that produced gaps.md predates several changes; verified current facts drive this design (below).

## Verified starting facts

- **`created_at` is already returned** by both `projectResponse` (`internal/api/projects_handlers.go`) and `envResponse` (`internal/api/environments_handlers.go`); the web `Project`/`Environment` types (`web/src/lib/endpoints.ts`) simply drop it. `Config` already carries `created_at` too.
- **`updated_at` exists on all three tables but is misleading** for "activity": it bumps only when the *row* changes (e.g. a rename), never on secret edits (those live on `config_versions`). So a meaningful "last activity" must aggregate `config_versions.created_at`.
- **No `author`/`created_by` column** on `projects`/`environments` (only `config_versions.created_by`). Author metadata is out of scope — it would need a schema + create-path + audit-correlation project.
- **No rename/PATCH endpoint** exists for projects or environments; **no clone-environment** endpoint.
- **Authz actions are granular** (`internal/authz/actions.go`): `env:create` / `env:delete` / `project:create` are **admin+**; `secret:write` / `config:create` are developer+. There is **no** `project:update` / `env:update` action yet.
- **The secret value-ciphertext AAD binds `config_id`.** `internal/secrets/secrets.go` builds one AAD `dekAAD(proj.ID, cfg.ID+"/"+key, valueVersion)` and uses it for **both** the DEK wrap **and** the value `crypto.Encrypt`. A cloned config has a new `config_id`, so its secrets **cannot** be blob-copied — each value must be decrypted and re-encrypted under the new config's AAD. This is exactly what env→env promotion already does.
- **`secrets.RevealConfig`** returns a config's **own** latest secrets (`GetLatest(cfg.ID)`), not the inheritance-merged view — which is what a DAG-preserving clone needs (copy own values per config, preserve `inherits_from`, and resolution reproduces identically in the new env).
- **Generic `Idempotency-Key` middleware** (PR #79) already wraps mutating verbs; new POSTs inherit it.

## Approach

**Compose existing, reviewed services. No new crypto, no migration.**

Rejected alternative: a store-layer "fast copy" of secret rows. Impossible here — the value AAD binds `config_id`, so ciphertext can't be reused; any clone must decrypt→re-encrypt, which the `secrets` service already does safely. Doing it at the store layer would duplicate crypto outside the 100%-covered path. Rejected.

---

## Section A — Backend: `last_activity_at` metadata

Add a computed `last_activity_at` (RFC3339 string or `null`) to the **project list** and **environment list** responses.

- Value = `max(config_versions.created_at)` across the entity's live configs:
  - **Project:** `environments → configs → config_versions`, only live rows (`deleted_at IS NULL`).
  - **Environment:** `configs → config_versions`, live rows.
- Implemented as a `LEFT JOIN LATERAL` / grouped subquery in the existing `ListPage` / `ListByProjectPage` repo queries (or a sibling method the handler calls) — **one** extra query per list, no N+1. `NULL` when the entity has no config versions yet.
- No migration: `config_versions` and the paging indexes already exist.
- Response additions: `projectResponse.last_activity_at`, `envResponse.last_activity_at` as `*string` (always present in JSON; explicit `null` when the entity has no config versions yet, for a stable client shape).
- OpenAPI spec (`docs/openapi.yaml`) updated for the two responses; the drift test stays green (route set unchanged).

Web types gain `created_at: string` and `last_activity_at: string | null` on `Project` and `Environment`.

## Section B — Backend: rename (display name only)

- **New authz actions** `project:update` and `env:update`, both **admin+** — added to the admin and owner action sets next to `env:create`, and to `actions_test.go`'s expectation matrix.
- **Routes:**
  - `PATCH /v1/projects/{pid}` body `{ "name": "..." }`
  - `PATCH /v1/projects/{pid}/environments/{eid}` body `{ "name": "..." }`
- **`name` (display) only. `slug` is immutable.** Slug is the stable address used by cross-project references (`${projects.<slug>.<env>.KEY}`), token/CI bindings, and CLI addressing; mutating it would silently break references. Rename changes only the human-facing `name`.
- Handlers: `authorize(...)` with the new action + resolved resource (project resource for project; project→env resource for env), decode `{name}` (allow empty? no — reject empty/whitespace-only with `CodeValidation`), call repo `UpdateName`, emit value-free audit `project.update` / `env.update`, return the updated view.
- Repo: `ProjectRepo.UpdateName(ctx, id, name)` and `EnvironmentRepo.UpdateName(ctx, id, name)` — parameterized `UPDATE ... SET name=$2, updated_at=now() WHERE id=$1 AND deleted_at IS NULL`; `ErrNotFound` on zero rows.

## Section C — Backend: clone-environment

- **Route:** `POST /v1/projects/{pid}/environments/{eid}/clone` body `{ "slug": "...", "name": "..." }`. Clones within the **same project**.
- **Authz:** `env:create` on the project (admin+) — same gate as creating an environment. Admins already hold secret read/write across the project, so no additional grant is needed; clone materializes only values the caller may already read.
- **Semantics** (a new `Service.CloneEnvironment` orchestrating existing pieces, in a transaction where practical):
  1. Create the new environment (`slug`, `name`) under the project. Slug collision → `409 CodeConflict`.
  2. Enumerate the source env's **live** configs. Recreate each in the new env in **topological order** so a config is created after any config it inherits from; remap `inherits_from` from source id → new id. Cycle guard mirrors the board's `seen`-set walk (defensive; the DB shouldn't hold cycles).
  3. For each source config, read its **own** latest secrets via a server-internal decrypt (the same own-values path `RevealConfig` uses) and re-encrypt them into the freshly created config via the normal `SetSecrets` write, producing v1. **No per-key `secret.reveal` audit** and no reveal-metric inflation — this is an internal copy, not a user reveal (mirrors promotion).
  4. Configs with no own secrets clone as empty configs (inheritance still resolves through the remapped `inherits_from`).
- **Audit:** exactly one value-free `env.clone` event: actor, source env id, new env id, config count, result. Never any key names' values.
- **Idempotency:** inherits the generic `Idempotency-Key` middleware.
- **Failure handling:** if any step after env creation fails, the operation returns an error; partial state is acceptable only if unavoidable, but prefer wrapping config+secret creation so a mid-clone failure doesn't strand a half-populated env. (Env creation + config tree + secret writes should share a transaction or a compensating cleanup; the implementation plan pins the exact boundary against what the store layer supports.)
- **Out of scope for this slice:** `janus env clone` CLI (frontend is the §2.4 goal); can be a trivial follow-up over this endpoint.

## Section D — Frontend

**`web/src/home/ProjectsList.tsx` (the `/projects` cards):**
- Add the project **glyph** (reuse `home/glyph.ts` `glyphClass` + first-initial badge, matching `HomeProjects`).
- Add a recency line: **"active 2h ago"** when `last_activity_at` is set, else **"created 3d ago"** (relative time via `lib/relativeTime`).
- Hover **quick-action `⋯` menu** per card: **Rename** (opens a small name dialog → `PATCH` project) and the existing **Delete** (move to Trash). Keyboard-accessible; menu closes on select/Esc/outside-click.
- Sort select gains **Newest**, **Oldest**, **Recently active**, alongside **A–Z** / **Z–A**. "Recently active" sorts by `last_activity_at` (nulls last), created sorts by `created_at`.

**`web/src/home/ProjectBoard.tsx` (env columns):**
- Env column header becomes actionable: a `⋯` menu with **Rename** (→ `PATCH` env), **Clone environment** (opens a slug+name dialog → clone POST; on success invalidate envs + configs), and the existing **Delete**.
- Small **"active 2h ago"** subline per column (from the env's `last_activity_at`).
- **Inheritance connectors:** replace the bare `↳` + `ml-4` with a left rail + elbow connector so parent→child inheritance is visible. Cycle-safe (existing `ConfigNodes` walk). Reduced-motion respected; token-based colors only.

**Cross-cutting FE rules:** token classes only (no raw palette / `dark:` variants / hex), correct in both themes (`npm run smoke`), monospace reserved for keys/values. New components get focused tests; MSW mocks mirror the new response fields (`created_at`, `last_activity_at`).

## Testing & guardrails

- **Go:** table-driven tests for the aggregate query (project/env with 0, 1, N config versions incl. soft-deleted configs excluded); rename (authz matrix viewer/dev/admin/owner, empty-name rejection, slug-immutability, `ErrNotFound`); clone (config DAG remap + ordering, own-vs-inherited value copy correctness, empty-config clone, one value-free `env.clone` audit, slug collision → 409). Extend the existing **log/audit leak test** to cover the clone path (no plaintext in logs/audit/errors).
- **Web:** ProjectsList (glyph, recency line, sort orders incl. nulls-last, quick-action rename/delete), ProjectBoard (header menu, clone dialog, rename, connector rendering, cycle-safety). Dual-theme smoke.
- **Gates:** `go test ./... -race`, `gosec`, `govulncheck`, web `npm test`/`typecheck`/`smoke` all green. OpenAPI drift test green (2 new routes registered + spec updated).

## Non-goals

- Author/created-by metadata (no column; separate project).
- Slug editing (immutable by design).
- Full config-version history in clone (latest snapshot only).
- Cross-project clone.
- `janus env clone` CLI (possible trivial follow-up).
- Board features beyond §2.4 (env describe/notes fields, drag-reorder of columns, etc.).

## Rollout

Frontend-heavy with three thin backend additions; **no migration**. After merge, rebuild dev containers and re-unseal (:8210). Update gaps.md §2.4 to done and the progress memory.
