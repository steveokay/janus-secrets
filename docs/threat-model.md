# Janus — threat model

_What Janus defends against, how, and — just as importantly — what it does
**not** defend against. This is the adversary model behind the mechanisms
described in [`docs/crypto.md`](crypto.md), [`docs/architecture.md`](architecture.md),
and the [security policy](../SECURITY.md). It scopes what counts as a
vulnerability (see [SECURITY.md → Scope](../SECURITY.md#scope))._

Janus is a **single-tenant, self-hosted** secrets manager: one Go binary plus
Postgres, run by an operator on infrastructure they control. That deployment
shape drives every decision below.

## 1. System & trust boundaries

```
                    ┌────────────────────────── operator's infrastructure ──────────────────────────┐
                    │                                                                                │
  human (browser) ──┼──TLS──▶ ┌───────────────┐        wrapped keys + ciphertext   ┌──────────────┐ │
  CI / machine   ───┼──TLS──▶ │  janus (server)│ ◀───────────────────────────────▶ │  PostgreSQL  │ │
  CLI / SDK      ───┼──TLS──▶ │               │                                     └──────────────┘ │
                    │         │  master key   │                                                      │
                    │         │  (memory only)│ ──outbound──▶ webhooks / IdP / KMS / sync targets    │
                    │         └───────────────┘                                                      │
                    └────────────────────────────────────────────────────────────────────────────────┘
```

**Trust boundaries** (where an attacker's privilege can change):

- **B1 — Network client → API.** Untrusted callers reach `/v1/`. Everything
  past this line is authenticated, authorized, and audited.
- **B2 — Server process → Postgres.** The DB stores only **wrapped** keys and
  **ciphertext**; it never holds the master key or plaintext. A read of the
  database alone does not yield secrets.
- **B3 — Server → external services.** Operator-configured outbound calls
  (notification webhooks, rotation targets, sync providers, OIDC IdPs, cloud
  KMS). These are egress points, guarded but ultimately trusted-by-config.
- **B4 — Sealed → unsealed.** Before unseal the master key does not exist in
  memory and all secret operations return `503`. Unseal (Shamir quorum or
  cloud KMS) is the transition that materializes the master key.

The **operator, the host OS, the container runtime, and the Postgres server**
are all **inside** the trust boundary. Janus protects secrets from *its own
users and from a database compromise*, not from the person who runs it.

## 2. Assets (in priority order)

1. **Secret values** — the plaintext key/value data users store.
2. **Key material** — the master key (root KEK), per-project KEKs, per-version
   DEKs, the token-HMAC key, transit keys, the TOTP secret.
3. **Audit integrity** — the tamper-evident record of who did what.
4. **Authentication material** — passwords (hashed), session cookies, service
   tokens (stored hashed), OIDC/federation trust config.
5. **Authorization state** — role bindings, scopes, four-eyes/approval policy.
6. **Availability** — a distant fifth; Janus is single-node by design.

## 3. Adversaries

| # | Adversary | Capability assumed | In scope? |
|---|---|---|---|
| A1 | **Unauthenticated network attacker** | Can reach the API / UI | ✅ Primary |
| A2 | **Authenticated low-privilege principal** (viewer/developer, scoped token, CI identity) | Valid creds, tries to exceed scope | ✅ Primary |
| A3 | **Malicious/curious insider with *some* privilege** | e.g. admin trying to defeat four-eyes, or read another project | ✅ (up to the role's intended authority) |
| A4 | **Database-only compromise** | Read/dump of Postgres, no server memory | ✅ — must not yield plaintext |
| A5 | **Network MITM** | On the wire | ⚠️ Partial — mitigated **only** if the operator runs TLS |
| A6 | **Supply-chain attacker** | Tampered release artifact | ⚠️ Addressed by signing/provenance (verify before run) |
| A7 | **Host / runtime / DBA compromise, or malicious owner** | Root on the box, or the top-level owner role | ❌ Out of scope (inside the trust boundary) |

## 4. What Janus defends against (properties + mechanisms)

**Confidentiality of secrets at rest (A4).** Envelope encryption: master key →
per-project KEKs (wrapped, stored) → per-version DEKs (AES-256-GCM, fresh
random nonce, fresh DEK per version). The master key lives **only in server
memory** after unseal and is never persisted in plaintext. AEAD additional
data is length-prefixed and domain-separated across every wrapped-blob type, so
ciphertext cannot be relocated between keys/configs (e.g. clone/promote cannot
blob-copy across a boundary). **A DB dump is inert without the master key.**

**Authentication (A1, A2).** Passwords hashed with Argon2id (constant-time
verify, dummy-hash on unknown user → no enumeration/timing oracle); session
cookies `HttpOnly` + `SameSite=Strict` (+`Secure` under TLS); service tokens are
256-bit, stored as HMAC-SHA256 only (never recoverable), with `expires_at` /
revocation checked every request and optional per-token IP allowlists.
Optional TOTP second factor (RFC 6238, single-use codes + recovery codes),
progressive per-account lockout, and short-lived tokens for OIDC-federated CI
identities (alg-confusion / claim-spoofing rejected).

**Authorization & isolation (A2, A3).** Deny-by-default RBAC; bindings/grants
match scopes exactly (no sibling/parent leak); cross-project isolation enforced
at the API boundary; four-eyes approval for protected-config writes (requester ≠
approver, write-authorized approver, claim-before-commit). Break-glass
elevation is time-boxed and cannot be laundered into a durable grant. Secret
references resolve under the caller's own `secret:read`, with cycle/depth caps.

**Audit integrity (A3).** Append-only `audit_events`, each carrying the SHA-256
of its predecessor (hash chain); `GET /v1/audit/verify` walks it. Audit write
failure fails the mutation (no unaudited changes to secrets/keys/tokens/policy).
Audit records **paths and key names, never values**.

**The value-never-leaks invariant.** `audit.Event` has no value field by
construction; dynamic/rotation errors use fixed sentinels (no DSN/password
interpolation); plaintext buffers are best-effort zeroized; the CLI never writes
plaintext to disk without an explicit `--plain`; the SPA holds revealed
plaintext only in component state and emits zero `console.*`. Enforced by ~20
per-package leak tests.

**Injection & web attack surface (A1, A2).** All store queries are
parameterized; download/path operations are traversal-guarded. The SPA ships a
strict CSP (`default-src 'self'`, no `unsafe-inline` script/eval,
`frame-ancestors 'none'`), bundles fonts/QR (no CDN), and uses cookie auth (no
JS-readable token).

**Outbound request safety (B3, defense-in-depth).** Every operator-configured
outbound caller dials through a shared hardened dialer that re-checks the
**resolved** IP at connect time (defeating DNS-rebinding), blocks
link-local/cloud-metadata ranges by default, caps redirects, and bounds dial
timeouts. This is **defense-in-depth against a mis-/maliciously-configured
integration**, not an authorization boundary — configuring outbound targets is
already an admin-gated privilege. `JANUS_OUTBOUND_BLOCK_PRIVATE=true`
additionally rejects loopback/RFC1918/ULA, and `JANUS_OUTBOUND_ALLOW` exempts
named IPs/CIDRs from *that* tightening only: no allowlist entry can reach the
link-local/cloud-metadata ranges, and an entry naming one is a startup error
rather than a silent no-op, so the highest-value SSRF target stays unreachable
through configuration. The resolved-IP recheck only holds when Janus
dials the destination itself, so these clients ignore `HTTP_PROXY`/`HTTPS_PROXY`
by default; `JANUS_OUTBOUND_ALLOW_PROXY=true` re-enables proxying, logs a
startup warning, and leaves only a best-effort URL-time literal-IP check —
through a proxy the destination is resolved by the proxy and is not inspectable
here (see [production deployment §3](guides/production-deployment.md#3-configuration)).

**Supply chain (A6).** Tagged releases are Cosign-signed (keyless/OIDC) with
Syft SBOMs and SLSA build-provenance attestations, so operators can verify an
artifact's origin before running it. CI runs `govulncheck` + `gosec` as
build-failing gates, and Dependabot proposes dependency updates.

**Sealed-by-default (B4).** The server boots sealed; all secret operations
`503` until a Shamir quorum or cloud-KMS unseal reconstructs the master key,
verified against a key-check value. A crash returns the process to sealed.

## 5. What Janus does NOT defend against (explicit non-defenses)

These are deliberate scope decisions. Reporting one as a vulnerability will be
closed as out-of-scope (but a *bypass* of a control in §4 is very much in scope).

- **A compromised host / container / Postgres server, or memory capture of an
  unsealed process (A7).** Once unsealed, the master key is in RAM by design; an
  attacker with root or a memory dump can read it. Protect the host.
- **A malicious top-level `owner`, or any principal acting within the authority
  its role legitimately grants (A3 ceiling).** RBAC constrains *scope*, not the
  intent of someone you granted power to. An `owner` can read secrets; that is
  not a bug. Use least-privilege roles and the audit log.
- **Multi-tenancy / organizational isolation.** Janus is **single-tenant**.
  There is no defense between "organizations" because there are none — run
  separate deployments for separate trust domains.
- **Transport security by itself.** Janus can terminate TLS (static or ACME),
  but if you run it plaintext behind something you trust, MITM (A5) is on you.
- **High availability / durability.** Single node + Postgres + your backups.
  No Raft/clustering; DoS-to-unavailability is not treated as a security issue
  beyond the documented rate limits.
- **The cryptographic primitives themselves.** We assume AES-256-GCM, SHA-256,
  HMAC, Argon2id, Ed25519, and the Go `crypto/*` + `x/crypto` implementations
  are sound. Janus does not implement its own primitives and makes no FIPS
  certification claim.
- **Cloud KMS / IdP compromise.** Auto-unseal trusts the configured cloud KMS;
  OIDC login trusts the configured IdP. Their compromise is their threat model.
  Note that with **group bindings** configured (`groups_claim` set), the IdP
  drives *role assignment* and not only authentication: whoever can edit
  membership of a mapped group can grant the Janus roles that group is bound to,
  without touching Janus. That authority is deliberately capped — a group
  binding can never be `owner`, so the master-key, audit-prune and
  secret-destroy tier still requires a binding recorded in Janus itself — but it
  reaches up to `admin`. Group membership in your directory should be treated as
  privileged configuration and reviewed like a Janus binding.
- **Hiding an event from a SCOPED audit view via direct database access.** Audit
  events carry a `project_id` used to filter project-scoped reads, and it is
  deliberately outside the hash chain (adding a hashed field would break
  verification for every existing event). An actor with direct Postgres access
  could therefore re-point an event so a team's scoped view misses it. That
  actor is already outside the trust boundary above, the **instance-wide view
  remains complete**, and the chain still detects any tampering with the event's
  own contents — but a scoped view is a convenience filter, not evidence in its
  own right, and should not be relied on as one.
- **The non-goals** (PKI/CA, SSH signing, HSM/PKCS#11) — not built, so not
  defended.

## 6. Operator responsibilities (residual risk)

Janus's guarantees hold **only** if the operator:

- Runs it over **TLS** (terminated by Janus or a trusted proxy) — closes A5.
- Protects the **host, container, and Postgres** — the trust boundary (A7).
- Safeguards **unseal material**: distributes Shamir shares to distinct
  custodians, or locks down the cloud-KMS key's IAM (auto-unseal trades a quorum
  ceremony for trust in the KMS).
- Applies **least privilege**: minimal roles/scopes, four-eyes on protected
  configs, short-lived tokens/CI federation over long-lived tokens.
- Treats **IdP group membership as privileged** where group bindings are used:
  restrict who can edit a mapped group in the directory, and include those
  groups in the same access review as Janus bindings. Because IdP-fed and local
  groups never mix, an access review against the directory *is* complete for
  IdP-derived access — anything granted outside it necessarily appears as a
  local-group or direct binding in Janus.
- Keeps **backups** of the sealed state + Postgres, and rehearses restore.
- **Verifies release signatures/provenance** before deploying, and keeps
  current with security releases.
- Reviews the **audit log** (and ships it to a SIEM) — it is the detection
  layer for insider actions Janus intentionally permits.

## 7. Cryptographic assumptions (summary)

- AES-256-GCM for all symmetric encryption; nonces are fresh CSPRNG values,
  never reused; a fresh DEK per secret version.
- Argon2id for password hashing; HMAC-SHA256 for token hashing; SHA-256 for the
  audit chain; Ed25519 for transit signing.
- AEAD additional data is length-prefixed and domain-separated so no wrapped
  blob is valid in another context.
- The master key has sufficient entropy (256-bit) and is only ever reconstructed
  transiently in memory (Shamir quorum or KMS decrypt), verified by a key-check
  value.
- Constant-time comparison for all token/MAC checks.

---

_This document evolves with the system. If you believe a property in §4 can be
broken, that is a vulnerability — see [SECURITY.md](../SECURITY.md)._
