-- Reverse 000044. Any pending discoverable-login challenge is dropped first so
-- the narrower CHECK can be re-applied; a pending ceremony is a five-minute
-- throwaway, and losing one only means the user retries.
DELETE FROM webauthn_challenges WHERE purpose = 'login_discoverable';

ALTER TABLE webauthn_challenges
    DROP CONSTRAINT webauthn_challenges_purpose_check;
ALTER TABLE webauthn_challenges
    ADD CONSTRAINT webauthn_challenges_purpose_check
    CHECK (purpose IN ('register', 'login'));

ALTER TABLE webauthn_credentials
    DROP COLUMN discoverable;
