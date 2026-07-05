# Milestone 8 — Secret-facing REST API — design

**Package:** `internal/api` (new handlers + routes over the existing
`internal/secrets` service and store repos). **Status:** design approved, pre-plan.

## Goal

Expose the project → environment → config → secret hierarchy and its two-level
versioning over `/v1/` HTTP, so Janus becomes usable as a secrets manager without
the CLI (which follows in the next milestone). Every route is RBAC-enforced,
value reveals and mutations are audited (the M7 seam), and no secret value ever
enters a log, error, or masked response. This is the phase-1 route that turns the
already-built `internal/secrets` service into a network API.

## Locked decisions (brainstorming)

1. **URL model — nested paths, opaque UUID params.** `/v1/projects/{pid}`,
   `/v1/projects/{pid}/environments/{eid}`, and config/secret/version operations
   under `/v1/configs/{cid}/…`. Path params are the same UUIDs the secrets
   service and the RBAC resolver already take. Consistent with the M6/M7
   member routes; no slug→id resolution machinery.
2. **Reveal model — endpoint-encoded.** A masked collection read is unaudited;
   fetching a single named secret reveals its value and audits; `?reveal=true` on
   the collection reveals all values in one shot and audits. Maps 1:1 to
   `ListSecrets` (masked) / `GetSecret` (one) / `RevealConfig` (all).
3. **Write model — batch + per-key convenience.** `PUT …/secrets` with a change
   list is one atomic save → one new config version; `PUT …/secrets/{key}` and
   `DELETE …/secrets/{key}` are one-item batches → one new version each. All map
   to `SetSecrets`.
4. **Idempotency — natural only; header deferred.** Unique slug/name constraints
   return `409` on duplicate creates; a retried secret write may create a
   duplicate (immutable, diffable, rollback-able) version. `Idempotency-Key` is
   deferred to the CLI milestone, when retries actually happen.
5. **Delete model — full lifecycle.** `DELETE` soft-deletes (recoverable);
   `POST …/restore` undeletes; `DELETE …?destroy=true` hard-destroys
   (owner-gated, irreversible, cascades to the whole subtree via a new
   `ON DELETE CASCADE` migration — see Architecture). For projects,
   environments, and configs. Secret deletion is a separate versioned tombstone
   via `DELETE …/secrets/{key}`.

## Architecture

Thin handlers in `internal/api` reuse the existing layers — no new package:

- **Crypto operations** go through `internal/secrets.Service`: project create
  (generates + wraps the project KEK), secret set/reveal/rollback, version diff.
- **Crypto-blind hierarchy operations** (get/list/soft-delete/undelete/destroy of
  project/env/config) call the store repos directly — exactly how the
  token/user/member handlers already call `store.NewXRepo(s.st)`.
- **The M6/M7 seam is reused unchanged:** `s.authorize(w, r, action, res,
  auditAction, auditResource)` (RBAC check + centralized denial audit),
  `s.can(r, action, res)` (bare check for masked reads), `s.record(r, …)`
  (fail-closed success audit), and `resolveScopeResource(ctx, "config", cid)`
  (the config→env→project chain for inheritance-aware checks).

The store already backs every operation: `ProjectRepo`/`EnvironmentRepo`/
`ConfigRepo` each expose `Create`/`Get`/`List*`/`SoftDelete`/`Undelete`/
`Destroy`; `secrets.Service` exposes `CreateProject`/`CreateEnvironment`/
`CreateConfig`/`SetSecrets`/`GetSecret`/`RevealConfig`/`GetSecretVersion`/
`ListSecrets`/`KeyHistory`/`ListVersions`/`DiffVersions`/`Rollback`.

`internal/audit` needs **no change**: `Event.Action` is a free-form string, so the
new action names are just new strings passed to `s.record`.

**One schema change — migration `000005`** — alters the ownership foreign keys to
`ON DELETE CASCADE` so `Destroy` reclaims a resource's whole subtree (the core
schema shipped them as `NO ACTION`, which would fail on any non-empty resource);
`configs.inherits_from` is left `NO ACTION` on purpose. Details under "Error
handling & edge cases". No store repo signatures change — `Destroy` already
issues the `DELETE`; the cascade is now honored by the database.

### File organization

New handler files in `internal/api`, split by resource so each stays focused
(mirroring `tokens_handlers.go` / `members_handlers.go`):

