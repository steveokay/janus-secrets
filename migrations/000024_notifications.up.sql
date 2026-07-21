-- Notifications: outbound alerting so failures/pending events find humans.
-- A dispatcher tails the (value-free) audit log and fans matching events out to
-- configured channels, so no notification can ever carry a secret value.

-- A channel is a destination (generic webhook or Slack incoming webhook) plus
-- the set of event kinds it subscribes to. The destination URL (+ optional
-- HMAC signing key) is a bearer secret, so the config blob is envelope-encrypted
-- under the MASTER key (instance-scoped, like the OIDC client secret) — it is
-- re-wrapped by master-key rotation.
CREATE TABLE notification_channels (
    id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    name       text NOT NULL UNIQUE,
    type       text NOT NULL CHECK (type IN ('webhook','slack')),
    enabled    bool NOT NULL DEFAULT true,
    -- Subscribed event kinds, e.g. {rotation.failed, sync.failed, promotion.pending, access.denied}.
    events     text[] NOT NULL DEFAULT '{}',
    -- Master-wrapped {"url":"...","hmac_key":"..."} (hmac_key webhook-only, optional).
    config_ct  bytea NOT NULL,
    created_by text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

-- The delivery outbox: one row per (matched audit event × subscribing channel).
-- Persisted before delivery so a crash or a channel outage never loses an alert;
-- the dispatcher retries pending rows with exponential backoff until delivered
-- or exhausted. Payload is a value-free rendering of the source audit event.
CREATE TABLE notification_deliveries (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    channel_id      uuid NOT NULL REFERENCES notification_channels(id) ON DELETE CASCADE,
    audit_seq       bigint NOT NULL,
    event_kind      text NOT NULL,
    payload         jsonb NOT NULL,
    status          text NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','delivered','failed')),
    attempts        int NOT NULL DEFAULT 0,
    next_attempt_at timestamptz NOT NULL DEFAULT now(),
    last_error      text,
    created_at      timestamptz NOT NULL DEFAULT now(),
    delivered_at    timestamptz,
    -- Idempotent fan-out: an event is enqueued at most once per channel even if
    -- the dispatcher re-processes a batch after a crash.
    UNIQUE (channel_id, audit_seq)
);
CREATE INDEX notification_deliveries_due
    ON notification_deliveries (next_attempt_at) WHERE status = 'pending';
CREATE INDEX notification_deliveries_channel
    ON notification_deliveries (channel_id, created_at DESC);

-- Single-row fan-out cursor: the highest audit seq already scanned. Seeded to
-- the current audit head so enabling notifications never replays history.
CREATE TABLE notification_cursor (
    id       bool PRIMARY KEY DEFAULT true CHECK (id),
    last_seq bigint NOT NULL DEFAULT 0
);
INSERT INTO notification_cursor (id, last_seq)
    SELECT true, COALESCE(MAX(seq), 0) FROM audit_events;
