-- Group-based role bindings (RBAC at organisation scale, item 1).
--
-- A binding may name a GROUP instead of a user, so "Team Payments owns these
-- projects" is one row per project rather than one row per person per project,
-- and offboarding happens in the IdP instead of by hunting individual rows.
--
-- A group is one of two kinds, never both:
--
--   oidc  — carries a claim_value; membership is a snapshot refreshed from the
--           OIDC group claim at each login. An admin can NEVER hand-add a member.
--   local — no claim value; membership is an explicit admin-managed list. This
--           is what an instance without an IdP uses, and it covers password
--           logins.
--
-- Keeping them distinct is what lets us state and hold the invariant "access
-- granted via an IdP group is fully described by the IdP" — an access review run
-- against Entra/Okta is therefore COMPLETE for those bindings. A hybrid group
-- (IdP-fed but also hand-editable) would make that review return a clean result
-- that is not true, and would turn an IdP outage into a permanent invisible
-- grant, since a login sync only ever clears rows it owns. Temporary access has
-- a purpose-built path already: break_glass_grants (TTL-clamped, loud, expiring).
--
-- No secret material is stored here. Group names, descriptions and claim values
-- are operator/IdP-supplied identifiers, never secret values.
CREATE TABLE groups (
    id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    -- Display identity. Kept separate from claim_value because Entra emits group
    -- GUIDs by default, and a binding should read "Team Payments", not a GUID.
    name         text NOT NULL,
    kind         text NOT NULL CHECK (kind IN ('oidc','local')),
    -- The exact value matched against the configured group claim. Opaque to us.
    claim_value  text,
    description  text NOT NULL DEFAULT '',
    created_by   uuid REFERENCES users(id),
    created_at   timestamptz NOT NULL DEFAULT now(),
    UNIQUE (name),
    -- Target for group_members' composite FK (see below).
    UNIQUE (id, kind),
    -- A claim value exists exactly when the group is IdP-fed.
    CONSTRAINT groups_claim_shape CHECK ((kind = 'oidc') = (claim_value IS NOT NULL))
);

-- One group per claim value: two groups matching the same claim would make the
-- effective grant depend on row order.
CREATE UNIQUE INDEX groups_claim_value_uniq ON groups (claim_value) WHERE kind = 'oidc';

-- Group membership. The denormalised group_kind + composite FK make "a
-- hand-added member inside an IdP-fed group" UNREPRESENTABLE rather than merely
-- rejected by a handler: the sync path writes 'oidc' rows and the admin path
-- writes 'local' rows, and each fails against a group of the other kind.
CREATE TABLE group_members (
    group_id    uuid NOT NULL,
    group_kind  text NOT NULL,
    user_id     uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    -- Who added them; NULL for rows written by the OIDC login sync.
    created_by  uuid REFERENCES users(id),
    created_at  timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (group_id, user_id),
    FOREIGN KEY (group_id, group_kind) REFERENCES groups(id, kind) ON DELETE CASCADE
);

-- Hot path: resolving one user's groups on every authorization decision.
CREATE INDEX group_members_user_idx ON group_members (user_id);

-- A role binding whose subject is a group. Deliberately a separate table from
-- role_bindings: the direct-binding SQL is the hottest security path in the
-- system and is left untouched, and each table keeps its own pagination cursor.
--
-- role omits 'owner' ON PURPOSE. Owner rotates the master key, prunes the audit
-- chain and hard-destroys secret versions — the destroy-the-evidence tier.
-- Group-deriving it would hand that to whoever administers the IdP, who can add
-- themselves silently and whose membership list Janus cannot authoritatively
-- enumerate (the snapshot only covers users who have logged in). It also means
-- CountInstanceOwners keeps working unchanged: every owner is a direct binding,
-- so an IdP outage can never strand the instance without an owner.
CREATE TABLE group_role_bindings (
    id             uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    group_id       uuid NOT NULL REFERENCES groups(id) ON DELETE CASCADE,
    scope_level    text NOT NULL CHECK (scope_level IN ('instance','project','environment')),
    project_id     uuid REFERENCES projects(id)     ON DELETE CASCADE,
    environment_id uuid REFERENCES environments(id) ON DELETE CASCADE,
    role           text NOT NULL CHECK (role IN ('viewer','developer','admin')),
    created_by     uuid REFERENCES users(id),
    created_at     timestamptz NOT NULL DEFAULT now(),
    -- Scope columns must match scope_level, exactly as role_bindings requires.
    CONSTRAINT group_role_bindings_scope_shape CHECK (
        (scope_level='instance'    AND project_id IS NULL     AND environment_id IS NULL) OR
        (scope_level='project'     AND project_id IS NOT NULL AND environment_id IS NULL) OR
        (scope_level='environment' AND environment_id IS NOT NULL AND project_id IS NULL)
    )
);

CREATE UNIQUE INDEX group_role_bindings_scope_uniq ON group_role_bindings
    (group_id, scope_level,
     COALESCE(project_id,     '00000000-0000-0000-0000-000000000000'),
     COALESCE(environment_id, '00000000-0000-0000-0000-000000000000'));
CREATE INDEX group_role_bindings_group_idx ON group_role_bindings (group_id);

-- Which ID-token claim carries group membership, e.g. 'groups' or a dotted path.
-- Empty disables group sync entirely (deny by default — the operator opts in).
ALTER TABLE oidc_providers ADD COLUMN groups_claim text NOT NULL DEFAULT '';
