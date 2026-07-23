-- Speeds the per-key "last read" scan used by advisory unused-secret detection.
--
-- Unused-secret detection computes, per secret key, MAX(occurred_at) over
-- audit_events WHERE action='secret.reveal' AND resource LIKE
-- 'configs/{cid}/secrets/%' GROUP BY resource. A PARTIAL index keyed on
-- (resource, occurred_at) restricted to the reveal action serves this exactly:
-- the WHERE action='secret.reveal' predicate is baked into the index (so it
-- stays small — reveals are a fraction of all audit rows), the leading
-- `resource` column supports the LIKE-prefix range scan and the GROUP BY, and
-- the trailing `occurred_at` lets the MAX be answered from the index. Chosen
-- over a plain (action, resource, occurred_at) index because the partial form
-- indexes only the rows this query ever touches, keeping it compact and its
-- maintenance cost off the (far more numerous) non-reveal audit writes.
CREATE INDEX IF NOT EXISTS audit_events_reveal_resource_idx
  ON audit_events (resource, occurred_at)
  WHERE action = 'secret.reveal';
