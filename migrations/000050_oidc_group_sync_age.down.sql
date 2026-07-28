DROP INDEX IF EXISTS users_oidc_groups_synced_idx;
ALTER TABLE users DROP COLUMN IF EXISTS oidc_groups_synced_at;
