# Federation (OIDC machine identity)

Federation lets a workload authenticate to Janus using its platform-issued OIDC
token instead of a stored long-lived secret. This is sub-project **C2** —
machine identity. Human sign-in (C1) is covered in [`docs/oidc.md`](oidc.md).

A CI job or a Kubernetes pod presents its ephemeral OIDC token; Janus verifies
it, matches its claims against a pre-registered **trust binding**, and returns a
**short-lived, scoped `janus_svc_` service token**. That token is an ordinary
service token — it authorizes downstream requests exactly like any other,
appears in the token list, is revocable, and expires on its own. Nothing
long-lived is stored in CI or in the cluster.

Five identity providers are supported out of the box: **GitHub Actions**,
**GitLab CI/CD**, **Buildkite**, **CircleCI**, and **Kubernetes service
accounts**.

**Several issuers can be trusted at the same time.** The trusted-issuer set is a
list (`/v1/sys/oidc/federation/issuers`); each entry carries its own audience and
provider preset, and **every trust binding is pinned to exactly one issuer**. A
token signed by issuer A can never satisfy a binding written for issuer B, no
matter how well its claims line up — so trusting your CI provider and your
Kubernetes cluster together is safe, and neither can impersonate the other.

> **Upgrading:** the pre-existing single-issuer config is preserved as the first
> entry of the set, and every existing binding is backfilled with that issuer
> (GitHub Actions if none was configured), so behaviour is unchanged until you
> add a second issuer. The legacy `PUT /v1/sys/oidc/federation` still replaces
> the whole set with one issuer, but **refuses with `409`** once several are
> trusted rather than silently dropping the others.

## Flow

The exchange is verify → match → mint:

1. The workload requests an OIDC token from its platform **with the audience
   Janus is configured to require** (for GitHub Actions, the `audience` input on
   the token request; for Kubernetes, the `audience` of a projected service
   account token volume).
2. The job `POST`s the token to `POST /v1/auth/oidc/federate`.
3. Janus reads the token's `iss` **only to select which trusted issuer's
   verifier to use**, then verifies the token with it: JWKS signature, `iss`,
   `exp`, and `aud` **exactly equal** to that issuer's configured audience (a
   token minted for another audience is rejected). A forged `iss` can at most
   pick a verifier whose JWKS will reject the signature; an issuer that is not
   in the trusted set is denied outright.
4. Janus projects the verified claims to a flat string map — nested objects
   become dotted paths, e.g. `kubernetes.io.serviceaccount.name` — and finds the
   **single** enabled binding **pinned to that issuer** whose every
   `match_claims` entry equals the token's claim. Zero matches → denied; more
   than one → denied as ambiguous.
5. Janus mints a service token for the matched binding's scope/access with the
   binding's `ttl_seconds` (capped at 1h **when the binding was created**, not at
   mint time — see Safety rules below), and returns it.

Every failure — not configured, untrusted issuer, bad
signature/issuer/audience/expiry, ambiguous claim projection, no match,
ambiguous match — returns one indistinguishable `federation_denied` (401); the
server-side audit records the real reason (`not_configured`,
`issuer_untrusted`, `verify_failed`, `ambiguous_claims`, `no_match`,
`ambiguous_match`).

## Endpoints

### Exchange (public, under `/v1/auth`, behind `RequireUnsealed`, rate-limited)

| Method & path | Behavior |
|---|---|
| `POST /v1/auth/oidc/federate` | Body `{"token":"<oidc-jwt>"}`. On success `200` with `{"token":"janus_svc_…","expires_at":"<rfc3339>","scope":{"kind":…,"id":…,"access":…}}`. Any failure → `401 federation_denied`. |

Because it sits behind `RequireUnsealed`, the endpoint returns **503** while the
server is sealed (minting needs the unsealed token-HMAC key).

### Admin config (under `/v1/sys`, gated by the `oidc:manage` instance action)

