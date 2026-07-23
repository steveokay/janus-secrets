-- Break-glass access (roadmap 1.4): time-boxed emergency role elevation.
--
-- A user who ALREADY holds a role on a scope may self-service elevate to a
-- higher role there for a bounded time, with a mandatory operator-entered
-- reason. The point is a paved, LOUD path — every activation is stamped into
-- the audit chain and forwarded to the notification dispatcher — not shared
-- root credentials.
--
-- The reason is operator-entered text (why the emergency access is needed); it
-- is never a secret value, so it is stored in plaintext and included in audit
-- events. The row carries no secret material at all.
CREATE TABLE break_glass_grants (
    id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    -- The elevated user. FK so a deleted user's grants disappear with them.
    user_id       uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    -- Scope the elevation applies to, mirroring role_bindings' scope modeling.
    scope_level   text NOT NULL CHECK (scope_level IN ('instance','project','environment')),
    project_id     uuid REFERENCES projects(id)     ON DELETE CASCADE,
    environment_id uuid REFERENCES environments(id) ON DELETE CASCADE,
    -- The (higher) role the grant confers while active.
    elevated_role text NOT NULL CHECK (elevated_role IN ('viewer','developer','admin','owner')),
    -- Mandatory operator-entered justification (non-secret).
    reason        text NOT NULL,
    activated_at  timestamptz NOT NULL DEFAULT now(),
    expires_at    timestamptz NOT NULL,
    -- Set when an operator ends the grant early; NULL while it may still apply.
    revoked_at    timestamptz,
    created_at    timestamptz NOT NULL DEFAULT now(),

    -- Scope columns must match the scope_level, exactly like role_bindings.
    CONSTRAINT break_glass_scope_shape CHECK (
        (scope_level = 'instance'    AND project_id IS NULL     AND environment_id IS NULL) OR
        (scope_level = 'project'     AND project_id IS NOT NULL AND environment_id IS NULL) OR
        (scope_level = 'environment' AND environment_id IS NOT NULL AND project_id IS NULL)
    )
);

-- The overlay lookup is "active (non-expired, non-revoked) grants for this
-- user + scope", so index by user for that hot path.
CREATE INDEX break_glass_grants_user_idx ON break_glass_grants (user_id);
-- Listing active grants (admin sees all) scans by liveness; index the expiry.
CREATE INDEX break_glass_grants_expiry_idx ON break_glass_grants (expires_at);
