-- Per-token IP allowlists + new-IP anomaly tracking. Both are value-free: they
-- store IPs/CIDRs and timestamps only — never a token value, HMAC, or secret.
--
-- service_tokens.ip_allowlist: optional list of CIDRs (IPv4 or IPv6). When
-- non-empty, a request authenticated with the token whose client IP is outside
-- every listed CIDR is rejected (403). NULL / empty means "any IP" (unchanged
-- behaviour). Validated at the API boundary with net.ParseCIDR.
ALTER TABLE service_tokens ADD COLUMN ip_allowlist text[];

-- token_seen_ips records the distinct client IPs a token has authenticated
-- from, for new-IP anomaly surfacing. One row per (token, ip); the first time a
-- token is seen from an IP, the INSERT ... ON CONFLICT DO NOTHING inserts a new
-- row (naturally throttling writes to genuinely new pairs). first_seen_at is
-- when that IP was first observed. Deleting the token cascades its seen-IP rows.
CREATE TABLE token_seen_ips (
    token_id      uuid        NOT NULL REFERENCES service_tokens(id) ON DELETE CASCADE,
    ip            text        NOT NULL,
    first_seen_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (token_id, ip)
);

-- Cheap "used from a new IP recently" surface for the Overview in-tray: scan
-- newest-first by first_seen_at without a full-table sort.
CREATE INDEX token_seen_ips_first_seen_idx ON token_seen_ips (first_seen_at DESC);
