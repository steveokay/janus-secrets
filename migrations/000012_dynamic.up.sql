CREATE TABLE dynamic_roles (
  id                     uuid PRIMARY KEY,
  project_id             uuid   NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
  config_id              uuid   NOT NULL REFERENCES configs(id)  ON DELETE CASCADE,
  name                   text   NOT NULL,
  default_ttl_seconds    bigint NOT NULL CHECK (default_ttl_seconds > 0),
  max_ttl_seconds        bigint NOT NULL CHECK (max_ttl_seconds >= default_ttl_seconds),
  config_ct              bytea  NOT NULL,
  config_nonce           bytea  NOT NULL,
  config_wrapped_dek     bytea  NOT NULL,
  config_dek_kek_version int    NOT NULL,
  created_by             text   NOT NULL,
  created_at             timestamptz NOT NULL DEFAULT now(),
  updated_at             timestamptz NOT NULL DEFAULT now(),
  UNIQUE (config_id, name)
);

CREATE TABLE dynamic_leases (
  id             uuid PRIMARY KEY,
  role_id        uuid NOT NULL REFERENCES dynamic_roles(id) ON DELETE CASCADE,
  project_id     uuid NOT NULL REFERENCES projects(id)      ON DELETE CASCADE,
  db_username    text NOT NULL,
  status         text NOT NULL DEFAULT 'creating'
                   CHECK (status IN ('creating','active','revoked','expired','revoke_failed')),
  issued_at      timestamptz NOT NULL DEFAULT now(),
  expires_at     timestamptz NOT NULL,
  max_expires_at timestamptz NOT NULL,
  renewed_at     timestamptz,
  revoked_at     timestamptz,
  last_error     text,
  created_by     text NOT NULL,
  created_at     timestamptz NOT NULL DEFAULT now(),
  updated_at     timestamptz NOT NULL DEFAULT now()
);

-- Lease-manager due-scan: expiring active leases + revoke retries.
CREATE INDEX dynamic_leases_active_due ON dynamic_leases (expires_at) WHERE status = 'active';
-- Reclaim of crash-orphaned in-flight issues + revoke retries.
CREATE INDEX dynamic_leases_creating ON dynamic_leases (created_at) WHERE status = 'creating';
CREATE INDEX dynamic_leases_revoke_failed ON dynamic_leases (id) WHERE status = 'revoke_failed';
