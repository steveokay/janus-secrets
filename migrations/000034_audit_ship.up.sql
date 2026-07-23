-- Audit shipping to a SIEM: a shipper tails the (value-free) audit log and
-- streams each event as one JSON object per line (JSONL / NDJSON) to an
-- external destination (webhook or syslog) for ingestion.
--
-- Only the durable HIGH-WATER MARK lives in Postgres — the destination itself
-- (mode/url/addr/hmac key) is configured from the environment (JANUS_AUDIT_SHIP_*)
-- so no destination secret is ever persisted here. The mark is the highest audit
-- seq already SHIPPED; it advances only after a successful send, giving
-- at-least-once semantics with no gaps (a failed send retries from the same mark).
--
-- Single-row table (id = true), seeded to the current audit head so enabling the
-- shipper never replays history from the beginning of the log.
CREATE TABLE audit_ship_state (
    id         bool PRIMARY KEY DEFAULT true CHECK (id),
    last_seq   bigint NOT NULL DEFAULT 0,
    updated_at timestamptz NOT NULL DEFAULT now()
);
INSERT INTO audit_ship_state (id, last_seq)
    SELECT true, COALESCE(MAX(seq), 0) FROM audit_events;