- `projects_handlers.go` — project CRUD + lifecycle.
- `environments_handlers.go` — environment CRUD + lifecycle.
- `configs_handlers.go` — config CRUD + lifecycle.
- `secrets_handlers.go` — masked list, reveal (one/all/historical), batch +
  per-key write, delete.
- `versions_handlers.go` — version list, diff, rollback, per-key history.
- Routes registered in `server.go` inside the existing
  `if s.auth != nil && s.authz != nil { … }` block.

## Route surface

```
Projects
  POST   /v1/projects                                   project:create (instance)  201 {id,slug,name,created_at}
  GET    /v1/projects                                    project:read   200 {projects:[…]}  (filtered by can)
  GET    /v1/projects/{pid}                               project:read   200
  DELETE /v1/projects/{pid}[?destroy=true]                project:delete 204  soft | hard(owner)
  POST   /v1/projects/{pid}/restore                       project:delete 200  {restored resource}

Environments
  POST   /v1/projects/{pid}/environments                 env:create     201 {id,project_id,slug,name,created_at}
  GET    /v1/projects/{pid}/environments                  project:read   200 {environments:[…]}
  GET    /v1/projects/{pid}/environments/{eid}             project:read   200
  DELETE .../environments/{eid}[?destroy=true]            env:delete     204
  POST   .../environments/{eid}/restore                   env:delete     200

Configs
  POST   /v1/projects/{pid}/environments/{eid}/configs    config:create  201 {id,environment_id,name,inherits_from,created_at}
  GET    .../environments/{eid}/configs                   config:read    200 {configs:[…]}
  GET    /v1/configs/{cid}                                 config:read    200
  DELETE /v1/configs/{cid}[?destroy=true]                 config:delete  204
  POST   /v1/configs/{cid}/restore                        config:delete  200

Secrets (under a config)
  GET    /v1/configs/{cid}/secrets                        secret:read    200  masked list        (NOT audited)
  GET    /v1/configs/{cid}/secrets?reveal=true            secret:read    200  reveal all         (audited)
  GET    /v1/configs/{cid}/secrets/{key}                   secret:read    200  reveal one         (audited)
  GET    /v1/configs/{cid}/secrets/{key}?version={n}       secret:read    200  reveal historical  (audited)
  GET    /v1/configs/{cid}/secrets/{key}/history           secret:read    200  masked KeyHistory  (NOT audited)
  PUT    /v1/configs/{cid}/secrets                         secret:write   200  batch save   -> new version
  PUT    /v1/configs/{cid}/secrets/{key}                   secret:write   200  one-item save-> new version
  DELETE /v1/configs/{cid}/secrets/{key}                   secret:write   200  tombstone    -> new version

Versions
  GET    /v1/configs/{cid}/versions                        config:read    200  {versions:[…]}
  GET    /v1/configs/{cid}/versions/diff?a={n}&b={n}        config:read    200  {a,b,added,changed,removed}
  POST   /v1/configs/{cid}/rollback                        secret:write   200  {target_version,message?} -> new version
```

Config/secret/version operations key on `{cid}` alone (a config id is globally
unique; nesting the full path under every secret op is noise); project/env
creation stays nested for parent context.

**Two version namespaces (do not conflate):** a **config version** is the unit of
save/diff/rollback (`/versions`, `/versions/diff?a=&b=`, `rollback`'s
`target_version` — all integers over the config). A **value version** is one
key's own value history (`/secrets/{key}/history`, and `/secrets/{key}?version=n`
reveals a specific historical value). `SetSecrets` creates one config version
that bumps the value version of each changed key.

## Request / response bodies

Secret values are UTF-8 JSON strings on the wire (`[]byte` ⇄ string). Binary
values are out of scope (a future `encoding` flag).

