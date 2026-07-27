ALTER TABLE oidc_providers DROP COLUMN IF EXISTS groups_claim;
DROP TABLE IF EXISTS group_role_bindings;
DROP TABLE IF EXISTS group_members;
DROP TABLE IF EXISTS groups;
