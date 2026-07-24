DROP TABLE IF EXISTS config_edit_requests;

ALTER TABLE configs
    DROP COLUMN IF EXISTS require_approval;
