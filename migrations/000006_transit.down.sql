ALTER TABLE service_tokens DROP CONSTRAINT service_tokens_scope_id_presence;
ALTER TABLE service_tokens DROP CONSTRAINT service_tokens_access_check;
ALTER TABLE service_tokens ADD  CONSTRAINT service_tokens_access_check
  CHECK (access IN ('read', 'readwrite'));
ALTER TABLE service_tokens DROP CONSTRAINT service_tokens_scope_kind_check;
ALTER TABLE service_tokens ADD  CONSTRAINT service_tokens_scope_kind_check
  CHECK (scope_kind IN ('config', 'environment'));
-- Drop any transit tokens first: their scope_id holds a key name (or NULL for
-- all-keys), neither castable to uuid, and the config/environment-only
-- scope_kind check re-added above would reject them anyway.
DELETE FROM service_tokens WHERE scope_kind = 'transit';
-- Restore the uuid column type.
ALTER TABLE service_tokens ALTER COLUMN scope_id TYPE uuid USING scope_id::uuid;
ALTER TABLE service_tokens ALTER COLUMN scope_id SET NOT NULL;

DROP TABLE transit_key_versions;
DROP TABLE transit_keys;
