CREATE TABLE promotion_pipeline_steps (
    project_id     uuid    NOT NULL REFERENCES projects (id) ON DELETE CASCADE,
    position       integer NOT NULL,
    environment_id uuid    NOT NULL REFERENCES environments (id) ON DELETE CASCADE,
    PRIMARY KEY (project_id, position),
    UNIQUE (project_id, environment_id)
);

CREATE TABLE config_locked_keys (
    config_id  uuid        NOT NULL REFERENCES configs (id) ON DELETE CASCADE,
    key        text        NOT NULL,
    created_by uuid        REFERENCES users (id),
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (config_id, key)
);
