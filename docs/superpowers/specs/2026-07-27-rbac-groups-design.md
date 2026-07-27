# Group-based role bindings — design

**Date:** 2026-07-27
**Tracker:** `status.md` → "Open — RBAC at organisation scale", item (1); mirrored in `docs/roadmap.md`.
**Status:** approved (decisions 1–3 confirmed with the maintainer; the remainder decided under delegated authority and recorded here).

## Problem

Role bindings are per-user only. An admin repeats the same grant for every
person on every project, and offboarding is a hunt for individual rows across
instance, project and environment scopes. The target shape — an organisation
with many product teams, each owning some projects, unable to see each other's
secrets — is *expressible* today but not *manageable*: it is approximated by
dozens of per-user rows that nothing keeps in step with the org chart.

The visibility half already works and is **not** rebuilt here. List endpoints
filter per item on the caller's permissions, so an account with no bindings sees
`projects: 0`. What is missing is managing that arrangement at scale.

## Goal

A binding may name a **group** instead of a user. Group membership comes from
the IdP's OIDC group claim where one is configured, and from an
admin-curated member list where one is not.

## Decisions

### 1. Two kinds of group, one discriminated table (approved)

A group is `kind = 'oidc'` or `kind = 'local'`, never both:

- **`oidc`** — carries a `claim_value`. Membership is a snapshot refreshed from
  the group claim at each OIDC login. An admin can never hand-add a member.
- **`local`** — no claim value. Membership is an explicit, admin-managed list.
  This is what an org without an IdP uses, and it covers password logins.

Rejected: a single group entity with two membership sources (an IdP-fed group
that also accepts hand-added members). Three edge cases killed it:

- **Cross-authority collision.** If the match key is the group's name, a Janus
  admin's local `payments` group and an identity team's unrelated Entra
  `payments` group become the same group. Neither admin can see the other's
  action, and everyone in the Entra group inherits whatever the local group was
  bound to. Avoiding this requires an explicit opt-in claim value — at which
  point the model *is* this one, plus hybrid groups.
- **Incident-born permanent grants.** During an IdP outage the obvious fix is
  "add them locally to the IdP group". That grant then outlives the incident
  forever, because a login sync only ever clears rows it owns. Janus already has
  the correct tool for temporary access — break-glass, TTL-clamped, loudly
  audited, self-revoking. A hybrid group would be a second elevation path with
  none of those properties.
- **The invariant.** With two kinds we can state and hold: *access granted via
  an IdP group is fully described by the IdP*. An access review run against
  Entra is therefore complete for those bindings, and anything granted outside
  it necessarily shows up as a local-group binding. A hybrid model makes that
  review return a clean result that is not true — the same failure mode the sync
  drift work avoided with `values_compared: false`.

A second, unplanned benefit: Entra emits group **GUIDs** in the `groups` claim
by default. Splitting `name` (display) from `claim_value` (matcher) means a
binding reads "Team Payments", not `a1b2c3d4-…`.

### 2. Authority ceiling (approved)

- **A group binding may grant `viewer`, `developer` or `admin` — never
  `owner`.** `owner` rotates the master key, prunes the audit chain and
  hard-destroys secret versions: the destroy-the-evidence tier. Making it
  group-derived hands it to whoever administers the IdP — typically the identity
  team, not the Janus operator — who can add themselves silently and whose
  membership list Janus cannot authoritatively enumerate (the snapshot only
  covers users who have logged in). Instance ownership must be a deliberate act
  recorded here. Enforced twice: a `CHECK` on the table and a `400` at the API.
- **Group-derived roles count toward `BoundRole`.** A group binding is durable,
  the same kind of thing as a direct binding, so the M-1 delegation cap treats
  it the same. M-1's actual invariant is untouched: break-glass grants still
  arrive through `GrantStore`, never through a binding source.
