-- Progressive per-account lockout state. Complements the per-IP token-bucket
-- limiter (which covers spray from one IP) and the manual admin disable
-- (users.disabled_at). All four columns are 1:1 with the user, low write
-- volume, and carry no secret material.
--
--   failed_login_count   consecutive failures in the CURRENT cycle; reset to 0
--                        when a lock trips (starting the next cycle) and on a
--                        successful login.
--   lockout_level        number of locks triggered since the last success;
--                        drives the escalating window. Reset on success.
--   locked_until         when set and in the future, the account is locked.
--                        Auto-expires (no admin action required).
--   last_failed_login_at timestamp of the most recent counted failure.
ALTER TABLE users
  ADD COLUMN failed_login_count   int         NOT NULL DEFAULT 0,
  ADD COLUMN lockout_level        int         NOT NULL DEFAULT 0,
  ADD COLUMN locked_until         timestamptz,
  ADD COLUMN last_failed_login_at timestamptz;
