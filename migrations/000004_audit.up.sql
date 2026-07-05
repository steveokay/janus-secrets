CREATE TABLE audit_events (
    seq          bigint PRIMARY KEY,
    occurred_at  timestamptz NOT NULL,
    actor_kind   text NOT NULL,
    actor_id     text,
    actor_name   text NOT NULL,
    action       text NOT NULL,
    resource     text NOT NULL,
    detail       text,
    result       text NOT NULL,
    result_code  text,
    ip           text NOT NULL,
    prev_hash    bytea NOT NULL,
    hash         bytea NOT NULL
);

CREATE INDEX audit_events_occurred_at_idx ON audit_events (occurred_at);
CREATE INDEX audit_events_action_idx      ON audit_events (action);
CREATE INDEX audit_events_result_idx      ON audit_events (result);
