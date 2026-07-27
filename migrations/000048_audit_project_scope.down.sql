DROP INDEX IF EXISTS audit_events_project_seq_idx;
ALTER TABLE audit_events DROP COLUMN IF EXISTS project_id;
