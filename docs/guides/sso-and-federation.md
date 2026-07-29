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

Janus does **not** auto-provision accounts: a user must already exist here
before they can sign in through the IdP.

### Group claim → role bindings (optional)

Set **Groups claim** to the ID-token claim carrying group membership — `groups`
for Okta, Entra and Google, or a dotted path like `realm_access.roles` for
Keycloak. Leaving it empty disables group sync entirely; you opt in.

With it set, create a group in Janus per claim value (**Groups → + New group**,
kind `oidc`) and bind that group at a scope. One binding then grants the whole
team a role, membership is maintained in your directory, and removing someone
there removes their access here at their next sign-in.

Entra emits group **object GUIDs** by default, so paste the GUID as the claim
value and give the group a readable name — the name is what appears on
bindings. Note that Entra stops sending the claim past roughly 200 groups per
user; Janus detects that and keeps the last known membership rather than
clearing it, and records `group.sync` with `status=overage` in the audit
ledger. Full detail, including what a group can and cannot be granted, is in
the [groups guide](groups.md).

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

   **CA certificate (optional).** The same form takes a PEM bundle used to verify
   the issuer's TLS. Leave it empty for any public issuer — every CI provider and
   the managed Kubernetes issuers (EKS/GKE/AKS). Fill it in for a **self-hosted
   cluster whose issuer is its own API server** (`https://kubernetes.default.svc`):
   that certificate is signed by the cluster CA, which no system root chains to,
   so without the bundle every exchange fails with the same opaque denial as a
   bad token. Get it with:

   ```sh
   kubectl config view --raw --minify \
     -o jsonpath='{.clusters[0].cluster.certificate-authority-data}' | base64 -d
   ```

   A bundle **replaces** the system roots for that issuer and applies to it
   alone; verification is never disabled. Clear the field to go back to the
   system roots. Malformed PEM is rejected when you save, not silently at the
   next exchange. Issuers using a bundle are marked *custom CA* in the list.

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
