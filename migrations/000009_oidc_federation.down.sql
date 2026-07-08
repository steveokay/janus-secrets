ALTER TABLE service_tokens DROP CONSTRAINT IF EXISTS service_tokens_minter_presence;
ALTER TABLE service_tokens DROP COLUMN IF EXISTS federation_binding;
-- Restore NOT NULL only if no NULL rows remain (federated tokens must be gone).
ALTER TABLE service_tokens ALTER COLUMN created_by SET NOT NULL;
DROP TABLE IF EXISTS oidc_federation_bindings;
DROP TABLE IF EXISTS oidc_federation_config;