```jsonc
// Project create  {slug,name}          201 -> {id,slug,name,created_at}
// Env create      {slug,name}          201 -> {id,project_id,slug,name,created_at}
// Config create   {name,inherits_from?} 201 -> {id,environment_id,name,inherits_from,created_at}

// Masked list  GET .../secrets                       200 (NOT audited)
{ "version": 7,
  "secrets": { "DB_URL": {"value_version":3,"created_at":"…"},
               "API_KEY": {"value_version":1,"created_at":"…"} } }

// Reveal one   GET .../secrets/{key}                 200 (audit secret.reveal)
{ "key":"DB_URL", "value":"postgres://…", "value_version":3 }

// Reveal all   GET .../secrets?reveal=true           200 (audit secret.reveal, detail=all)
{ "version":7, "secrets": { "DB_URL":"postgres://…", "API_KEY":"…" } }

// Key history  GET .../secrets/{key}/history         200 (NOT audited)
{ "key":"DB_URL", "history":[ {"value_version":3,"created_at":"…"}, … ] }

// Batch save   PUT .../secrets                       200 -> {version:8,id,created_at}
{ "message":"rotate db creds",
  "changes":[ {"key":"DB_URL","value":"postgres://new"},
              {"key":"OLD","delete":true} ] }

// Per-key      PUT .../secrets/{key}   {value:"…"}    200 -> {version:9,…}
// Delete key   DELETE .../secrets/{key}              200 -> {version:10,…}

// Versions     GET .../versions   200 -> {versions:[{version,message,created_by,created_at}]}
// Diff         GET .../versions/diff?a=5&b=8  200 -> {a:5,b:8,added:[],changed:[],removed:[]}  (key names only)
// Rollback     POST .../rollback  {target_version:5,message?}  200 -> {version:11,…}
```

**Status codes:** `201` on resource creation (project/env/config); `200` on
reads, secret writes (returns the new version metadata), rollback, and restore
(returns the restored resource); `204` on soft-delete and hard-destroy. Secret
writes return the new config-version metadata, not a `Location` (the collection
is versioned, not a new addressable resource).

**Version attribution:** `SetSecrets`/`Rollback` take an `actor` string; handlers
pass the principal id, so each config version records who saved it (`created_by`)
for history — independent of, and in addition to, the tamper-evident audit event.

## Authorization & audit

Every route's gate, RBAC resource, and audit event:

| Route | Action | RBAC resource | Gate → audit |
|---|---|---|---|
| POST `/v1/projects` | `project:create` | `Instance()` | `authorize` → `project.create` |
| GET projects, `/projects/{pid}` | `project:read` | `{ProjectID}` | `can` (list filtered by `can`); no audit |
| DELETE `/projects/{pid}` (`?destroy`) | `project:delete` | `{ProjectID}` | `authorize` → `project.delete` (detail `soft`\|`destroy`) |
| POST `/projects/{pid}/restore` | `project:delete` | `{ProjectID}` | `authorize` → `project.restore` |
| POST `.../environments` | `env:create` | `{ProjectID}` | `authorize` → `env.create` |
| GET environments | `project:read` | `{ProjectID,EnvID}` | `can`; no audit |
| DELETE/restore env | `env:delete` | `{ProjectID,EnvID}` | `authorize` → `env.delete`/`env.restore` |
| POST `.../configs` | `config:create` | `{ProjectID,EnvID}` | `authorize` → `config.create` |
| GET config(s) | `config:read` | resolve `{cid}` | `can`; no audit |
| DELETE/restore config | `config:delete` | resolve `{cid}` | `authorize` → `config.delete`/`config.restore` |
| GET masked list, `/versions`, `/versions/diff`, `.../{key}/history` | `secret:read` / `config:read` | resolve `{cid}` | `can`; **no audit** |
| GET `.../secrets/{key}`, `?reveal=true`, `?version=n` | `secret:read` | resolve `{cid}` | `authorize` → `secret.reveal` |
| PUT `.../secrets`, `.../secrets/{key}` | `secret:write` | resolve `{cid}` | `authorize` → `secret.write` (detail = key count / key) |
| DELETE `.../secrets/{key}` | `secret:write` | resolve `{cid}` | `authorize` → `secret.delete` |
| POST `.../rollback` | `secret:write` | resolve `{cid}` | `authorize` → `config.rollback` (detail = target version) |

- **`{cid}` resolution** reuses `resolveScopeResource(ctx, "config", cid)` → the
  full project→env→config chain; a missing id returns `store.ErrNotFound` → `404`
  before any authz decision.
- **Reveal/write vs masked** follows the M7 contract exactly: reveals and
  mutations use `s.authorize` (denials audited; success recorded post-action;
  fail-closed 500 if the record write fails), masked/metadata reads use `s.can`
  (403 without an audit event) — consistent with token/user/member list reads.
- **Values never enter authz or audit:** `resource`/`detail` carry only paths,
  key names, and counts; `Diff` and masked responses are key-names-only by
  construction; the `Event` type has no value field.
