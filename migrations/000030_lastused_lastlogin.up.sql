-- Track when a service token was last used (throttled writes on auth) and when a
-- local user last logged in (password + OIDC session mint). Both are value-free
-- timestamps: no secret material, no token/hash, no password. Nullable, no
-- default — NULL means "never used"/"never logged in".
ALTER TABLE service_tokens ADD COLUMN last_used_at timestamptz;
ALTER TABLE users ADD COLUMN last_login_at timestamptz;
