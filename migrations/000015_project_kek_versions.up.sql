CREATE TABLE project_kek_versions (
    project_id  uuid    NOT NULL REFERENCES projects (id) ON DELETE CASCADE,
    version     integer NOT NULL,
    wrapped_kek bytea   NOT NULL,
    created_at  timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (project_id, version)
);
