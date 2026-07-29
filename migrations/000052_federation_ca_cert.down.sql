-- Dropping the column returns every trusted issuer to system-roots-only TLS
-- verification. That is a TIGHTENING, not a widening — nothing becomes trusted
-- that was not before — but it is not harmless: any issuer that was reachable
-- only via a private CA (a self-hosted Kubernetes cluster's own API server)
-- stops verifying, and its workloads' exchanges start failing closed with the
-- generic federation_denied. Re-add the bundle after migrating back up.
ALTER TABLE oidc_federation_config DROP COLUMN IF EXISTS ca_cert;
