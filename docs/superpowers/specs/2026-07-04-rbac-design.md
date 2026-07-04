# RBAC (authorization engine) — Design Spec

## Overview

Milestone 6 adds `internal/authz`: a deny-by-default authorization engine that
decides whether an authenticated `Principal` may perform an action on a
resource. It builds directly on the M5 identity layer (`Principal{Kind,ID,Name}`
and service-token scopes) and gives the server real access control ahead of the
secret-facing REST API.

The engine is a **pure decision function** — a fixed role→action matrix in code
plus role bindings in Postgres — with no HTTP coupling and no runtime policy
configuration. Handlers enforce explicitly: each calls
`authz.Can(ctx, principal, action, resource)` for the exact action and resource
it is about to touch and returns `403` on denial.

M6 also closes the M5 deferrals: `POST /v1/sys/seal` and the token
mint/list/revoke endpoints become properly authorized, and the previously
deferred user CRUD lands here (you need users to assign roles to).

## Locked design decisions

1. **Scope model** — three levels: **instance**, **project**, **environment**.
   Top-down inheritance; a resource's effective permission is the **union
   (most-permissive)** of every applicable binding; **deny by default**.
2. **Enforcement** — **explicit handler checks** against a central
   `authz.Can(...)`; a thin `RequireInstanceRole` middleware guards the
   purely-instance gates. The engine stays HTTP-free and the `internal/secrets`
   service layer stays identity-free.
3. **Service-token mapping** — a token's `access` is a **secret capability at
   its scope** (`read → {secret:read, config:read}`, `readwrite → + secret:write`),
   strictly least-privilege. Tokens carry no role bindings and can never reach
   management or instance actions.
4. **Delegation** — per-scope: owners/admins manage membership within their own
   scope and **cannot grant a role above their own effective role**
   (granting `owner` requires being an owner). `owner` > `admin`: owner adds
   `project:delete` and owner-management.

## Architecture & package layout

**`internal/authz`** — a new pure-decision package:
- The **action vocabulary** (`resource:verb` string constants) and the four
  **roles** as hardcoded action-bundles (the matrix), all in code.
- `Resource{ProjectID, EnvID, ConfigID string}` — the target's scope chain; any
  field may be empty, and instance-scoped actions carry a zero-value `Resource`.
- `Authorizer.Can(ctx, principal, action, resource) error` → `nil` or
  `ErrForbidden`. It loads the principal's role bindings through a narrow store
  interface, resolves inheritance, and unions action sets. Service-token
  principals skip roles and take the scope+access capability path.
- The matrix and resolution logic are **pure functions** (`roleAllows`, the
  user/token resolvers), unit-tested exhaustively; the `Authorizer` only wraps
  them with the one binding load.

**`internal/store`** — a new `RoleBindingRepo` + migration `000003_rbac` (one
`role_bindings` table). Crypto-blind, standard repo pattern.

**`internal/api`** — handlers call `s.authz.Can(...)` explicitly and return
`403` on deny; `RequireInstanceRole(action)` middleware guards instance-only
routes.

**Layering:** `internal/authz` sits above `store`, is consumed by
`internal/api`, takes a `Principal` (from `internal/auth`) + a `Resource`, and
deliberately does **not** reach into `internal/secrets`.

### M6 scope

- `internal/authz` engine + `role_bindings` store + migration.
- Membership-management endpoints (grant/revoke at instance/project/env).
- Minimal user management (create/list/disable) — deferred to here from M5.
- Retrofit existing surfaces: `sys/seal` → `sys:seal`; token mint/list/revoke →
  proper authz (closes the M5 deferrals).
- Bootstrap: the init admin becomes **instance owner**; a startup reconciliation
  guarantees at least one instance owner always exists.
- Future secret/config/env handlers call `Can()` as they are built — M6 ships
  the engine they will use.

## Data model

Migration `000003_rbac`:

```sql
CREATE TABLE role_bindings (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    subject_user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    scope_level     text NOT NULL CHECK (scope_level IN ('instance','project','environment')),
    project_id      uuid REFERENCES projects(id) ON DELETE CASCADE,
    environment_id  uuid REFERENCES environments(id) ON DELETE CASCADE,
    role            text NOT NULL CHECK (role IN ('viewer','developer','admin','owner')),
    created_by      uuid REFERENCES users(id),   -- who granted it (null for bootstrap)
    created_at      timestamptz NOT NULL DEFAULT now(),
    CHECK ( (scope_level='instance'    AND project_id IS NULL     AND environment_id IS NULL)
         OR (scope_level='project'     AND project_id IS NOT NULL AND environment_id IS NULL)
         OR (scope_level='environment' AND environment_id IS NOT NULL AND project_id IS NULL) )
);

-- one role per subject per exact scope (COALESCE sentinels because NULLs are distinct):
CREATE UNIQUE INDEX role_bindings_scope_uniq ON role_bindings
    (subject_user_id, scope_level,
     COALESCE(project_id,     '00000000-0000-0000-0000-000000000000'),
     COALESCE(environment_id, '00000000-0000-0000-0000-000000000000'));
CREATE INDEX role_bindings_subject_idx ON role_bindings (subject_user_id);
```