- **`CountInstanceOwners` is unchanged** — a consequence of the first rule. All
  owners are direct bindings, so the never-lock-out guard keeps working exactly
  as written and an IdP outage can never strand the instance without an owner.

### 3. The engine stays a pure decision function

Group bindings reach the engine as ordinary `[]*store.RoleBinding` values from
an **optional second binding source**, mirroring the existing `WithGrants`
pattern:

```go
func (e *Engine) WithGroups(g GroupBindingStore) *Engine
```

`userAllows`, `bindingApplies`, `BoundRole` and `EffectiveRole` gain no new
concepts — they see one longer slice. An engine built without `WithGroups`
behaves exactly as it does today.

Rejected: folding a `UNION ALL` into `RoleBindingRepo.ListForUser`. It couples
the repos and edits SQL on the hottest security path in the system for no gain.
Two indexed lookups per check is what `Can()` already does with break-glass.

Each group-derived binding carries `ViaGroupID *string` (nil = direct) so the
API can explain *why* a user has access. `bindingApplies` ignores it.

### 4. Managing groups vs. binding groups are different authorities

- **`group:manage`** (new action, instance-scoped, admin+) — create/delete
  groups, edit local membership, set the claim mapping. Curating the catalog is
  a directory operation, like `user:manage`.
- **Binding** a group at a scope reuses **`member:manage`** at that scope, plus
  the identical `BoundRole` delegation cap `memberPut` applies. Without the cap,
  groups would be a way around M-1.

The separation is load-bearing. A project admin (`member:manage` on project A
only) can bind any group to project A up to their own role — the same authority
they already have over users — but cannot add themselves to a group, because
that needs instance-scoped `group:manage`. So they cannot reach project B.

### 5. Membership is stored, not carried in the session

`Can()` resolves everything from `p.ID`; `BoundRole` is called from `memberPut`
with no session at all. Membership is therefore a property of the **user**, not
the session: a snapshot table read live on every decision.

Consequences, all intended:

- Revoking a group binding, deleting a group, or removing a local member takes
  effect on the **next request** — permissions are never frozen into a session.
- A user who logs in with a password after having logged in via OIDC keeps
  their IdP-derived groups. The snapshot is theirs, not their session's.
- The only staleness is the OIDC snapshot itself, which refreshes at login.
  Removal in the IdP therefore reaches Janus within one session lifetime —
  **24h maximum** (`sessionTTL`, non-sliding), less with
  `JANUS_SESSION_IDLE_TIMEOUT`. The instant levers are revoking the binding or
  the user's sessions, both of which already exist.

## Data model (migration `000045`)

```sql
CREATE TABLE groups (
    id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    name         text NOT NULL,
    kind         text NOT NULL CHECK (kind IN ('oidc','local')),
    claim_value  text,
    description  text NOT NULL DEFAULT '',
    created_by   uuid REFERENCES users(id),
    created_at   timestamptz NOT NULL DEFAULT now(),
    UNIQUE (name),
    UNIQUE (id, kind),                                    -- composite FK target
    CHECK ((kind = 'oidc') = (claim_value IS NOT NULL))
);
CREATE UNIQUE INDEX groups_claim_value_uniq
    ON groups (claim_value) WHERE kind = 'oidc';

CREATE TABLE group_members (
    group_id    uuid NOT NULL,
    group_kind  text NOT NULL,
    user_id     uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    created_by  uuid REFERENCES users(id),                -- NULL for sync rows
    created_at  timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (group_id, user_id),
    FOREIGN KEY (group_id, group_kind) REFERENCES groups(id, kind) ON DELETE CASCADE
);
CREATE INDEX group_members_user_idx ON group_members (user_id);

CREATE TABLE group_role_bindings (
    id             uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    group_id       uuid NOT NULL REFERENCES groups(id) ON DELETE CASCADE,
    scope_level    text NOT NULL CHECK (scope_level IN ('instance','project','environment')),
    project_id     uuid REFERENCES projects(id)     ON DELETE CASCADE,
    environment_id uuid REFERENCES environments(id) ON DELETE CASCADE,
    role           text NOT NULL CHECK (role IN ('viewer','developer','admin')),
    created_by     uuid REFERENCES users(id),
    created_at     timestamptz NOT NULL DEFAULT now(),
    CHECK ( (scope_level='instance'    AND project_id IS NULL     AND environment_id IS NULL)
         OR (scope_level='project'     AND project_id IS NOT NULL AND environment_id IS NULL)
         OR (scope_level='environment' AND environment_id IS NOT NULL AND project_id IS NULL) )
);
CREATE UNIQUE INDEX group_role_bindings_scope_uniq ON group_role_bindings
    (group_id, scope_level,
     COALESCE(project_id,     '00000000-0000-0000-0000-000000000000'),
     COALESCE(environment_id, '00000000-0000-0000-0000-000000000000'));
CREATE INDEX group_role_bindings_group_idx ON group_role_bindings (group_id);
```

