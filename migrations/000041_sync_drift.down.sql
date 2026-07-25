DROP TABLE IF EXISTS sync_verify_runs;
DROP TABLE IF EXISTS sync_verify_state;

-- Restore the (over-narrow) 000011 provider constraint. NOT VALID so rows using
-- one of the six later providers do not block the rollback.
ALTER TABLE sync_targets DROP CONSTRAINT IF EXISTS sync_targets_provider_check;
ALTER TABLE sync_targets ADD CONSTRAINT sync_targets_provider_check
    CHECK (provider IN ('github','k8s')) NOT VALID;
