CREATE TABLE transit_keys (
  id                     uuid PRIMARY KEY,
  name                   text NOT NULL UNIQUE,
  key_type               text NOT NULL CHECK (key_type IN ('aes256-gcm', 'ed25519')),
  latest_version         int  NOT NULL DEFAULT 1,
  min_decryption_version int  NOT NULL DEFAULT 1,
  deletion_allowed       bool NOT NULL DEFAULT false,
  created_at             timestamptz NOT NULL DEFAULT now(),
  updated_at             timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE transit_key_versions (
  id               uuid PRIMARY KEY,
  transit_key_id   uuid NOT NULL REFERENCES transit_keys(id) ON DELETE CASCADE,
  version          int  NOT NULL,
  wrapped_material bytea NOT NULL,
  public_key       bytea,
  created_at       timestamptz NOT NULL DEFAULT now(),
  UNIQUE (transit_key_id, version)
);

-- Extend service_tokens for the transit scope: a transit token may target all
-- keys (scope_id NULL) or one key (scope_id = transit_keys.id).
ALTER TABLE service_tokens ALTER COLUMN scope_id DROP NOT NULL;
ALTER TABLE service_tokens DROP CONSTRAINT service_tokens_scope_kind_check;
ALTER TABLE service_tokens ADD  CONSTRAINT service_tokens_scope_kind_check
  CHECK (scope_kind IN ('config', 'environment', 'transit'));
ALTER TABLE service_tokens DROP CONSTRAINT service_tokens_access_check;
ALTER TABLE service_tokens ADD  CONSTRAINT service_tokens_access_check
  CHECK (access IN ('read', 'readwrite', 'use', 'manage'));
-- Guard: only a transit token may omit scope_id.
ALTER TABLE service_tokens ADD  CONSTRAINT service_tokens_scope_id_presence
  CHECK (scope_id IS NOT NULL OR scope_kind = 'transit');
