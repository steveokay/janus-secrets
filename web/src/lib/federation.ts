// Federation provider presets. Each preset fills the issuer URL (when it is
// fixed) and names the claim(s) an admin must bind, so issuer URLs and claim
// keys don't have to be hand-typed. The backend enforces the provider-aware
// required-claim rule; these presets mirror it for the UI.
//
// SEVERAL issuers can be trusted at once (a CI provider AND a Kubernetes
// cluster): a trust binding is pinned to exactly one issuer, and a token signed
// by another issuer can never satisfy it. CircleCI's issuer is org-specific
// (https://oidc.circleci.com/org/<ORG_ID>) and Kubernetes cluster issuers are
// cluster-specific, so those presets leave the URL for the admin to supply.

/** One claim an admin fills in when creating a trust binding. */
export interface ClaimField {
  /** Claim key as matched by the backend; nested JWT claims use a dotted path. */
  key: string
  /** Human label (form field + table header). */
  label: string
  /** Example value shown as a placeholder. */
  example: string
}

export interface FederationProvider {
  id: string
  label: string
  /** Fixed issuer URL, or '' when the admin must supply it. */
  issuer: string
  /** Claims a binding must pin for this provider (all are required). */
  claims: ClaimField[]
  /** Short hint rendered under the issuer field. */
  hint?: string
  /**
   * Short hint rendered under the CA bundle field, for providers whose issuer is
   * commonly served by a private CA. Absent means the provider's issuer is a
   * public endpoint and the system roots are the right answer.
   */
  caHint?: string
}

export const federationProviders: FederationProvider[] = [
  {
    id: 'github',
    label: 'GitHub Actions',
    issuer: 'https://token.actions.githubusercontent.com',
    claims: [{ key: 'repository', label: 'Repository', example: 'acme/atlas-api' }],
  },
  {
    id: 'gitlab',
    label: 'GitLab CI/CD',
    issuer: 'https://gitlab.com',
    claims: [{ key: 'project_path', label: 'Project path', example: 'acme/atlas-api' }],
  },
  {
    id: 'buildkite',
    label: 'Buildkite',
    issuer: 'https://agent.buildkite.com',
    claims: [{ key: 'organization_slug', label: 'Organization slug', example: 'acme' }],
  },
  {
    id: 'circleci',
    label: 'CircleCI',
    issuer: '', // https://oidc.circleci.com/org/<ORG_ID> — admin supplies ORG_ID
    claims: [{
      key: 'oidc.circleci.com/project-id',
      label: 'Project ID',
      example: '00000000-0000-0000-0000-000000000000',
    }],
    hint: 'Organization Settings → Overview gives the ORG_ID for https://oidc.circleci.com/org/<ORG_ID>.',
  },
  {
    id: 'kubernetes',
    label: 'Kubernetes service accounts',
    issuer: '', // cluster-specific: --service-account-issuer / EKS / GKE
    claims: [
      { key: 'kubernetes.io.namespace', label: 'Namespace', example: 'prod' },
      { key: 'kubernetes.io.serviceaccount.name', label: 'Service account', example: 'atlas-api' },
    ],
    hint: 'Cluster issuer from `kubectl get --raw /.well-known/openid-configuration`. Janus must be able to reach it — EKS and GKE publish it, most self-hosted clusters do not.',
    caHint: 'Needed when the issuer is the cluster API server itself (the default https://kubernetes.default.svc), whose certificate is signed by the CLUSTER CA — nothing in the system roots signs it. Get it with `kubectl config view --raw --minify -o jsonpath=\'{.clusters[0].cluster.certificate-authority-data}\' | base64 -d`. EKS and GKE issuers are public: leave this empty.',
  },
  {
    id: 'custom',
    label: 'Custom / self-hosted',
    issuer: '',
    claims: [{ key: 'sub', label: 'Subject', example: 'repo:acme/app:ref:refs/heads/main' }],
    caHint: 'Only needed if the issuer’s certificate is signed by a private CA. Leave empty for a publicly-trusted certificate.',
  },
]

const byId: Record<string, FederationProvider> = Object.fromEntries(
  federationProviders.map(p => [p.id, p]),
)

const byIssuer: Record<string, FederationProvider> = Object.fromEntries(
  federationProviders.filter(p => p.issuer).map(p => [p.issuer, p]),
)

const fallback = byId.custom

/**
 * Provider for a trusted issuer. The stored preset wins (Kubernetes cluster
 * issuers are unrecognisable from their URL alone); otherwise the issuer URL is
 * matched against the known fixed issuers.
 */
export function providerFor(issuer: string, preset?: string): FederationProvider {
  if (preset && byId[preset]) return byId[preset]
  const trimmed = (issuer ?? '').replace(/\/+$/, '')
  if (byIssuer[trimmed]) return byIssuer[trimmed]
  if (trimmed.startsWith('https://oidc.circleci.com/org/')) return byId.circleci
  return fallback
}

/** The identifying claim value(s) carried by a binding, for display. */
export function bindingClaimSummary(match: Record<string, string>): string {
  const known = federationProviders.flatMap(p => p.claims.map(c => c.key))
  const hits = known.filter(k => match[k]).map(k => match[k])
  if (hits.length) return hits.join(' / ')
  const first = Object.entries(match)[0]
  return first ? `${first[0]}=${first[1]}` : '—'
}
