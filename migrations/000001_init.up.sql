CREATE TABLE seal_config (
    id                 integer PRIMARY KEY DEFAULT 1 CHECK (id = 1),
    type               text NOT NULL,
    threshold          integer,
    shares             integer,
    key_check_value    bytea,
    wrapped_master_key bytea
);

CREATE TABLE projects (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    slug        text NOT NULL,
    name        text NOT NULL DEFAULT '',
    wrapped_kek bytea NOT NULL,
    kek_version integer NOT NULL DEFAULT 1,
    created_at  timestamptz NOT NULL DEFAULT now(),
    updated_at  timestamptz NOT NULL DEFAULT now(),
    deleted_at  timestamptz
);
CREATE UNIQUE INDEX projects_slug_key ON projects (slug) WHERE deleted_at IS NULL;

CREATE TABLE environments (
    id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    project_id uuid NOT NULL REFERENCES projects (id),
    slug       text NOT NULL,
    name       text NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    deleted_at timestamptz
);
CREATE UNIQUE INDEX environments_project_slug_key
    ON environments (project_id, slug) WHERE deleted_at IS NULL;

CREATE TABLE configs (
    id             uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    environment_id uuid NOT NULL REFERENCES environments (id),
    name           text NOT NULL,
    inherits_from  uuid REFERENCES configs (id),
    created_at     timestamptz NOT NULL DEFAULT now(),
    updated_at     timestamptz NOT NULL DEFAULT now(),
    deleted_at     timestamptz
);
CREATE UNIQUE INDEX configs_env_name_key
    ON configs (environment_id, name) WHERE deleted_at IS NULL;

CREATE TABLE config_versions (
    id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    config_id  uuid NOT NULL REFERENCES configs (id),
    version    integer NOT NULL,
    message    text NOT NULL DEFAULT '',
    created_by text,
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (config_id, version)
);

CREATE TABLE secret_values (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    config_id       uuid NOT NULL REFERENCES configs (id),
    key             text NOT NULL,
    value_version   integer NOT NULL,
    wrapped_dek     bytea NOT NULL,
    ciphertext      bytea NOT NULL,
    nonce           bytea NOT NULL,
    dek_key_version integer NOT NULL DEFAULT 1,
    created_at      timestamptz NOT NULL DEFAULT now(),
    UNIQUE (config_id, key, value_version)
);

CREATE TABLE config_version_entries (
    config_version_id uuid NOT NULL REFERENCES config_versions (id),
    key               text NOT NULL,
    secret_value_id   uuid REFERENCES secret_values (id),
    tombstone         boolean NOT NULL DEFAULT false,
    PRIMARY KEY (config_version_id, key),
    CHECK (tombstone = (secret_value_id IS NULL))
);
