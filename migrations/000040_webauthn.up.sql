-- WebAuthn / passkeys for UI login.

-- One row per registered passkey. Everything stored here is PUBLIC credential
-- material (credential id + COSE public key + attestation metadata) — there is
-- no secret-equivalent value, so no master-key wrapping is involved and a
-- master-key rotation has nothing to re-wrap.
--
-- credential_id is the raw authenticator credential id and is globally unique
-- (an authenticator must never present the same id for two accounts).
-- sign_count is the authenticator's signature counter; it is advanced with a
-- strictly-increasing compare-and-swap on every assertion so a cloned or
-- replayed authenticator is detected (WebAuthn L3 §6.1.3 step 21).
-- rp_id records the Relying Party ID the credential was registered under: a
-- credential is only usable under the RP ID that created it, so changing
-- JANUS_WEBAUTHN_RP_ID must not silently resurrect old credentials.
CREATE TABLE webauthn_credentials (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id         uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    rp_id           text NOT NULL,
    credential_id   bytea NOT NULL UNIQUE,
    -- The go-webauthn Credential Record, JSON-encoded (public key, transports,
    -- flags, attestation). The store stays format-blind.
    credential      jsonb NOT NULL,
    sign_count      bigint NOT NULL DEFAULT 0,
    nickname        text NOT NULL,
    created_at      timestamptz NOT NULL DEFAULT now(),
    last_used_at    timestamptz
);
CREATE INDEX webauthn_credentials_user ON webauthn_credentials (user_id);
CREATE UNIQUE INDEX webauthn_credentials_user_nickname
    ON webauthn_credentials (user_id, lower(nickname));

-- Pending ceremony challenges. A challenge is a public random value, but it MUST
-- be single-use, expiring, and bound to the account it was issued for — so it is
-- claimed with a DELETE ... RETURNING (atomic, no replay) and filtered on
-- expires_at.
--
-- user_id is NULL for a login challenge issued against an unknown email: the
-- begin endpoint answers identically for known and unknown accounts (no
-- enumeration oracle), so the row exists but can never be finished.
-- session_data is the go-webauthn SessionData (challenge, RP ID, allowed
-- credential ids, user-verification requirement) — public ceremony parameters,
-- never credential material.
CREATE TABLE webauthn_challenges (
    id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    challenge    text NOT NULL UNIQUE,
    purpose      text NOT NULL CHECK (purpose IN ('register', 'login')),
    user_id      uuid REFERENCES users(id) ON DELETE CASCADE,
    session_data jsonb NOT NULL,
    created_at   timestamptz NOT NULL DEFAULT now(),
    expires_at   timestamptz NOT NULL
);
CREATE INDEX webauthn_challenges_expires ON webauthn_challenges (expires_at);
