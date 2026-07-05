ALTER TABLE service_tokens DROP CONSTRAINT service_tokens_scope_id_presence;
ALTER TABLE service_tokens DROP CONSTRAINT service_tokens_access_check;
ALTER TABLE service_tokens ADD  CONSTRAINT service_tokens_access_check
  CHECK (access IN ('read', 'readwrite'));
ALTER TABLE service_tokens DROP CONSTRAINT service_tokens_scope_kind_check;
ALTER TABLE service_tokens ADD  CONSTRAINT service_tokens_scope_kind_check
  CHECK (scope_kind IN ('config', 'environment'));
-- Restore the uuid column type (assumes transit tokens are gone — the
-- config/environment-only scope_kind check re-added above already requires it).
ALTER TABLE service_tokens ALTER COLUMN scope_id TYPE uuid USING scope_id::uuid;
ALTER TABLE service_tokens ALTER COLUMN scope_id SET NOT NULL;

DROP TABLE transit_key_versions;
DROP TABLE transit_keys;
