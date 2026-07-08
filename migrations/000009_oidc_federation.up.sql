-- CI federation: a single trust-provider row + claim-matched bindings that mint
-- short-lived scoped service tokens for CI jobs (GitHub Actions OIDC).
CREATE TABLE oidc_federation_config (
    id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    issuer     text NOT NULL,
    audience   text NOT NULL,
    enabled    boolean NOT NULL DEFAULT false,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE oidc_federation_bindings (
    id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    name         text NOT NULL UNIQUE,
    match_claims jsonb NOT NULL,
    scope_kind   text NOT NULL CHECK (scope_kind IN ('config', 'environment')),
    scope_id     uuid NOT NULL,
    access       text NOT NULL CHECK (access IN ('read', 'readwrite')),
    ttl_seconds  integer NOT NULL CHECK (ttl_seconds > 0),
    enabled      boolean NOT NULL DEFAULT true,
    created_at   timestamptz NOT NULL DEFAULT now(),
    updated_at   timestamptz NOT NULL DEFAULT now()
);

-- Federated tokens have no human minter: allow NULL created_by and record the
-- binding that minted the token (forensics + integrity). Existing user tokens
-- keep a non-null created_by.
ALTER TABLE service_tokens ALTER COLUMN created_by DROP NOT NULL;
ALTER TABLE service_tokens ADD COLUMN federation_binding uuid
    REFERENCES oidc_federation_bindings(id) ON DELETE SET NULL;
ALTER TABLE service_tokens ADD CONSTRAINT service_tokens_minter_presence
    CHECK (created_by IS NOT NULL OR federation_binding IS NOT NULL);
