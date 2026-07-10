CREATE TABLE rotation_policies (
  id                     uuid PRIMARY KEY,
  project_id             uuid NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
  config_id              uuid NOT NULL REFERENCES configs(id)  ON DELETE CASCADE,
  secret_key             text NOT NULL,
  type                   text NOT NULL CHECK (type IN ('postgres','webhook')),
  interval_seconds       bigint NOT NULL CHECK (interval_seconds > 0),
  next_rotation_at       timestamptz NOT NULL,
  status                 text NOT NULL DEFAULT 'active' CHECK (status IN ('active','failed','paused')),
  failure_count          int  NOT NULL DEFAULT 0,
  last_error             text,
  last_rotated_at        timestamptz,
  last_config_version    int,
  config_ct              bytea NOT NULL,
  config_nonce           bytea NOT NULL,
  config_wrapped_dek     bytea NOT NULL,
  config_dek_kek_version int   NOT NULL,
  pending_ct             bytea,
  pending_nonce          bytea,
  pending_wrapped_dek    bytea,
  pending_state          text CHECK (pending_state IN ('applying')),
  created_by             text NOT NULL,
  created_at             timestamptz NOT NULL DEFAULT now(),
  updated_at             timestamptz NOT NULL DEFAULT now(),
  UNIQUE (config_id, secret_key)
);

-- Scheduler due-scan: partial index over active policies by due time.
CREATE INDEX rotation_policies_due ON rotation_policies (next_rotation_at)
  WHERE status = 'active';