`oidc:manage` is held by **admin** and **owner** (the same action that gates C1's
`/v1/sys/oidc`); denials are audited fail-closed.

| Method & path | Behavior |
|---|---|
| `GET /v1/sys/oidc/federation/issuers` | **List every trusted issuer** (`id, issuer, audience, preset, enabled`). |
| `POST /v1/sys/oidc/federation/issuers` | **Add or update ONE trusted issuer**, leaving the others alone. Audited `oidc.federation.issuer.write`. |
| `DELETE /v1/sys/oidc/federation/issuers/{id}` | Stop trusting one issuer (204). Audited `oidc.federation.issuer.delete`. |
| `GET /v1/sys/oidc/federation` | Legacy single-issuer read: the oldest trusted issuer. |
| `PUT /v1/sys/oidc/federation` | Legacy: replaces the whole set with this one issuer. `409` when several are trusted. Audited `oidc.federation.config.write`. |
| `DELETE /v1/sys/oidc/federation` | Remove **every** trusted issuer (204). Audited. |
| `GET /v1/sys/oidc/federation/bindings` | List bindings. |
| `POST /v1/sys/oidc/federation/bindings` | Create a binding. Audited `oidc.federation.binding.write`. |
| `DELETE /v1/sys/oidc/federation/bindings/{id}` | Remove a binding (204). Audited. |

The federation config holds **no secret** — federation is a public-key / JWKS
trust relationship, so there is nothing to wrap.

## Trusted issuers

`POST /v1/sys/oidc/federation/issuers`:

| Field | Notes |
|---|---|
| `issuer` | OIDC issuer; discovery + JWKS resolved from it. Must be a well-formed absolute `http(s)` URL. Required (no implicit default on this endpoint). Trailing slashes are trimmed. |
| `audience` | Required, non-empty. The token's `aud` must equal this exactly (a one-element `aud` array, as Kubernetes emits, counts as that value). |
| `preset` | `github`, `gitlab`, `buildkite`, `circleci`, `kubernetes`, `custom`, or empty. Selects the provider-aware required-claim rule for bindings on this issuer. |
| `enabled` | Whether the exchange is live for this issuer. Disabling one leaves the others working. |

Re-posting the same `issuer` updates that entry in place; posting a different
one adds it. Removing an issuer leaves its bindings in place but **inert** — they
can never match again unless that exact issuer is trusted anew.

Known issuer URLs:

