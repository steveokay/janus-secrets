CREATE TABLE sync_runs (
  id              BIGSERIAL PRIMARY KEY,
  target_id       UUID NOT NULL REFERENCES sync_targets(id) ON DELETE CASCADE,
  started_at      TIMESTAMPTZ NOT NULL,
  ended_at        TIMESTAMPTZ NOT NULL,
  status          TEXT NOT NULL CHECK (status IN ('success','failure')),
  error           TEXT,
  config_version  INTEGER,
  keys_count      INTEGER NOT NULL DEFAULT 0,
  attempt_num     INTEGER NOT NULL,
  created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_sync_runs_target ON sync_runs (target_id, id DESC);
