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

## Machine identity federation (no long-lived secret)

Lets a CI pipeline — or a Kubernetes pod — exchange its runtime OIDC JWT for a
short-lived scoped `janus_svc_…` token, with nothing stored in CI or in the
cluster. **GitHub Actions, GitLab CI/CD, Buildkite, CircleCI and Kubernetes
service accounts** are supported, and **several issuers can be trusted at once**
(each trust binding is pinned to one issuer, so they cannot impersonate each
other).

1. **Machine identity federation → Configure / + Trusted issuer**: pick a
   **provider preset** (fills the issuer URL where it is fixed and names the
   claims to bind), then set the audience your workloads will request (commonly
   your Janus URL). Issuers:

   | Provider | Issuer | Strong claim(s) to bind |
   |---|---|---|
   | GitHub Actions | `https://token.actions.githubusercontent.com` | `repository` |
   | GitLab CI/CD | `https://gitlab.com` (or self-hosted URL) | `project_path` |
   | Buildkite | `https://agent.buildkite.com` | `organization_slug` |
   | CircleCI | `https://oidc.circleci.com/org/<ORG_ID>` | `oidc.circleci.com/project-id` |
   | Kubernetes | cluster-specific (`kubectl get --raw /.well-known/openid-configuration`) | `kubernetes.io.namespace` + `kubernetes.io.serviceaccount.name`, or `sub` |

   The cluster issuer must be reachable from Janus — EKS and GKE publish theirs
   publicly, most self-hosted clusters do not. See the
   [federation reference](../ci-federation.md#kubernetes-service-accounts).

2. **+ Trust binding**: name, the issuer, the strong identifying claim value(s)
   the JWT must carry, scope (a config or environment), access (read /
   read-write), TTL (≤ 1 hour). Every binding must constrain at least one strong
   claim for its issuer (a claim-less binding is rejected). A workload can
   federate only if **exactly one** enabled binding **for its issuer** matches
   its claims.
3. In the pipeline (or the Pod spec), request an ID token and exchange it at
   `POST /v1/auth/oidc/federate` — full YAML per provider in the
   [federation reference](../ci-federation.md); the GitHub flow is also in the
   [GitHub Actions guide](github-actions.md).

Delete a binding to cut that workload off immediately; disable or remove a
trusted issuer to stop all exchanges for it (the others keep working).

## Outbound sync summary

The bottom card lists sync targets with their state and last push, linking to
[Operations](operations-console.md) where they're managed.
