-- Master-key rotation observability. seal_config is the single-row (id=1)
-- seal metadata table. Existing instances default to version 1 (never rotated).
ALTER TABLE seal_config
    ADD COLUMN master_key_version    integer     NOT NULL DEFAULT 1,
    ADD COLUMN master_key_rotated_at timestamptz;
