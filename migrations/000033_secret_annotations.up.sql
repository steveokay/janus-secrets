-- Per-key secret annotations: owner + free-text note metadata, per config.
--
-- Value-free: this table stores ONLY human-facing metadata (an owner label and
-- a free-text note) so "what is this secret and who do I ask about it" is
-- answerable from the masked view. It NEVER stores secret VALUES, and nothing
-- here blocks any read, write, or reveal.
--
-- Mirrors the config_secret_max_age / config_locked_keys per-key metadata
-- pattern: (config_id, key) primary key, cascade on config delete, FK/column
-- types match. owner/note are bounded by CHECK to keep the metadata small.
CREATE TABLE config_secret_annotations (
    config_id  uuid        NOT NULL REFERENCES configs (id) ON DELETE CASCADE,
    key        text        NOT NULL,
    owner      text        CHECK (owner IS NULL OR char_length(owner) <= 256),
    note       text        CHECK (note IS NULL OR char_length(note) <= 2048),
    updated_by uuid        REFERENCES users (id),
    updated_at timestamptz NOT NULL DEFAULT now(),
    -- At least one of owner/note must be present; a row with neither is a clear.
    CHECK (owner IS NOT NULL OR note IS NOT NULL),
    PRIMARY KEY (config_id, key)
);
