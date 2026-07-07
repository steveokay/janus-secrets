CREATE TABLE oidc_providers (
  id                    uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  name                  text NOT NULL UNIQUE,
  issuer                text NOT NULL,
  client_id             text NOT NULL,
  wrapped_client_secret bytea NOT NULL,
  scopes                text[] NOT NULL DEFAULT '{openid,email,profile}',
  redirect_url          text NOT NULL,
  enabled               bool NOT NULL DEFAULT true,
  created_at            timestamptz NOT NULL DEFAULT now(),
  updated_at            timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE oidc_identities (
  id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  user_id       uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  issuer        text NOT NULL,
  subject       text NOT NULL,
  created_at    timestamptz NOT NULL DEFAULT now(),
  last_login_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE (issuer, subject)
);

CREATE TABLE oidc_auth_requests (
  state         text PRIMARY KEY,
  nonce         text NOT NULL,
  pkce_verifier text NOT NULL,
  provider_id   uuid NOT NULL REFERENCES oidc_providers(id) ON DELETE CASCADE,
  created_at    timestamptz NOT NULL DEFAULT now(),
  expires_at    timestamptz NOT NULL
);