Three things the schema enforces that code must not be trusted to:

- `group_members.group_kind` + the composite FK make "a local member inside an
  OIDC group" **unrepresentable**, rather than merely rejected by a handler.
  Sync writes `'oidc'`, the admin path writes `'local'`; each fails loudly
  against the wrong group.
- `role` omits `'owner'` on group bindings (decision 2).
- The scope-shape `CHECK` and the unique index mirror `role_bindings` exactly,
  so group bindings inherit the same cascade behaviour when a project or
  environment is deleted.

The OIDC group claim is configured on the existing provider row:

```sql
ALTER TABLE oidc_providers ADD COLUMN groups_claim text NOT NULL DEFAULT '';
```

Empty = group sync **disabled**. Deny-by-default: an operator opts in.

## OIDC group sync

At `resolveOIDCLogin`, after the ID token is verified and the user is resolved:

1. If `groups_claim` is empty → do nothing.
2. Extract the claim at the configured (possibly dotted) path, reusing the
   fail-closed flattening rule already used by CI federation: a path that is
   genuinely ambiguous (`{"a.b": …}` vs `{"a": {"b": …}}`) **rejects** rather
   than picking a precedence.
3. Map claim values → `groups` rows with `kind='oidc'`, and replace the user's
   `kind='oidc'` membership rows **in one transaction** (delete-not-in, insert
   missing). There is never a window where the user has zero groups.
4. Unknown claim values match no group and grant nothing. Groups are not
   auto-created — a claim value only matters once an admin has created a group
   for it and bound it.

### Claim edge cases

| Case | Handling | Why |
|---|---|---|
| Claim is a JSON array of strings | Normal path | The common shape |
| Claim is a single JSON string | Treated as a one-element list | Some IdPs emit one group unwrapped |
| Claim contains non-string elements | **Unknown** — snapshot untouched, login fails | A partially-parsed authorization input must not be treated as authoritative |
| Claim absent, no overage marker | **Empty** — snapshot cleared | The user is in no groups. Fails *closed* (access is lost, not gained), which is the safe direction if an operator misconfigures the IdP |
| Claim absent, `_claim_names`/`_claim_sources` names it | **Unknown** — snapshot retained, `group.sync` audited as `overage`, surfaced in `GET /v1/sys/status` | Entra replaces `groups` with a Graph pointer past ~200 groups. Treating that as "empty" would clear every membership and read as a legitimate removal from every group |
| Delimited string (`"a b"`, `"a,b"`) | **Not split** — treated as one value | Splitting invents a parse Janus cannot verify and breaks any group whose name contains the delimiter |

The overage case is the one place this design retains stale membership without a
bound. It cannot *grant* anything new — only preserve what the last good sync
recorded — and it is loud in the audit chain and the status endpoint. The
operator's fix is IdP-side (assign the app a filtered group set). Recorded as a
known limitation rather than silently absorbed.

