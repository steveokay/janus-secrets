DROP INDEX IF EXISTS oidc_federation_bindings_issuer_idx;
ALTER TABLE oidc_federation_bindings DROP CONSTRAINT IF EXISTS oidc_federation_bindings_issuer_check;
ALTER TABLE oidc_federation_bindings DROP COLUMN IF EXISTS issuer;

DROP INDEX IF EXISTS oidc_federation_config_issuer_key;
ALTER TABLE oidc_federation_config DROP CONSTRAINT IF EXISTS oidc_federation_config_preset_check;
ALTER TABLE oidc_federation_config DROP COLUMN IF EXISTS preset;
