-- Secret value-version retention (roadmap 8.2).
--
-- Every save writes an immutable secret_values row (its own DEK + ciphertext);
-- nothing has ever removed one, so a long-lived instance keeps every value a
-- key has ever held, forever. This migration adds the persistence an explicit,
-- audited pruning policy needs.
--
-- PRUNING GRANULARITY — why config VERSIONS, not value versions.
--
-- Migration 000005 set
--     config_version_entries.secret_value_id -> secret_values (id) ON DELETE CASCADE
-- so deleting a secret_values row SILENTLY deletes the manifest entries that
-- reference it: old config versions would quietly lose keys and a rollback to
-- one of them would restore an incomplete config, with no error anywhere. A
-- naive "delete value versions older than N days" therefore corrupts history.
--
-- Pruning is instead done at the CONFIG-VERSION granularity (the unit of diff
-- and rollback): whole config_versions rows are deleted (their manifest entries
-- go with them by the config_version_id cascade), and only then are
-- secret_values rows that are no longer referenced by ANY surviving
-- config_version_entries garbage-collected. That preserves the invariant
--
--     every RETAINED config version is fully restorable
--
-- which internal/store.SecretRepo.PruneConfigVersions re-asserts inside the
-- prune transaction (a live-entry count taken before and after the deletes must
-- match) and which the integration tests assert directly.

-- Per-config retention override. Value-free: config ids and integers only.
--
-- Semantics: this can only ever RETAIN MORE than the instance-wide floor
-- (JANUS_SECRET_RETAIN_MIN_VERSIONS / JANUS_SECRET_RETAIN_MIN_DAYS). The
-- effective floor for a config is the strictest (largest) of the instance floor,
-- this override, and the prune request's own keep_* parameters, so a per-config
-- row can never weaken an operator's instance-wide guarantee. Mirrors the
-- config_secret_max_age per-config policy pattern (migration 000028): config_id
-- FK with ON DELETE CASCADE, created_by referencing users, value-free columns.
--
-- NULL means "no opinion from this config" (inherit the instance floor); a row
-- must state at least one of the two, otherwise it carries no information.
CREATE TABLE config_version_retention (
    config_id    uuid PRIMARY KEY REFERENCES configs (id) ON DELETE CASCADE,
    -- Never prune below the newest N config versions of this config.
    min_versions integer,
    -- Never prune a config version younger than N days.
    min_days     integer,
    created_by   uuid        REFERENCES users (id),
    created_at   timestamptz NOT NULL DEFAULT now(),
    updated_at   timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT config_version_retention_min_versions_positive
        CHECK (min_versions IS NULL OR min_versions >= 1),
    CONSTRAINT config_version_retention_min_days_positive
        CHECK (min_days IS NULL OR min_days >= 1),
    CONSTRAINT config_version_retention_not_empty
        CHECK (min_versions IS NOT NULL OR min_days IS NOT NULL)
);

-- The garbage-collection step asks, for every secret_values row of one config,
-- "is this row still referenced by any config_version_entries row?". Without an
-- index on the referencing column that is a sequential scan of the whole
-- manifest table (its primary key is (config_version_id, key)). Partial on
-- NOT NULL because tombstone entries carry a NULL secret_value_id and are never
-- probed by the GC.
CREATE INDEX config_version_entries_secret_value_id_idx
    ON config_version_entries (secret_value_id)
    WHERE secret_value_id IS NOT NULL;
