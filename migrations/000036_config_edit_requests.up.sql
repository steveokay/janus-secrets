-- Protected configs (require_approval): direct secret saves to a config with
-- require_approval = true do not commit immediately; they become a pending
-- config_edit_requests row that a DIFFERENT user must approve (four-eyes),
-- reusing the promotion-approval patterns.

ALTER TABLE configs
    ADD COLUMN require_approval boolean NOT NULL DEFAULT false;

-- A proposed, not-yet-committed batch of secret edits awaiting four-eyes
-- approval. The proposed changes ([]SecretChange serialized to JSON) are stored
-- ENVELOPE-ENCRYPTED: a fresh DEK (wrapped by the config's project KEK) encrypts
-- the blob, so proposed secret VALUES are never at rest in plaintext. The row's
-- metadata (requester, reason, status, changed key NAMES in changed_keys) is
-- value-free.
CREATE TABLE config_edit_requests (
    id                 uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    config_id          uuid NOT NULL REFERENCES configs (id) ON DELETE CASCADE,
    requested_by       uuid NOT NULL REFERENCES users (id),
    reason             text NOT NULL DEFAULT '',
    status             text NOT NULL DEFAULT 'pending',
    -- envelope-encrypted proposed []SecretChange (never plaintext values)
    proposed_ciphertext bytea NOT NULL,
    wrapped_dek        bytea NOT NULL,
    nonce              bytea NOT NULL,
    dek_key_version    integer NOT NULL DEFAULT 1,
    -- value-free: names of the keys the request would change (for list views)
    changed_keys       jsonb NOT NULL DEFAULT '[]'::jsonb,
    message            text NOT NULL DEFAULT '',
    resolved_by        uuid REFERENCES users (id),
    resolved_at        timestamptz,
    applied_version    integer,
    created_at         timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX idx_config_edit_requests_config_status ON config_edit_requests (config_id, status, created_at DESC, id DESC);
CREATE INDEX idx_config_edit_requests_requester ON config_edit_requests (requested_by, status);
