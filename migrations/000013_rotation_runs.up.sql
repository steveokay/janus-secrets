CREATE TABLE rotation_runs (
  id              BIGSERIAL PRIMARY KEY,
  policy_id       UUID NOT NULL REFERENCES rotation_policies(id) ON DELETE CASCADE,
  started_at      TIMESTAMPTZ NOT NULL,
  ended_at        TIMESTAMPTZ NOT NULL,
  status          TEXT NOT NULL CHECK (status IN ('success','failure')),
  error           TEXT,
  config_version  INTEGER,
  attempt_num     INTEGER NOT NULL,
  created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_rotation_runs_policy ON rotation_runs (policy_id, id DESC);
