-- Dropping the override returns the instance to env-only egress policy. That is
-- a WIDENING for any instance that had used the screen to add an allowlist, and
-- a TIGHTENING for one that had used it to relax the environment's setting —
-- either way the environment is authoritative again after this runs.
DROP TABLE IF EXISTS outbound_policy;
