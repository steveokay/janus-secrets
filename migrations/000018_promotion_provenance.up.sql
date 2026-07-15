ALTER TABLE config_versions
    ADD COLUMN promoted_from_env_id  uuid REFERENCES environments (id),
    ADD COLUMN promoted_from_version integer;
