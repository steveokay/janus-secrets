ALTER TABLE config_versions
    DROP COLUMN IF EXISTS promoted_from_version,
    DROP COLUMN IF EXISTS promoted_from_env_id;
