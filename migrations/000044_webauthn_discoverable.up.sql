-- Passwordless (client-side discoverable) passkey login.

-- Whether the authenticator actually stored this credential as DISCOVERABLE
-- (a "resident key"), i.e. whether it can be used to sign in with no email
-- typed first. This is reported by the client through the `credProps`
-- extension at registration time; it is a property of the credential, not a
-- secret, and it is advisory metadata only — the server never trusts it to
-- make an authorization decision.
--
-- NULL means UNKNOWN, and is the correct value for every credential enrolled
-- before this migration: those ceremonies ran with residentKey "preferred"
-- and did not request credProps, so whether the authenticator stored a
-- discoverable credential was never recorded. The UI surfaces "unknown"
-- rather than guessing, so a user is never told a passkey will work
-- passwordlessly when it might not.
ALTER TABLE webauthn_credentials
    ADD COLUMN discoverable boolean;

-- A discoverable login ceremony is a THIRD challenge pool. It must be separate
-- from the identified-login pool: a discoverable challenge carries no user
-- (identity comes from the assertion's userHandle) while an identified one is
-- bound to exactly one account, so allowing one to be finished as the other
-- would let a caller opt out of that binding.
ALTER TABLE webauthn_challenges
    DROP CONSTRAINT webauthn_challenges_purpose_check;
ALTER TABLE webauthn_challenges
    ADD CONSTRAINT webauthn_challenges_purpose_check
    CHECK (purpose IN ('register', 'login', 'login_discoverable'));
