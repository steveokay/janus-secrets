-- TOTP second factor for password logins.

-- One row per user who has enrolled a TOTP authenticator. wrapped_secret is the
-- master-key-wrapped shared secret (re-wrapped by master-key rotation).
-- activated_at NULL = enrolled but not yet confirmed (no login gate yet); set on
-- confirmation. Deleting the row disables 2FA for that user.
CREATE TABLE user_totp (
    user_id        uuid PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    wrapped_secret bytea NOT NULL,
    activated_at   timestamptz,
    created_at     timestamptz NOT NULL DEFAULT now()
);

-- Single-use recovery codes (HMAC-SHA256, never stored in the clear). used_at
-- marks a spent code; codes are regenerated as a set.
CREATE TABLE user_recovery_codes (
    id        uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id   uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    code_hmac bytea NOT NULL,
    used_at   timestamptz,
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX user_recovery_codes_user ON user_recovery_codes (user_id) WHERE used_at IS NULL;