- **The `project:read` list** (`GET /v1/projects`) returns only projects the
  caller can `project:read`, filtered in-handler like `handleTokenList`.

## Error handling & edge cases

The project-wide `{"error":{"code","message"}}` envelope throughout; internals
never leak.

- **Sealed server:** `RequireUnsealed` 503s every non-`/v1/sys/` route, so all
  secret routes (even masked reads) return `503 sealed` while sealed.
  `secrets.ErrSealed` is a defense-in-depth fallback, mapped to `503`.
- **Error mapping:** `store.ErrNotFound` → `404 not_found`; `ErrAlreadyExists`
  (duplicate slug/name) → `409 conflict`; invalid key/version/body → `400
  validation`; RBAC deny → `403 forbidden`; anything else → `500 internal`.
- **Batch writes are all-or-nothing:** one `SetSecrets` call = one transaction =
  one version. Any invalid key (`validateKey`) or a **duplicate key within the
  batch** rejects the whole save `400`; no version is created.
- **Values:** empty string is a valid value (distinct from delete). Binary is out
  of scope.
- **Rollback:** to a missing target → `404`; to the current version → allowed
  (repoints at existing ciphertext, new version, no re-encryption), never an
  error.
- **Soft-deleted reads:** a soft-deleted project/env/config (and its secrets)
  reads as `404 not_found`; `restore` brings it back. Deleted IDs are
  indistinguishable from never-existed (no existence oracle).
- **Hard-destroy cascades (via migration `000005`).** The core schema's
  ownership foreign keys were originally `NO ACTION`, so `Destroy` would fail on
  any non-empty resource. Migration `000005` alters the **ownership** FKs to
  `ON DELETE CASCADE` — `environments.project_id`, `configs.environment_id`,
  `secret_values.config_id`, `config_versions.config_id`, and
  `config_version_entries` (→ `config_versions`, `secret_values`) — so a
  `?destroy=true` removes the whole subtree in one delete: a destroyed project
  takes its environments, configs, config versions, secret values, and its
  wrapped KEK with it. **`configs.inherits_from` stays `NO ACTION`
  deliberately** — a branch config must not be silently deleted because its base
  was destroyed, so destroying a config still referenced as an inheritance base
  returns `409 conflict` (`store.ErrParentNotFound`). Hard-destroy is
  irreversible; owner-gated for projects (`project:delete` is owner-only),
  admin-gated for env/config. `?destroy=true` is the only hard-delete path.

## Testing

- **Per-resource e2e** (testcontainers Postgres, real audit recorder): full
  lifecycle — create project→env→config, batch-set secrets, reveal one/all/
  historical, masked list + key history, per-key set/delete, diff, rollback,
  soft-delete→restore→destroy — asserting status codes and response shapes.
- **RBAC enforcement**, table-driven role×action: viewer can reveal + list but
  not write; developer can write + create config but not delete config or
  destroy; admin can delete/create env; only owner can destroy a project.
  Denials return a generic `403`.
- **Audit integration:** reveal emits `secret.reveal`, writes emit
  `secret.write`/`secret.delete`, rollback emits `config.rollback`; masked list/
  history/diff emit nothing; a denied write emits a `denied` row; the chain still
  `verify`s after a mixed flow.
- **Leak test:** extend the existing HTTP leak harness to push a known secret
  value through the new write/reveal routes and assert it never appears in any
  error response or log line.
- **Gates:** `go build`/`go vet`/`go test ./...`, `gosec`
  (`-exclude-dir=internal/crypto/shamir`), `govulncheck`. No new pure-logic
  package, so no new 100%-coverage target — handlers are integration-tested
  against real Postgres.

## Non-goals (scope discipline)

- **Config inheritance resolution** and **secret references**
  (`${projects.other.prod.KEY}`) — deferred to their own later spec; configs
  store `inherits_from` but the API does not resolve it yet.
- **Cursor pagination** — lists return full sets (single-tenant, bounded);
  deferred until a list can grow unbounded (version history is the first
  candidate).
- **`Idempotency-Key`** — deferred to the CLI milestone.
- **Binary-value encoding** — values are UTF-8 strings for now.
- **Download-format conversion** (`env`/`json`/`yaml`) — belongs to the secrets
  CLI milestone (`janus secrets download`), not the HTTP API.