Rationale:
- **FK cascades** keep bindings consistent automatically: hard-destroying a
  project deletes its environments, which deletes their env-level bindings;
  deleting a user deletes all their bindings. No dangling rows, no app cleanup.
- **Env-level bindings store only `environment_id`** (the env implies its
  project); a project binding cascades to nested resources by matching
  `project_id` at resolution time — no denormalized project id.
- **The uniqueness index** means a user holds at most one role per exact scope,
  but can hold different roles at different scopes; the union rule combines them.

`store.RoleBindingRepo` (standard repo pattern):
- `Create(ctx, in RoleBindingInput) (*RoleBinding, error)` — grant; idempotent
  upsert on the unique scope (re-granting updates the role).
- `ListForUser(ctx, userID) ([]*RoleBinding, error)` — all bindings for `Can()`.
- `ListForScope(ctx, level, scopeID) ([]*RoleBinding, error)` — members at a scope.
- `Delete(ctx, id)` / `DeleteForScope(...)` — revoke.
- `CountInstanceOwners(ctx) (int, error)` — powers "can't remove the last
  instance owner" and the startup reconciliation.

`RoleBinding` mirrors the columns (`ProjectID`/`EnvironmentID`/`CreatedBy` as
`*string`).

## Permission model

Actions are `resource:verb` strings. `secret:write` covers set/delete/rollback.
`config:create` makes a branch config; `config:delete` removes one.
`project:create`, `user:manage`, and `sys:seal` operate on an **instance-scoped**
resource (no project/env), so they are only reachable through an instance-level
binding.

The four roles are cumulative bundles:

| Action | viewer | developer | admin | owner |
|---|:--:|:--:|:--:|:--:|
| `secret:read` | ✓ | ✓ | ✓ | ✓ |
| `secret:write` | · | ✓ | ✓ | ✓ |
| `config:read` | ✓ | ✓ | ✓ | ✓ |
| `config:create` | · | ✓ | ✓ | ✓ |
| `config:delete` | · | · | ✓ | ✓ |
| `env:create` / `env:delete` | · | · | ✓ | ✓ |
| `project:read` | ✓ | ✓ | ✓ | ✓ |
| `project:create` † | · | · | ✓ | ✓ |
| `project:delete` | · | · | · | ✓ |
| `member:read` | ✓ | ✓ | ✓ | ✓ |
| `member:manage` ‡ | · | · | ✓ | ✓ |
| `token:read` / `token:mint` / `token:revoke` | · | · | ✓ | ✓ |
| `user:manage` † | · | · | ✓ | ✓ |
| `audit:read` | · | · | ✓ | ✓ |
| `sys:seal` † | · | · | ✓ | ✓ |

**† instance-scoped** — listed under admin/owner, but because the action acts on
an instance resource, only an *instance-level* binding reaches it. A
project-admin cannot create projects or seal the server; an instance-admin can.

**‡ delegation constraint** (enforced in the member handler, beyond the matrix):
a granter may only assign a role **≤ their own effective role** at that scope, so
granting `owner` requires being an owner; the last instance owner cannot be
removed (guarded by `CountInstanceOwners`).

**Union + inheritance:** the effective permission for a resource is the union of
actions from every applicable binding (instance always applies; a project
binding where `project_id` matches; an env binding where `environment_id`
matches). Example: viewer on project P + admin on env E (inside P) → a secret in
E gets admin (writable); a secret in sibling env E2 gets only viewer (read-only).

## Resolution algorithm

`Authorizer.Can(ctx, principal, action, resource) error` → `nil` or
`ErrForbidden`, deny-by-default.

**Service-token principals:**
- When `RequireAuth` verifies a service token it already loads the token row, so
  it stashes the token's `{scope_kind, scope_id, access}` in the request context
  beside the Principal (no re-query). `Can()` reads it via `tokenScopeFrom(ctx)`.
- Capability set: `read → {secret:read, config:read}`; `readwrite → + secret:write`.
- Scope containment: `scope_kind=config` applies iff `resource.ConfigID == scope_id`;
  `scope_kind=environment` applies iff `resource.EnvID == scope_id` (covers every
  config in that env).
- Allow iff `action ∈ capabilities` **and** the resource is within scope.
  Management/instance actions are never in the set, so a token structurally
  cannot reach them.

**User principals:**
- Load `RoleBindingRepo.ListForUser(ctx, principal.ID)` once per request.
- A binding is *applicable* when it is `instance`, or `project` with
  `project_id == resource.ProjectID`, or `environment` with
  `environment_id == resource.EnvID`.
- Allow iff any applicable binding's role bundle contains `action` (the union);
  otherwise `ErrForbidden`.

**Resource** is built by the handler from the (nested) route
(`{ProjectID, EnvID, ConfigID}`, zero-value for instance actions). `authz` does
**no** I/O to resolve the chain — nested routes already carry the full chain; on
the rare leaf-only path the handler resolves parents via the store first. This
keeps `authz` a pure decision function.

