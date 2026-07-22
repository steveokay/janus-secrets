ALTER TABLE users DROP COLUMN IF EXISTS last_failed_login_at;
ALTER TABLE users DROP COLUMN IF EXISTS locked_until;
ALTER TABLE users DROP COLUMN IF EXISTS lockout_level;
ALTER TABLE users DROP COLUMN IF EXISTS failed_login_count;
