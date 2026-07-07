-- Speeds the trailing-24h scan on action='secret.reveal' for usage metrics.
CREATE INDEX IF NOT EXISTS audit_events_action_time_idx
  ON audit_events (action, occurred_at);
