-- Advisory secret max-age / expiry policy, per config.
--
-- Purely advisory: a max-age NEVER blocks reads, writes, reveals, or anything
-- else. It only lets the UI compute a stale/expired signal from a key's age
-- (now - current value version's created_at). No secret VALUES are stored here
-- — only key names and a duration.
--
-- The config-level DEFAULT max-age is stored under the sentinel key '' (empty
-- string). '' is never a valid secret key (validateKey rejects it), so it can
-- never collide with a real per-key override. Effective max-age for a key is:
-- per-key override if present, else the config default ('') if present, else
-- none (never stale). Mirrors the config_locked_keys per-key metadata pattern:
-- config_id/key primary key, cascade on config delete, FK/column types match.
CREATE TABLE config_secret_max_age (
    config_id       uuid        NOT NULL REFERENCES configs (id) ON DELETE CASCADE,
    key             text        NOT NULL, -- '' = config-level default
    max_age_seconds bigint      NOT NULL CHECK (max_age_seconds > 0),
    created_by      uuid        REFERENCES users (id),
    created_at      timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (config_id, key)
);
