-- Generic idempotency: one row per (Idempotency-Key, actor). status_code 0 =
-- claimed-but-pending. Bodies are NEVER stored — only the status code — so no
-- once-shown secret can persist here.
CREATE TABLE idempotency (
    idempotency_key text        NOT NULL,
    actor           text        NOT NULL,
    endpoint        text        NOT NULL,
    request_hash    text        NOT NULL,
    status_code     integer     NOT NULL DEFAULT 0,
    created_at      timestamptz NOT NULL DEFAULT now(),
    completed_at    timestamptz,
    PRIMARY KEY (idempotency_key, actor)
);

-- The bespoke promotion idempotency table is superseded by the generic one.
DROP TABLE IF EXISTS promotion_idempotency;

-- Keyset pagination covering indexes (created_at DESC, id DESC); partial on the
-- soft-deleting tables to match the WHERE deleted_at IS NULL scan.
CREATE INDEX idx_projects_page       ON projects       (created_at DESC, id DESC) WHERE deleted_at IS NULL;
CREATE INDEX idx_environments_page   ON environments   (created_at DESC, id DESC) WHERE deleted_at IS NULL;
CREATE INDEX idx_configs_page        ON configs        (created_at DESC, id DESC) WHERE deleted_at IS NULL;
CREATE INDEX idx_service_tokens_page ON service_tokens (created_at DESC, id DESC);
CREATE INDEX idx_users_page          ON users          (created_at DESC, id DESC);
CREATE INDEX idx_role_bindings_page  ON role_bindings  (created_at DESC, id DESC);
CREATE INDEX idx_transit_keys_page   ON transit_keys   (created_at DESC, id DESC);
