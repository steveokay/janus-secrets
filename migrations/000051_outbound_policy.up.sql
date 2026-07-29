-- Make the outbound (SSRF) egress policy editable at runtime.
--
-- Until now the policy was purely process configuration: JANUS_OUTBOUND_BLOCK_PRIVATE
-- and JANUS_OUTBOUND_ALLOW, read once at boot. That is a genuinely strong
-- property — it puts the control OUTSIDE the application, so it constrains what
-- a compromised admin can make the server dial — but it also means correcting a
-- ClusterIP requires an environment change and a restart, which is how operators
-- ended up disabling the control entirely rather than fitting it to the cluster.
--
-- The trade is bounded on purpose:
--
--   * `allow_proxy` is deliberately NOT stored here. It stays env-only, because
--     it is the one setting that truly blinds the guard — through a proxy the
--     destination is resolved by the proxy, so the link-local/metadata block
--     stops applying to the real target. Nothing reachable from the UI may turn
--     that off.
--   * The link-local / cloud-metadata ranges remain unexemptable in ENFORCEMENT
--     (internal/nethard checkIP), not merely in validation, so no row in this
--     table — however it was written, and whatever wrote it — can produce a dial
--     to 169.254.169.254.
--   * JANUS_OUTBOUND_POLICY_LOCKED=true makes the API refuse every write, so a
--     hardened deployment can keep the policy pinned outside the application and
--     retain the original property.
--
-- Env remains the BOOTSTRAP default: with no row here, the server behaves
-- exactly as before. A row supersedes it, which is what makes the setting
-- durable across restarts — and `source` on the API response reports which of
-- the two is in force so the difference is never silent.
CREATE TABLE outbound_policy (
    -- Single-row table. The CHECK is what enforces that: there is one egress
    -- policy per instance, and a second row would make "the policy" ambiguous
    -- with no rule for picking. Same shape as a settings singleton.
    id             boolean     PRIMARY KEY DEFAULT true CHECK (id),

    -- Mirrors JANUS_OUTBOUND_BLOCK_PRIVATE.
    block_private  boolean     NOT NULL DEFAULT false,

    -- Mirrors JANUS_OUTBOUND_ALLOW: IP/CIDR entries exempt from block_private.
    -- Stored as text[] rather than a delimited string so a malformed entry
    -- cannot hide inside a larger value, and normalised to network form by the
    -- application before it ever reaches here (10.96.0.1/24 -> 10.96.0.0/24).
    --
    -- NOT inet/cidr: the application already parses and normalises these with
    -- the exact same code the connect-time guard uses (nethard.ParseAllow), and
    -- letting Postgres accept a value that parser would reject would create a
    -- second, subtly different definition of a valid entry.
    allow          text[]      NOT NULL DEFAULT '{}',

    -- Provenance, so the UI can say who changed egress policy and when. The
    -- audit chain is the authoritative record; these are for display.
    updated_at     timestamptz NOT NULL DEFAULT now(),
    updated_by     uuid        REFERENCES users(id) ON DELETE SET NULL
);

-- No row is inserted. Absence means "no override", which is what preserves the
-- pre-upgrade behaviour: an instance that never visits the screen keeps taking
-- its policy from the environment.
