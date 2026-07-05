CREATE TABLE role_bindings (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    subject_user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    scope_level     text NOT NULL CHECK (scope_level IN ('instance','project','environment')),
    project_id      uuid REFERENCES projects(id) ON DELETE CASCADE,
    environment_id  uuid REFERENCES environments(id) ON DELETE CASCADE,
    role            text NOT NULL CHECK (role IN ('viewer','developer','admin','owner')),
    created_by      uuid REFERENCES users(id),
    created_at      timestamptz NOT NULL DEFAULT now(),
    CHECK ( (scope_level='instance'    AND project_id IS NULL     AND environment_id IS NULL)
         OR (scope_level='project'     AND project_id IS NOT NULL AND environment_id IS NULL)
         OR (scope_level='environment' AND environment_id IS NOT NULL AND project_id IS NULL) )
);

CREATE UNIQUE INDEX role_bindings_scope_uniq ON role_bindings
    (subject_user_id, scope_level,
     COALESCE(project_id,     '00000000-0000-0000-0000-000000000000'),
     COALESCE(environment_id, '00000000-0000-0000-0000-000000000000'));
CREATE INDEX role_bindings_subject_idx ON role_bindings (subject_user_id);
