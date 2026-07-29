-- Per-issuer CA bundle for federation OIDC discovery + JWKS.
--
-- WHY: a federation issuer whose TLS certificate is signed by a PRIVATE CA could
-- never be verified. The verifier was built on one process-wide HTTP client that
-- trusts the SYSTEM roots and nothing else, so for the default Kubernetes issuer
-- (https://kubernetes.default.svc, served by the API server under the CLUSTER CA)
-- discovery failed the TLS handshake no matter how reachable the endpoint was.
-- The failure surfaced as a generic 401 from the exchange, which is exactly the
-- wrong shape of feedback for a certificate problem — it reads as "bad token".
--
-- The asymmetry that settles it: the Kubernetes SYNC provider already accepts an
-- explicit `ca_cert` and works in the same cluster where federation could not.
-- Two features dialling the same API server, one of them able to state its trust
-- anchor and the other not, is an accident of implementation order, not a design.
--
-- Managed clusters (EKS/GKE/AKS) are unaffected: their issuers are public
-- endpoints with publicly-trusted certificates, so they leave this column empty
-- and keep using the system roots.
--
-- NOT A SECRET. A CA certificate is public material — it is presented in every
-- TLS handshake — so it is stored in plaintext, needs no master-key wrapping, and
-- master-key rotation has nothing to re-wrap here. It is still never written to a
-- log or an audit entry: audit records only that a bundle is set.
--
-- Trust semantics (enforced in internal/auth, stated here so the column is not
-- misread): a non-empty ca_cert REPLACES the system roots for THIS issuer rather
-- than adding to them. That is the stricter reading and matches the sync
-- provider. It is also the useful one: an issuer is one specific host, so pinning
-- it to the one CA that should have signed it means a mis-issuance by any public
-- CA cannot be used to impersonate it. An operator who wants public roots leaves
-- the column empty. There is deliberately no way to disable verification.
ALTER TABLE oidc_federation_config ADD COLUMN ca_cert text NOT NULL DEFAULT '';

-- Empty means "use the system roots", which is precisely the pre-upgrade
-- behaviour, so every existing trusted issuer keeps verifying exactly as before.
COMMENT ON COLUMN oidc_federation_config.ca_cert IS
    'PEM CA bundle used to verify TLS for this issuer''s OIDC discovery and JWKS '
    'fetches. Empty = system roots. Public material, stored in plaintext. '
    'When set it REPLACES the system roots for this issuer.';
