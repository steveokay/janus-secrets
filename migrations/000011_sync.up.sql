CREATE TABLE sync_targets (
  id                     uuid PRIMARY KEY,
  project_id             uuid NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
  config_id              uuid NOT NULL REFERENCES configs(id)  ON DELETE CASCADE,
  provider               text NOT NULL CHECK (provider IN ('github','k8s')),
  prune                  bool NOT NULL DEFAULT true,
  interval_seconds       bigint NOT NULL CHECK (interval_seconds > 0),
  next_sync_at           timestamptz NOT NULL,
  status                 text NOT NULL DEFAULT 'active' CHECK (status IN ('active','failed','paused')),
  failure_count          int  NOT NULL DEFAULT 0,
  last_error             text,
  last_synced_at         timestamptz,
  synced_config_version  int,
  creds_ct               bytea NOT NULL,
  creds_nonce            bytea NOT NULL,
  creds_wrapped_dek      bytea NOT NULL,
  creds_dek_kek_version  int   NOT NULL,
  addr                   jsonb NOT NULL,
  managed_keys           text[] NOT NULL DEFAULT '{}',
  synced_fingerprint     bytea,
  created_by             text NOT NULL,
  created_at             timestamptz NOT NULL DEFAULT now(),
  updated_at             timestamptz NOT NULL DEFAULT now()
);

-- One target per (config, provider, destination). addr is jsonb; hash it so the
-- destination participates in the uniqueness constraint.
CREATE UNIQUE INDEX sync_targets_dest ON sync_targets (config_id, provider, md5(addr::text));

-- Scheduler due-scan.
CREATE INDEX sync_targets_due ON sync_targets (next_sync_at) WHERE status = 'active';
