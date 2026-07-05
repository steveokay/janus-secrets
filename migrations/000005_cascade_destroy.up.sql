ALTER TABLE environments DROP CONSTRAINT environments_project_id_fkey,
    ADD CONSTRAINT environments_project_id_fkey
        FOREIGN KEY (project_id) REFERENCES projects (id) ON DELETE CASCADE;

ALTER TABLE configs DROP CONSTRAINT configs_environment_id_fkey,
    ADD CONSTRAINT configs_environment_id_fkey
        FOREIGN KEY (environment_id) REFERENCES environments (id) ON DELETE CASCADE;

ALTER TABLE config_versions DROP CONSTRAINT config_versions_config_id_fkey,
    ADD CONSTRAINT config_versions_config_id_fkey
        FOREIGN KEY (config_id) REFERENCES configs (id) ON DELETE CASCADE;

ALTER TABLE secret_values DROP CONSTRAINT secret_values_config_id_fkey,
    ADD CONSTRAINT secret_values_config_id_fkey
        FOREIGN KEY (config_id) REFERENCES configs (id) ON DELETE CASCADE;

ALTER TABLE config_version_entries DROP CONSTRAINT config_version_entries_config_version_id_fkey,
    ADD CONSTRAINT config_version_entries_config_version_id_fkey
        FOREIGN KEY (config_version_id) REFERENCES config_versions (id) ON DELETE CASCADE;

ALTER TABLE config_version_entries DROP CONSTRAINT config_version_entries_secret_value_id_fkey,
    ADD CONSTRAINT config_version_entries_secret_value_id_fkey
        FOREIGN KEY (secret_value_id) REFERENCES secret_values (id) ON DELETE CASCADE;