**Purity & testability:** `roleAllows(role, action)` and the user/token
resolvers are pure functions over `(bindings|scope, resource, action)`,
exhaustively table-tested; the `Authorizer` only wraps them with the binding
load.

## Enforcement points & endpoints

**Retrofit existing surfaces (closes the M5 deferrals):**
- `POST /v1/sys/seal` → `Can(sys:seal, instance)` — instance admin/owner
  (`RequireInstanceRole`).
- `POST /v1/tokens` (mint) → `Can(token:mint, resourceOf(requested scope))` —
  admin+ at the token's scope; the handler resolves the scope's project/env
  chain from the store to build the resource.
- `GET /v1/tokens` → **scope-filtered**: returns only tokens the caller passes
  `token:read` on (instance admin sees all).
- `DELETE /v1/tokens/{id}` → `Can(token:revoke, that token's scope)`.

**Membership endpoints** (nested; each carries its scope in the path):
- `GET|PUT|DELETE /v1/instance/members[/{userId}]`
- `GET|PUT|DELETE /v1/projects/{pid}/members[/{userId}]`
- `GET|PUT|DELETE /v1/projects/{pid}/environments/{eid}/members[/{userId}]`

`GET` needs `member:read`; `PUT`/`DELETE` need `member:manage` **plus** the
delegation constraint (no granting above your own effective role; no removing the
last instance owner). `PUT` body `{"role": "..."}` upserts the binding.

**Minimal user management** (instance, `user:manage`):
- `POST /v1/users {email}` → creates a user with a one-time generated password
  (returned once, same pattern as the bootstrap admin).
- `GET /v1/users` → list `{id, email, disabled}`.
- `POST /v1/users/{id}/disable` → sets `disabled_at`; guarded against disabling
  yourself or the last instance owner. Adds a small `UserRepo.SetDisabled`.

**Bootstrap & the never-lock-out invariant:**
- `CreateInitialAdmin` returns the new user's id; the init handler then grants it
  an **instance-owner** binding.
- `Boot` runs a **reconciliation**: if users exist but `CountInstanceOwners == 0`,
  grant the oldest user instance owner and log it. This self-heals an M5→M6
  upgrade (admin exists with no binding) and guarantees the server can never end
  up with nobody able to administer it. Migrations stay schema-only.

**Middleware:** `RequireInstanceRole(action)` wraps purely-instance routes;
project/env routes call `Can` inline (they need the path IDs).

## Error handling

- **Deny → `403 {"error":{"code":"forbidden","message":"access denied"}}`.** New
  `CodeForbidden = "forbidden"`. Generic message; never names the missing action
  or role (no policy leak).
- **401 vs 403:** `401 unauthenticated` (no/invalid credential) stays distinct
  from `403 forbidden` (valid principal, insufficient role); a sealed keyring
  still surfaces as `503` ahead of any authz check.
- **403 vs 404:** list endpoints are scope-filtered (you see only what you can
  read); item endpoints return `404` for genuinely-absent resources and `403`
  for existing-but-forbidden ones. The mild enumeration signal (403 confirms
  existence) is accepted for a single-tenant tool where members are not
  adversarial, and is far clearer for legitimate users than a misleading 404.
- **Audit hook:** authz denials and every grant/revoke are exactly the events the
  audit milestone will record; M6 leaves those call-sites as attach points but
  does not emit yet (no audit engine until the next milestone).

## Testing

- **Matrix tests** — table-driven, *exhaustive* over every (role, action) pair
  against the matrix, so the bundle cannot silently drift.
- **Resolution** — inheritance (instance/project/env), union
  (`viewer@P + admin@E` → admin in E), sibling isolation (`E` binding does not
  grant `E2`), deny-by-default.
- **Service-token least-privilege** — read cannot write, out-of-scope denied,
  management/instance actions unreachable, config- vs env-scope containment.
- **Delegation** — admin cannot grant owner, owner can, the last instance owner
  cannot be removed, self/last-owner cannot be disabled.
- **Bootstrap** — init admin gets instance owner; `Boot` reconciliation grants
  the oldest user when none exists.
- **e2e (real stack)** — create user → grant role → exercise the live surfaces
  (members, tokens, sys/seal): developer cannot seal, admin mints tokens, token
  list is scope-filtered, instance owner manages instance members; assert the
  403s.
- **Gates** — `internal/authz` pure logic held to **100% coverage** (like
  `internal/crypto`); plus gosec, govulncheck, and a check that 403 bodies carry
  no policy internals.

## Non-goals / deferred

- **Audit emission** — the attach points exist; the hash-chained audit engine is
  the next milestone.
- **Secret/config/env HTTP routes** — those arrive with the REST API milestone
  and will call `Can()` as they are built.
- **Runtime-configurable roles/permissions** — the matrix is fixed in code
  (single-tenant, four roles); no data-driven policy.
- **Per-token explicit action grants** — the shipped `read`/`readwrite` scope
  model is sufficient.
- **OIDC / federated principals** — Phase 2; `Principal` already leaves room, and
  `Can()` treats any future principal kind as deny-by-default until mapped.
