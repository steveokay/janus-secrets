-- Run history for the scheduled S3 backup engine. Value-free: no key material,
-- no ciphertext, no credentials. `object_key` is the S3 object path only;
-- `error` is a sanitized category string. Modeled on rotation_runs/sync_runs.
CREATE TABLE backup_runs (
  id           BIGSERIAL PRIMARY KEY,
  started_at   TIMESTAMPTZ NOT NULL,
  finished_at  TIMESTAMPTZ NOT NULL,
  status       TEXT NOT NULL CHECK (status IN ('success','failure')),
  object_key   TEXT,
  size_bytes   BIGINT,
  error        TEXT,
  created_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_backup_runs_id ON backup_runs (id DESC);
