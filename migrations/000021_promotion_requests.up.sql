CREATE TABLE promotion_requests (
    id                     uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    project_id             uuid NOT NULL REFERENCES projects (id),
    source_config_id       uuid NOT NULL REFERENCES configs (id),
    source_version         integer NOT NULL,
    target_config_id       uuid REFERENCES configs (id),
    target_env_id          uuid NOT NULL REFERENCES environments (id),
    target_name            text NOT NULL DEFAULT '',
    create_target          boolean NOT NULL DEFAULT false,
    selections             jsonb NOT NULL DEFAULT '[]'::jsonb,
    note                   text NOT NULL DEFAULT '',
    status                 text NOT NULL DEFAULT 'pending',
    requested_by           uuid NOT NULL REFERENCES users (id),
    decided_by             uuid REFERENCES users (id),
    decision_note          text NOT NULL DEFAULT '',
    applied_target_version integer,
    created_at             timestamptz NOT NULL DEFAULT now(),
    decided_at             timestamptz
);

CREATE INDEX idx_promotion_requests_project_status ON promotion_requests (project_id, status, created_at DESC, id DESC);
CREATE INDEX idx_promotion_requests_target_status ON promotion_requests (target_env_id, status);
CREATE INDEX idx_promotion_requests_requester ON promotion_requests (requested_by, status);
