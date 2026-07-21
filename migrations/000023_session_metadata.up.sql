-- Session client metadata for the session-management surface: a user can list
-- their own active sessions and spot/revoke an unfamiliar one. Both columns are
-- nullable — pre-existing sessions and any non-HTTP mint carry NULL, and neither
-- is a secret (the raw cookie is still never stored; only its HMAC is).
ALTER TABLE sessions ADD COLUMN ip         text;
ALTER TABLE sessions ADD COLUMN user_agent text;
