-- Multi-issuer federation (roadmap 7.3, Kubernetes service-account federation).
--
-- oidc_federation_config becomes a SET of trusted issuers (one row per issuer)
-- rather than the single row the delete-then-insert upsert enforced, so a
-- deployment can trust its CI provider AND its Kubernetes cluster at the same
-- time. Every trust binding is pinned to exactly one issuer, so a token signed
-- by issuer A can never match a binding written for issuer B.

-- Defensive de-dup before the unique index: the old single-row upsert already
-- guaranteed at most one row, but never assume it.
DELETE FROM oidc_federation_config c
 WHERE c.ctid NOT IN (SELECT min(ctid) FROM oidc_federation_config GROUP BY issuer);

-- preset selects the provider-aware required-claim rule. Kubernetes cluster
-- issuer URLs are cluster-specific (EKS/GKE/self-hosted all differ) and cannot
-- be recognised from the URL, so the rule is chosen explicitly.
ALTER TABLE oidc_federation_config ADD COLUMN preset text NOT NULL DEFAULT '';
ALTER TABLE oidc_federation_config
    ADD CONSTRAINT oidc_federation_config_preset_check
    CHECK (preset IN ('', 'github', 'gitlab', 'buildkite', 'circleci', 'kubernetes', 'custom'));

CREATE UNIQUE INDEX oidc_federation_config_issuer_key ON oidc_federation_config (issuer);

-- Bindings carry their issuer. Backfill from the pre-existing single config row
-- (or the historical GitHub Actions default when none was configured) so an
-- upgraded deployment keeps matching exactly the tokens it matched before.
ALTER TABLE oidc_federation_bindings ADD COLUMN issuer text NOT NULL DEFAULT '';
UPDATE oidc_federation_bindings SET issuer = COALESCE(
    (SELECT issuer FROM oidc_federation_config ORDER BY created_at LIMIT 1),
    'https://token.actions.githubusercontent.com');
ALTER TABLE oidc_federation_bindings ALTER COLUMN issuer DROP DEFAULT;
ALTER TABLE oidc_federation_bindings
    ADD CONSTRAINT oidc_federation_bindings_issuer_check CHECK (issuer <> '');
CREATE INDEX oidc_federation_bindings_issuer_idx ON oidc_federation_bindings (issuer);
