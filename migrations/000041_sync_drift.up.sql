-- Sync drift detection (roadmap 7.4): read the state of a sync target's external
-- destination back and compare it with what Janus believes it pushed.
--
-- VALUE-FREE BY CONSTRUCTION. Nothing here stores a secret value, nor a digest
-- of one. The comparison itself happens entirely in memory using keyed HMACs
-- (the same master-key-derived sync fingerprint subkey the change-detection path
-- already uses); only key NAMES, counts, booleans, and a sanitized error
-- category are ever persisted.
--
-- Two tables:
--   sync_verify_state — per-target scheduling + last-result summary (1:1 with
--     sync_targets). Deliberately a side table rather than columns on
--     sync_targets so the verify scheduler can be enabled/disabled and paced
--     independently of the push scheduler, and so a target with no row yet is
--     treated as "enabled, due now" via COALESCE defaults.
--   sync_verify_runs — bounded history of verification passes, mirroring the
--     existing sync_runs/rotation_runs pattern (capped per target in Go).

-- ── BUG FIX (found while building drift detection) ───────────────────────────
-- migration 000011 pinned sync_targets.provider to CHECK (provider IN
-- ('github','k8s')) and no later migration widened it, so the six providers
-- added afterwards (gitlab, aws_ssm, cloudflare, aws_secrets, vercel, netlify)
-- could never be persisted: every INSERT failed the check constraint. The
-- engine, API, CLI and UI all offer them, so this is repaired here rather than
-- left for a separate migration number.
ALTER TABLE sync_targets DROP CONSTRAINT IF EXISTS sync_targets_provider_check;
ALTER TABLE sync_targets ADD CONSTRAINT sync_targets_provider_check
    CHECK (provider IN ('github','k8s','gitlab','aws_ssm','cloudflare',
                        'aws_secrets','vercel','netlify'));

CREATE TABLE sync_verify_state (
    target_id        uuid PRIMARY KEY REFERENCES sync_targets(id) ON DELETE CASCADE,
    -- Per-target opt-out. The GLOBAL switch is JANUS_SYNC_VERIFY_TICK (default
    -- 0 = the verify scheduler is off); this flag only matters once that is set.
    enabled          boolean NOT NULL DEFAULT true,
    -- Pace of scheduled verification. Verification reads values back from the
    -- destination, so it is deliberately much slower than the push interval.
    interval_seconds bigint NOT NULL DEFAULT 3600 CHECK (interval_seconds > 0),
    next_verify_at   timestamptz NOT NULL DEFAULT now(),
    last_verify_at   timestamptz,
    -- clean | drift | error | unsupported (see internal/secretsync/verify.go)
    last_status      text CHECK (last_status IN ('clean','drift','error','unsupported')),
    -- Total drifted key count from the last pass (missing + modified + extra).
    -- A COUNT, never a value.
    last_drift_count integer NOT NULL DEFAULT 0,
    created_at       timestamptz NOT NULL DEFAULT now(),
    updated_at       timestamptz NOT NULL DEFAULT now()
);

-- The verify scheduler claims due targets by this predicate.
CREATE INDEX sync_verify_state_due_idx ON sync_verify_state (next_verify_at)
    WHERE enabled;

CREATE TABLE sync_verify_runs (
    id              bigserial PRIMARY KEY,
    target_id       uuid NOT NULL REFERENCES sync_targets(id) ON DELETE CASCADE,
    started_at      timestamptz NOT NULL,
    ended_at        timestamptz NOT NULL,
    status          text NOT NULL CHECK (status IN ('clean','drift','error','unsupported')),
    -- What the provider was able to see: 'values' (remote values readable →
    -- real value comparison), 'names_only' (write-only destination: the API
    -- returns key names but never values), 'none' (no read capability at all).
    capability      text NOT NULL CHECK (capability IN ('values','names_only','none')),
    -- Explicit so an operator can never mistake "no drift" for "values verified".
    values_compared boolean NOT NULL DEFAULT false,
    -- Key NAMES only, capped in Go (see verifyNameCap). The *_count columns are
    -- always exact even when the name arrays are truncated.
    missing_keys    text[] NOT NULL DEFAULT '{}',
    modified_keys   text[] NOT NULL DEFAULT '{}',
    extra_keys      text[] NOT NULL DEFAULT '{}',
    -- Keys present at the destination whose VALUE it would not return (e.g. a
    -- variable an operator flipped to "secret" at the provider). Recorded so a
    -- partial comparison can never be presented as a full one.
    unreadable_keys text[] NOT NULL DEFAULT '{}',
    missing_count   integer NOT NULL DEFAULT 0,
    modified_count  integer NOT NULL DEFAULT 0,
    extra_count     integer NOT NULL DEFAULT 0,
    unreadable_count integer NOT NULL DEFAULT 0,
    -- Number of Janus-managed keys examined this pass.
    checked_count   integer NOT NULL DEFAULT 0,
    -- Sanitized, value-free error category (never a credential, DSN, or value).
    error           text,
    created_at      timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX sync_verify_runs_target_idx ON sync_verify_runs (target_id, id DESC);
