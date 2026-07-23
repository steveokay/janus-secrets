# How-to: SSO login and keyless CI via the Integrations page

**Integrations** is the admin surface for the two OIDC features (system
references: [oidc.md](../oidc.md), [ci-federation.md](../ci-federation.md))
plus a status summary of outbound sync.

## OIDC single sign-on (humans)

**Integrations → OIDC single sign-on → Configure**:

1. Display name (what the login button says), issuer URL, client ID, client
   secret, redirect URL (prefilled to `<your-origin>/v1/auth/oidc/callback` —
   register the same URL with your provider).
2. Save. The login page now shows *continue with <name>*.

The client secret is **write-only**: the form must be re-entered on every
save, and the read view only reports whether one is set. The flow is
Authorization Code + PKCE with state/nonce; tested against GitHub and Google.
Password login keeps working alongside SSO.

## CI federation (machines, no long-lived secret)

Lets a CI pipeline exchange its runtime OIDC JWT for a short-lived scoped
`janus_svc_…` token — nothing stored in CI. **GitHub Actions, GitLab CI/CD,
Buildkite, and CircleCI** are supported; **one provider is active at a time**.

1. **CI federation → Configure**: pick a **provider preset** (fills the issuer
   URL and hints the claim to bind), then set the audience your pipelines will
   request (commonly your Janus URL). Issuers:

   | Provider | Issuer | Strong claim to bind |
   |---|---|---|
   | GitHub Actions | `https://token.actions.githubusercontent.com` | `repository` |
   | GitLab CI/CD | `https://gitlab.com` (or self-hosted URL) | `project_path` |
   | Buildkite | `https://agent.buildkite.com` | `organization_slug` |
   | CircleCI | `https://oidc.circleci.com/org/<ORG_ID>` | `oidc.circleci.com/project-id` |

2. **+ Trust binding**: name, the provider's strong identifying claim value the
   JWT must carry, scope (a config or environment), access (read / read-write),
   TTL (≤ 1 hour). Every binding must constrain at least one strong claim for
   the configured provider (a claim-less binding is rejected). A pipeline can
   federate only if **exactly one** enabled binding matches its claims.
3. In the pipeline, request an ID token and exchange it at
   `POST /v1/auth/oidc/federate` — full YAML per provider in the
   [CI federation reference](../ci-federation.md); the GitHub flow is also in the
   [GitHub Actions guide](github-actions.md).

Delete a binding to cut that pipeline off immediately; disable the federation
config to stop all exchanges.

## Outbound sync summary

The bottom card lists sync targets with their state and last push, linking to
[Operations](operations-console.md) where they're managed.
