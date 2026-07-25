-- Audit hash-chain checkpointing (roadmap 8.1): a signed high-water anchor over
-- the append-only, SHA-256 hash-chained audit log.
--
-- Because GET /v1/audit/verify walks the chain from genesis, the audit_events
-- table can never be pruned without breaking verification. A checkpoint records
-- the chain head at a point in time (its seq, its chained hash, and the total
-- event count up to and including that seq) together with an HMAC-SHA256 tag
-- over those fields. The MAC key is derived (domain-separated) from the
-- master-key-wrapped token-HMAC key that only exists in memory post-unseal, so a
-- checkpoint row cannot be forged from database access alone.
--
-- With a valid checkpoint, verify can (a) confirm the anchor's MAC, then (b)
-- walk the chain forward from the anchor instead of from genesis — which lets a
-- verified prefix (seq <= through_seq) be safely pruned (POST /v1/audit/prune).
--
-- No secret VALUE is ever stored here: through_hash is a chain hash, and the MAC
-- is over metadata + counts only.
CREATE TABLE audit_checkpoints (
    id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    -- The audit seq this checkpoint anchors (the chain head at capture time).
    -- UNIQUE so a given seq is anchored at most once.
    through_seq  bigint NOT NULL UNIQUE,
    -- The chained SHA-256 hash of the event at through_seq (bytea, matching the
    -- audit_events.hash column encoding).
    through_hash bytea NOT NULL,
    -- Total number of events in the chain up to and including through_seq. With a
    -- contiguous genesis-anchored chain this equals through_seq, but it is stored
    -- (and MAC'd) explicitly so a future non-1-based chain stays self-describing.
    event_count  bigint NOT NULL,
    created_at   timestamptz NOT NULL DEFAULT now(),
    -- HMAC-SHA256(ckKey, lengthPrefixed(through_seq || through_hash || event_count)).
    mac          bytea NOT NULL
);

-- Fetching the latest checkpoint (verify + prune) is a hot path.
CREATE INDEX audit_checkpoints_through_seq_idx ON audit_checkpoints (through_seq DESC);
