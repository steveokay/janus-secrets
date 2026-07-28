-- Irreversible by nature: per-key owners were folded into notes or promoted
-- to the project, so the original column cannot be reconstructed. Restoring the
-- column gives back the shape, not the data.
ALTER TABLE config_secret_annotations DROP CONSTRAINT IF EXISTS config_secret_annotations_note_present;
ALTER TABLE config_secret_annotations ADD COLUMN IF NOT EXISTS owner text CHECK (owner IS NULL OR char_length(owner) <= 256);
ALTER TABLE projects DROP COLUMN IF EXISTS owner;
