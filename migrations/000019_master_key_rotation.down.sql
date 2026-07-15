ALTER TABLE seal_config
    DROP COLUMN IF EXISTS master_key_rotated_at,
    DROP COLUMN IF EXISTS master_key_version;