**Sync failure fails the login.** Writing the snapshot is a mutation of
authorization state; completing a login against a snapshot we just failed to
update is precisely the silent-stale case this feature exists to remove.

## API

```
GET    /v1/groups                          group:manage        list (kind, name, claim_value, counts)
POST   /v1/groups                          group:manage        create
GET    /v1/groups/{gid}                    group:manage        detail + bindings
DELETE /v1/groups/{gid}                    group:manage        delete (cascades members + bindings)
GET    /v1/groups/{gid}/members            group:manage        members (source implied by kind)
PUT    /v1/groups/{gid}/members/{uid}      group:manage        add — local groups only, 409 on oidc
DELETE /v1/groups/{gid}/members/{uid}      group:manage        remove — local groups only, 409 on oidc

GET    /v1/groups/{gid}/bindings           group:manage        where this group grants access
PUT    /v1/{scope}/group-members/{gid}     member:manage@scope bind (role ≤ BoundRole, never owner)
DELETE /v1/{scope}/group-members/{gid}     member:manage@scope unbind
GET    /v1/{scope}/group-members           member:read@scope   group bindings at this scope
```

`{scope}` follows the existing member routes: `instance`, `projects/{pid}`,
`environments/{eid}`. The env scope resolves its parent chain via
`resolveScopeResource`, never the path `pid` — the same IDOR guard `envScope`
already applies.

Cursor pagination on the list endpoints, per API conventions.

## Audit

New value-free events (group names and ids only — no claim payloads, no
values): `group.create`, `group.delete`, `group.member.add`,
`group.member.remove`, `group.binding.grant`, `group.binding.revoke`, and
`group.sync`.

`group.sync` is emitted **only on change** (a non-empty added/removed set) or on
an `overage`/parse failure — not on every OIDC login, which would drown the
ledger.

## UI

- **`/groups`** (admin) — the catalog: create local or OIDC groups, edit local
  membership, view the OIDC snapshot read-only with its last-sync stamp, and see
  every scope a group is bound to.
- **Members screens** (instance / project / environment) gain a **Groups**
  section alongside users, using the existing ledger table primitive.
- The RBAC matrix gains group rows, so "who can write prod" answers with the
  group included.

Atrium rules apply: tokens only, both themes, no native dialogs.

## CLI

`janus group list|create|delete|members|add-member|remove-member|bind|unbind`,
consistent with the existing `janus project|env|config|token` control plane.

## Security invariants (each gets a test)

1. A local member cannot exist in an OIDC group — unrepresentable in the schema.
2. A group binding cannot carry `owner` — `CHECK` + API rejection.
3. Binding a group is capped by the granter's `BoundRole`, not `EffectiveRole`,
   so break-glass cannot be laundered into a durable group binding (M-1).
4. Group-derived access is revoked on the next request after unbinding — no
   session-frozen permissions.
5. A project admin cannot reach another project by way of groups.
6. An ambiguous or malformed group claim never produces a partial snapshot.
7. Deleting a project/environment cascades group bindings, exactly as it does
   direct bindings.

## Testing

- Table-driven unit tests for claim extraction (array, single string,
  non-string element, absent, overage marker, nested path, ambiguous path).
- `internal/authz` tests: group bindings union with direct ones; `BoundRole`
  includes them; break-glass still excluded.
- Store integration tests (testcontainers) for the schema invariants above —
  including asserting the local-member-in-OIDC-group insert *fails*.
- API e2e over the seven invariants.
- Existing leak test extended: no claim values in logs or errors.

## Out of scope

Deliberately not in this build, and tracked separately in `status.md`:

- Delegated project creation (item 2) and scoped audit read (item 3).
- Exposing effective permissions on `/v1/auth/me` for UI gating.
- A per-user "what can this person reach?" offboarding view — much easier once
  groups exist, but it is its own item.
- SAML. Janus speaks OIDC; all three major IdPs emit groups over OIDC.
- Deny rules. Allow-union with no deny is the engine's best property.