| Provider | Issuer |
|---|---|
| GitHub Actions | `https://token.actions.githubusercontent.com` |
| GitLab CI/CD (SaaS) | `https://gitlab.com` (self-hosted: your instance base URL) |
| Buildkite | `https://agent.buildkite.com` |
| CircleCI | `https://oidc.circleci.com/org/<ORG_ID>` (org-specific) |
| Kubernetes | cluster-specific — see [Kubernetes service accounts](#kubernetes-service-accounts) |

Kubernetes cluster issuers cannot be recognised from their URL (every cluster
differs), which is why the **preset** is explicit rather than sniffed.

The web UI (Integrations → Machine identity federation) offers these as a
**provider preset** dropdown that fills the issuer URL where it is fixed and
names the claims to bind, so admins don't hand-type issuer URLs.

## Trust bindings

Each binding maps a set of claim conditions to a scope + access + TTL. Fields:

| Field | Notes |
|---|---|
| `name` | Unique label. |
| `issuer` | The trusted issuer this binding is pinned to. Optional while exactly one issuer is trusted (it resolves to that one); **required once several are**, and it must be one of them. Tokens from any other issuer can never satisfy this binding. |
| `match_claims` | JSON object `{claim: value}`; **all** entries must match the token's claims (exact string equality, AND-ed). Nested claims are addressed by dotted path (see [Nested claims](#nested-claims)). **Must constrain at least one strong identifying claim for the binding's issuer** (see below). |
| `scope_kind` | `config` or `environment`. |
| `scope_id` | The target config/environment id (must exist). |
| `access` | `read` or `readwrite`. |
| `ttl_seconds` | Minted-token lifetime; `1 ≤ ttl ≤ 3600`. Omitting it (0) defaults to 900 (15m). |
| `enabled` | Whether the binding can mint. |

#### Provider-aware required claim

The old rule required a literal `repository` claim, which only fits GitHub. The
requirement is **provider-aware**: a binding must constrain at least one
**strong identifying claim** appropriate to its issuer's preset. Which claim
counts as strong:

| Preset (issuer) | Required strong claim | Recommended extra narrowing |
|---|---|---|
| `github` — `https://token.actions.githubusercontent.com` | `repository` | `environment`, `ref` |
| `gitlab` — `https://gitlab.com` | `project_path` | `ref`, `ref_type`, `environment` |
| `buildkite` — `https://agent.buildkite.com` | `organization_slug` | `pipeline_slug`, `build_branch` |
| `circleci` — `https://oidc.circleci.com/org/<ORG_ID>` | `oidc.circleci.com/project-id` (or `aud`) | project/context claims |
| `kubernetes` — cluster issuer | `sub` **or** (`kubernetes.io.namespace` **and** `kubernetes.io.serviceaccount.name`) | `kubernetes.io.pod.name` is *not* identity — pods are fungible |
| `custom` / empty preset on an unknown issuer | *any single non-empty match claim* | — |

For a Kubernetes issuer, pinning only `aud` is **rejected**: every workload in
the cluster can request a token for that audience, so the audience alone
identifies nothing. For an unknown or self-hosted issuer with no preset, the rule
falls back to "at least one non-empty match claim is required" — a claim-less
binding is always rejected.

#### Nested claims

Kubernetes puts service-account identity in a nested object:

```json
{"sub":"system:serviceaccount:prod:atlas-api",
 "kubernetes.io":{"namespace":"prod",
                  "serviceaccount":{"name":"atlas-api","uid":"…"},
                  "pod":{"name":"atlas-api-7c9","uid":"…"}}}
```

Janus flattens nested objects to **dotted paths**, so a binding pins
`kubernetes.io.namespace` and `kubernetes.io.serviceaccount.name` directly.
Rules, all of them fail-closed:

- **Non-string values are still dropped, never coerced.** Numbers, booleans,
  arrays and `null` are not matchable — a binding pinning `"42"` can never be
  satisfied by the number `42`. The one exception is `aud`, where an array
  holding *exactly one* string is unwrapped to that string (the shape Kubernetes
  emits); a multi-valued `aud` stays unmatchable.
- **Ambiguity is rejected outright.** A literal claim key may itself contain dots
  (CircleCI emits `oidc.circleci.com/project-id`), so `{"a.b":"x"}` and
  `{"a":{"b":"y"}}` would both want the path `a.b`. Rather than pick a winner —
  which whichever side an attacker controls could use to shadow the other — the
  **whole token is denied** (`ambiguous_claims`) whenever two constructions
  produce the same dotted path. The rule is order-independent and applies to
  literal/nested and nested/nested collisions alike.
- Nesting deeper than 6 levels, or more than 512 projected claims, is likewise
  rejected rather than truncated.

### Safety rules (non-negotiable)

- **A binding is pinned to one issuer.** Cross-issuer matching is impossible:
  bindings are filtered by the issuer that actually signed the token before any
  claim comparison happens.
- **A strong identifying claim is required** on every binding (provider-aware,
  per the table above) — an owner-wide or claim-less binding is rejected at
  config time. Empty match-claim *values* are also rejected (they would match
  tokens that lack the claim entirely).
- **Exactly one binding must match.** Zero or multiple → denied. There is no
  "most-specific wins" resolution; ambiguity is an admin error.
- **Audience is required and exact-matched**, per issuer.
- **TTL is capped at 1h**, default 15m; a binding over the cap is rejected at
  config time.

## Security properties

- The raw CI JWT is a bearer credential: it is never logged, echoed, or written
  to an audit entry. A leak test (`TestOIDCFederationJWTNeverLeaks`) drives a
  full exchange and asserts the raw JWT and the minted token appear in no log
  line and no `audit_events` row. Success audits record only `binding`,
  `issuer`, `repository`, and `sub`; denials record only the reason.
- **Issuers are isolated.** The verifier is selected by the token's own `iss`,
  the signature is checked against *that* issuer's JWKS, and go-oidc re-checks
  `iss` against the trusted entry — so a token cannot route itself to a more
  permissive issuer, and bindings for other issuers are never even considered.
  Verifiers are cached per issuer and flushed whenever the trust set changes.
- All exchange failures are indistinguishable to the caller.
- Minted tokens are short-lived and revocable; the TTL bounds blast radius even
  without explicit revocation.
- Federated tokens have no human minter — `service_tokens.created_by` is NULL and
  `federation_binding` records the minting binding (forensics + FK integrity).

## GitHub Actions example

Configure the provider and a binding (as an admin), then in a workflow:

```yaml
permissions:
  id-token: write   # allow the job to request an OIDC token

jobs:
  deploy:
    runs-on: ubuntu-latest
    steps:
      - name: Get a Janus token
        id: janus
        run: |
          # Request a GitHub OIDC token with the audience Janus requires.
          RESP=$(curl -sS \
            "$ACTIONS_ID_TOKEN_REQUEST_URL&audience=janus" \
            -H "Authorization: Bearer $ACTIONS_ID_TOKEN_REQUEST_TOKEN")
          JWT=$(echo "$RESP" | jq -r '.value')

          # Exchange it for a short-lived scoped Janus token.
          TOKEN=$(curl -sS -X POST https://janus.internal/v1/auth/oidc/federate \
            -H 'Content-Type: application/json' \
            -d "{\"token\":\"$JWT\"}" | jq -r '.token')
          echo "::add-mask::$TOKEN"
          echo "token=$TOKEN" >> "$GITHUB_OUTPUT"

      - name: Use it
        env:
          JANUS_TOKEN: ${{ steps.janus.outputs.token }}
        run: janus run -- ./deploy.sh
```

The binding for this workflow would be, for example:

```json
{
  "name": "prod-deploy",
  "match_claims": { "repository": "org/app", "environment": "prod" },
  "scope_kind": "config",
  "scope_id": "<prod config id>",
  "access": "read",
  "ttl_seconds": 900,
  "enabled": true
}
```

## GitLab CI/CD example

- **Issuer:** `https://gitlab.com` (or your self-hosted GitLab base URL).
- **Audience:** whatever Janus is configured to require (e.g. `janus`).
- **Bind:** `project_path` (GitLab's `org/group/project`), optionally narrowed by
  `ref` / `ref_type` / `environment`.

GitLab injects an ID token when you declare it in `id_tokens`:

```yaml
deploy:
  id_tokens:
    JANUS_JWT:
      aud: janus            # must equal Janus's configured audience
  script:
    - >
      TOKEN=$(curl -sS -X POST https://janus.internal/v1/auth/oidc/federate
      -H 'Content-Type: application/json'
      -d "{\"token\":\"$JANUS_JWT\"}" | jq -r '.token')
    - JANUS_TOKEN="$TOKEN" janus run -- ./deploy.sh
```

Binding:

```json
{
  "name": "gitlab-prod",
  "match_claims": { "project_path": "acme/atlas-api", "ref": "main" },
  "scope_kind": "config", "scope_id": "<prod config id>",
  "access": "read", "ttl_seconds": 900, "enabled": true
}
```

## Buildkite example

- **Issuer:** `https://agent.buildkite.com`.
- **Audience:** Janus's configured audience.
- **Bind:** `organization_slug`, and recommend also binding `pipeline_slug`.

Buildkite mints an OIDC token via `buildkite-agent`:

```yaml
steps:
  - command: |
      JWT=$(buildkite-agent oidc request-token --audience janus)
      TOKEN=$(curl -sS -X POST https://janus.internal/v1/auth/oidc/federate \
        -H 'Content-Type: application/json' \
        -d "{\"token\":\"$JWT\"}" | jq -r '.token')
      JANUS_TOKEN="$TOKEN" janus run -- ./deploy.sh
```

Binding:

```json
{
  "name": "buildkite-deploy",
  "match_claims": { "organization_slug": "acme", "pipeline_slug": "atlas-deploy" },
  "scope_kind": "config", "scope_id": "<prod config id>",
  "access": "read", "ttl_seconds": 900, "enabled": true
}
```

## CircleCI example

- **Issuer:** `https://oidc.circleci.com/org/<ORG_ID>` — org-specific; get
  `<ORG_ID>` from Organization Settings.
- **Audience:** by default CircleCI sets `aud` to your organization ID; configure
  Janus's audience to match (or a custom audience if you set one).
- **Bind:** `oidc.circleci.com/project-id` (the project's UUID), or narrow by
  context/VCS claims.

CircleCI exposes the token as `$CIRCLE_OIDC_TOKEN`:

```yaml
jobs:
  deploy:
    steps:
      - run: |
          TOKEN=$(curl -sS -X POST https://janus.internal/v1/auth/oidc/federate \
            -H 'Content-Type: application/json' \
            -d "{\"token\":\"$CIRCLE_OIDC_TOKEN\"}" | jq -r '.token')
          JANUS_TOKEN="$TOKEN" janus run -- ./deploy.sh
```

Binding:

```json
{
  "name": "circleci-deploy",
  "match_claims": { "oidc.circleci.com/project-id": "00000000-0000-0000-0000-000000000000" },
  "scope_kind": "config", "scope_id": "<prod config id>",
  "access": "read", "ttl_seconds": 900, "enabled": true
}
```

## Kubernetes service accounts

A pod can federate with its **projected service account token** — the same
mechanism EKS IRSA and GKE Workload Identity build on — so an in-cluster
workload reads its secrets with a short-lived Janus token and no bootstrap
secret sitting in a `Secret`.

### 1. Find the cluster's OIDC issuer

The issuer is whatever the API server was started with
(`--service-account-issuer`). Ask the cluster:

```sh
kubectl get --raw /.well-known/openid-configuration | jq -r '.issuer, .jwks_uri'
```

Typical values:

| Cluster | Issuer |
|---|---|
| EKS | `https://oidc.eks.<region>.amazonaws.com/id/<CLUSTER_ID>` (also `aws eks describe-cluster --name <c> --query cluster.identity.oidc.issuer`) |
| GKE | `https://container.googleapis.com/v1/projects/<project>/locations/<loc>/clusters/<cluster>` |
| kubeadm / k3s / kind (default) | `https://kubernetes.default.svc` (or `…svc.cluster.local`) |

### 2. Make sure Janus can reach it — this is the real constraint

Janus verifies tokens by fetching the issuer's discovery document and JWKS over
HTTP (through the SSRF-hardened client). **The issuer URL must be resolvable and
reachable from the Janus server, and must serve
`/.well-known/openid-configuration` whose `issuer` field equals the URL you
configured.**

- **EKS and GKE publish a public, anonymously-readable discovery endpoint** — the
  URLs above work from anywhere, including a Janus running outside the cluster.
- **Many self-hosted clusters do NOT.** With the default
  `https://kubernetes.default.svc` issuer, discovery is served only by the API
  server, normally requires authentication (anonymous discovery is off unless
  the `system:service-account-issuer-discovery` role is bound to
  `system:unauthenticated`), and the hostname only resolves *inside* the
  cluster. Options, in order of preference:
  1. Run Janus **in the cluster** so `https://kubernetes.default.svc` resolves,
     and allow anonymous discovery:
     `kubectl create clusterrolebinding oidc-discovery --clusterrole=system:service-account-issuer-discovery --group=system:unauthenticated`.
     Janus must also trust the API server's certificate.
  2. Point the API server at an **external issuer you host**:
     `--service-account-issuer=https://oidc.example.com/my-cluster`, then publish
     the discovery document and the JWKS (`kubectl get --raw /openid/v1/jwks`) as
     static files behind HTTPS (a bucket or any web server). This is the standard
     pattern for federating self-hosted clusters, and what EKS does internally.
  3. If neither is possible, keep a long-lived service token for that cluster —
     Janus will not accept an issuer it cannot verify against.

Add the issuer once it is reachable:

```sh
curl -sS -X POST https://janus.internal/v1/sys/oidc/federation/issuers \
  -H "Authorization: Bearer $ADMIN_TOKEN" -H 'Content-Type: application/json' \
  -d '{"issuer":"https://oidc.eks.eu-west-1.amazonaws.com/id/EXAMPLE",
       "audience":"janus","preset":"kubernetes","enabled":true}'
```

### 3. Request a bound, audience-scoped token in the Pod spec

Do **not** use the legacy `Secret`-backed service account token: it never
expires, is not audience-scoped, and is not bound to the pod. Use a
`serviceAccountToken` projected volume whose `audience` equals the audience
configured for that issuer:

```yaml
apiVersion: v1
kind: Pod
metadata:
  name: atlas-api
  namespace: prod
spec:
  serviceAccountName: atlas-api        # the identity Janus will bind
  containers:
    - name: app
      image: registry.example/atlas-api:1.4.2
      volumeMounts:
        - name: janus-token
          mountPath: /var/run/secrets/janus
          readOnly: true
  volumes:
    - name: janus-token
      projected:
        sources:
          - serviceAccountToken:
              path: token
              audience: janus          # MUST equal the issuer's audience
              expirationSeconds: 3600  # kubelet rotates the file in place
```

The token is bound to the pod and the service account — it stops being valid
when the pod goes away — and a token minted for a different audience (e.g. the
default `https://kubernetes.default.svc`) is rejected by Janus.

Exchange it at startup:

```sh
JWT=$(cat /var/run/secrets/janus/token)
TOKEN=$(curl -sS -X POST https://janus.internal/v1/auth/oidc/federate \
  -H 'Content-Type: application/json' \
  -d "{\"token\":\"$JWT\"}" | jq -r '.token')
JANUS_TOKEN="$TOKEN" janus run -- ./atlas-api
```

Because the kubelet rotates the projected token in place, long-running pods
should re-read the file and re-exchange before their Janus token expires (the
exchange response carries `expires_at`).

### 4. Bind the service-account identity

A Kubernetes projected token carries:

```json
{
  "iss": "https://oidc.eks.eu-west-1.amazonaws.com/id/EXAMPLE",
  "aud": ["janus"],
  "sub": "system:serviceaccount:prod:atlas-api",
  "kubernetes.io": {
    "namespace": "prod",
    "serviceaccount": { "name": "atlas-api", "uid": "…" },
    "pod": { "name": "atlas-api-7c9f8", "uid": "…" }
  }
}
```

Bind namespace + service account (equivalently, pin the exact `sub`):

```json
{
  "name": "atlas-api-prod",
  "issuer": "https://oidc.eks.eu-west-1.amazonaws.com/id/EXAMPLE",
  "match_claims": {
    "kubernetes.io.namespace": "prod",
    "kubernetes.io.serviceaccount.name": "atlas-api"
  },
  "scope_kind": "config", "scope_id": "<prod config id>",
  "access": "read", "ttl_seconds": 900, "enabled": true
}
```

Do **not** bind `kubernetes.io.pod.name`: pod names change on every rollout and
name nothing durable. Binding only `aud` is rejected outright — every workload in
the cluster can ask for a token with that audience.
