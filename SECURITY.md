# Security Policy

Janus is a self-hosted secrets manager, so we take vulnerabilities in it
seriously. Thank you for helping keep Janus and its users safe.

## Reporting a vulnerability

**Please do not open a public issue, discussion, or pull request for a
suspected vulnerability** — that would disclose it to attackers before a fix
is available.

Instead, report privately through **GitHub's private vulnerability reporting**:

1. Go to the repository's **Security** tab.
2. Click **Report a vulnerability** (under *Advisories*).
3. Fill in the details — this opens a private channel visible only to you and
   the maintainers.

Direct link: <https://github.com/steveokay/janus-secrets/security/advisories/new>

If you cannot use GitHub private reporting, you may instead reach the
maintainer at **mokaysteve@gmail.com** with the subject line
`JANUS SECURITY` — but the GitHub channel is strongly preferred, and please do
not include full exploit detail in an initial email.

### What to include

A good report lets us reproduce and triage quickly:

- The version / commit (`janus version`, or the tag / image digest you ran).
- Affected component (server API, CLI, crypto/unseal, auth/OIDC, an
  integration/rotator/sync provider, the web UI, an SDK, the Terraform
  provider).
- A description of the issue and its impact, with a concrete reproduction
  (request sequence, config, or a minimal PoC).
- Whether the issue is already public anywhere.

Please **do not include real secret values, production credentials, or
customer data** in a report. Redact them — we never need them to understand a
finding.

## Our commitment (response targets)

Janus is maintained on a best-effort basis; these are targets, not contractual
SLAs:

| Stage | Target |
|---|---|
| Acknowledge your report | within **3 business days** |
| Initial assessment & severity | within **7 business days** |
| Fix or documented mitigation for HIGH/CRITICAL | within **30 days** where feasible |

We will keep you updated through the private advisory, credit you in the
advisory and release notes (unless you prefer to remain anonymous), and
coordinate a disclosure timeline with you. We support **coordinated
disclosure** and will publish a GitHub Security Advisory (and, where relevant,
a CVE) once a fix is available.

## Supported versions

Janus is pre-1.0 and ships from a single line of development. Security fixes
land on `main` and in the next tagged release; there is no long-term-support
back-port branch yet.

| Version | Supported |
|---|---|
| `main` (latest) | ✅ |
| Latest `v0.1.x` release | ✅ |
| Older tagged releases | ❌ — upgrade to the latest |

Run the latest release (or `main`) to receive security fixes.

## Scope

**In scope** — anything that lets an actor exceed their intended authority or
break a core security property of Janus, for example:

- Bypassing authentication, RBAC/authorization, four-eyes approval, or
  cross-tenant/cross-project isolation.
- Recovering plaintext secret values or key material from ciphertext, logs,
  audit records, error messages, backups, or the sealed state.
- Flaws in the envelope-encryption hierarchy, unseal (Shamir / cloud-KMS),
  transit engine, or token/session handling.
- Audit-log tampering that defeats the hash chain, or unaudited mutations of
  secrets/keys/tokens/policies.
- Injection (SQL, command, template), SSRF against internal/metadata targets,
  path traversal, or XSS/CSP bypass in the web UI.
- Secret material leaking into logs, error strings, or telemetry.

**Out of scope / not a vulnerability** — these are documented design choices or
things Janus deliberately does **not** defend against (see
[`docs/threat-model.md`](docs/threat-model.md) for the full model):

- The [non-goals](README.md): HA/Raft, PKI/CA, SSH signing, HSM/PKCS#11,
  multi-tenancy/organizations, FIPS certification.
- Compromise of the host, container runtime, or the Postgres server Janus runs
  on; theft of an unsealed server's memory; a malicious operator/owner.
- Attacks requiring already-valid high-privilege credentials to perform actions
  that role legitimately allows (e.g. an `owner` reading secrets, an admin
  configuring an outbound integration to a host they control — SSRF egress
  guards are defense-in-depth, not an authorization boundary).
- Denial of service from resource exhaustion, missing rate limits beyond the
  documented ones, or self-inflicted lockout.
- Vulnerabilities in third-party dependencies with no exploitable path in
  Janus (report those upstream; we track them via `govulncheck` in CI).
- Social engineering, physical access, or issues requiring a
  man-in-the-middle on a connection the operator was told to run over TLS.

## Safe harbor

We consider security research conducted in good faith under this policy to be
authorized. If you make a good-faith effort to comply with this policy — you
report promptly, avoid privacy violations and data destruction, do not
degrade the service for others, and only interact with systems/accounts you
own or have explicit permission to test — we will not pursue or support legal
action against you, and we will work with you to understand and resolve the
issue. This authorization does not extend to third-party systems or to actions
that violate applicable law.

## Verifying what you run

Release artifacts are built by a tagged GitHub Actions workflow and are
**signed and attested** so you can verify provenance before running them —
Cosign keyless signatures on the checksums file and container images, Syft
SBOMs, and SLSA build-provenance attestations. See the release notes and
[`docs/guides/production-deployment.md`](docs/guides/production-deployment.md)
for verification commands (`cosign verify-blob`, `cosign verify`, and
`gh attestation verify`).
