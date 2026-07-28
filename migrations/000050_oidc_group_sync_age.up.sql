-- Bound how long an OIDC group snapshot may be trusted.
--
-- Group membership from an identity provider is a snapshot refreshed at each
-- login (migration 000045). Normally that self-corrects: an active user
-- re-syncs constantly, and an absent one loses access when their session
-- expires. But there is one case where the snapshot is retained and NEVER
-- refreshed — Entra stops emitting the `groups` claim once a user exceeds ~200
-- groups, sending a `_claim_names` Graph pointer instead. Janus correctly
-- treats that as "membership unknown" and keeps the last good snapshot rather
-- than clearing it (clearing would look exactly like a legitimate removal from
-- every group), but the retained snapshot then had no time bound at all. That
-- was the one place this design kept stale membership indefinitely.
--
-- The fix is a maximum snapshot age, deliberately provider-agnostic: past it,
-- OIDC-derived group bindings stop applying for that user. No Microsoft Graph
-- fetch, which would need new credentials, add an outbound call to the login
-- path, and be Entra-specific in a deliberately generic OIDC implementation.
--
-- Only AUTHORITATIVE syncs stamp this column. An overage login must NOT refresh
-- it, or the bound would never trigger for the exact case it exists for.
--
-- LOCAL group membership is unaffected and can never expire: it is
-- admin-managed and has no freshness concept. Expiring it would break every
-- instance that has no identity provider at all.
ALTER TABLE users
    ADD COLUMN oidc_groups_synced_at timestamptz;

-- Backfill anyone who currently holds OIDC-derived membership, so enabling the
-- bound after an upgrade does not retroactively revoke access from users whose
-- snapshot predates this column. They get one full window to log in again.
UPDATE users u
   SET oidc_groups_synced_at = now()
 WHERE EXISTS (
        SELECT 1 FROM group_members m
         WHERE m.user_id = u.id AND m.group_kind = 'oidc'
       );

-- The staleness check runs on the authorization hot path, keyed by user.
CREATE INDEX users_oidc_groups_synced_idx ON users (oidc_groups_synced_at)
    WHERE oidc_groups_synced_at IS NOT NULL;
