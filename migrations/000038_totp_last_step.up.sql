-- Track the last TOTP time-step consumed by a successful login so a code that
-- validated once cannot be replayed within its ±skew window (RFC 6238 §5.2).
-- NULL = no successful TOTP verification yet. A verification only succeeds when
-- the matched step is strictly greater than last_step, and the update is a
-- conditional CAS so two concurrent logins presenting the same code cannot both
-- win. Recovery-code logins do not touch this column.
ALTER TABLE user_totp ADD COLUMN last_step BIGINT;
