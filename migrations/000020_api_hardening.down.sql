DROP INDEX IF EXISTS idx_transit_keys_page;
DROP INDEX IF EXISTS idx_role_bindings_page;
DROP INDEX IF EXISTS idx_users_page;
DROP INDEX IF EXISTS idx_service_tokens_page;
DROP INDEX IF EXISTS idx_configs_page;
DROP INDEX IF EXISTS idx_environments_page;
DROP INDEX IF EXISTS idx_projects_page;

DROP TABLE IF EXISTS idempotency;

-- Recreate the promotion idempotency table (original 000017 shape).
CREATE TABLE promotion_idempotency (
    idempotency_key text        NOT NULL,
    actor           text        NOT NULL,
    request_hash    text        NOT NULL,
    response        jsonb,
    created_at      timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (idempotency_key, actor)
);
