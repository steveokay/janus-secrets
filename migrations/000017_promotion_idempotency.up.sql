CREATE TABLE promotion_idempotency (
    idempotency_key text        NOT NULL,
    actor           text        NOT NULL,
    request_hash    text        NOT NULL,
    response        jsonb,
    created_at      timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (idempotency_key, actor)
);
