CREATE TABLE users (
    id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    email         text NOT NULL,
    -- Argon2id PHC string; NULL reserves room for Phase-2 OIDC-only users.
    password_hash text,
    created_at    timestamptz NOT NULL DEFAULT now(),
    updated_at    timestamptz NOT NULL DEFAULT now(),
    disabled_at   timestamptz
);
CREATE UNIQUE INDEX users_email_lower_idx ON users (lower(email));

CREATE TABLE sessions (
    id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id      uuid NOT NULL REFERENCES users(id),
    -- HMAC-SHA256 of the cookie value; the raw value is never stored.
    token_hmac   bytea NOT NULL UNIQUE,
    created_at   timestamptz NOT NULL DEFAULT now(),
    expires_at   timestamptz NOT NULL,
    last_seen_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE service_tokens (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    name        text NOT NULL,
    token_hmac  bytea NOT NULL UNIQUE,
    created_by  uuid NOT NULL REFERENCES users(id),
    scope_kind  text NOT NULL CHECK (scope_kind IN ('config', 'environment')),
    scope_id    uuid NOT NULL,
    access      text NOT NULL CHECK (access IN ('read', 'readwrite')),
    created_at  timestamptz NOT NULL DEFAULT now(),
    expires_at  timestamptz,
    revoked_at  timestamptz
);

CREATE TABLE auth_config (
    id                     integer PRIMARY KEY DEFAULT 1 CHECK (id = 1),
    -- Random 256-bit HMAC key, wrapped by the master key (AAD janus:auth:token-hmac).
    wrapped_token_hmac_key bytea NOT NULL
);
