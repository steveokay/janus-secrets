// CI OIDC federation provider presets. Each preset fills the issuer URL and
// hints the strong identifying claim an admin should bind, so issuer URLs and
// claim keys don't have to be hand-typed. The backend enforces the provider-
// aware required-claim rule; these presets mirror it for the UI.
//
// One CI provider is active at a time (single FederationConfig). CircleCI's
// issuer is org-specific (https://oidc.circleci.com/org/<ORG_ID>) so its preset
// leaves a fill-in placeholder rather than a fixed URL.

export interface FederationProvider {
  id: string
  label: string
  /** Fixed issuer URL, or '' when the admin must supply it (CircleCI). */
  issuer: string
  /** The strong identifying claim key a trust binding should constrain. */
  claimKey: string
  /** Human label for that claim (form field + table header). */
  claimLabel: string
  /** Example value shown as a placeholder. */
  claimExample: string
}

export const federationProviders: FederationProvider[] = [
  {
    id: 'github',
    label: 'GitHub Actions',
    issuer: 'https://token.actions.githubusercontent.com',
    claimKey: 'repository',
    claimLabel: 'Repository',
    claimExample: 'acme/atlas-api',
  },
  {
    id: 'gitlab',
    label: 'GitLab CI/CD',
    issuer: 'https://gitlab.com',
    claimKey: 'project_path',
    claimLabel: 'Project path',
    claimExample: 'acme/atlas-api',
  },
  {
    id: 'buildkite',
    label: 'Buildkite',
    issuer: 'https://agent.buildkite.com',
    claimKey: 'organization_slug',
    claimLabel: 'Organization slug',
    claimExample: 'acme',
  },
  {
    id: 'circleci',
    label: 'CircleCI',
    issuer: '', // https://oidc.circleci.com/org/<ORG_ID> — admin supplies ORG_ID
    claimKey: 'oidc.circleci.com/project-id',
    claimLabel: 'Project ID',
    claimExample: '00000000-0000-0000-0000-000000000000',
  },
  {
    id: 'custom',
    label: 'Custom / self-hosted',
    issuer: '',
    claimKey: 'sub',
    claimLabel: 'Match claim',
    claimExample: 'repo:acme/app:ref:refs/heads/main',
  },
]

const byIssuer: Record<string, FederationProvider> = Object.fromEntries(
  federationProviders.filter(p => p.issuer).map(p => [p.issuer, p]),
)

/** Best-effort provider lookup from a configured issuer URL. */
export function providerForIssuer(issuer: string): FederationProvider {
  const trimmed = (issuer ?? '').replace(/\/+$/, '')
  if (byIssuer[trimmed]) return byIssuer[trimmed]
  if (trimmed.startsWith('https://oidc.circleci.com/org/')) {
    return federationProviders.find(p => p.id === 'circleci')!
  }
  return federationProviders.find(p => p.id === 'custom')!
}

/** The single strong claim value carried by a binding, for display. */
export function bindingClaimSummary(match: Record<string, string>): string {
  const known = federationProviders.map(p => p.claimKey)
  for (const k of known) {
    if (match[k]) return match[k]
  }
  const first = Object.entries(match)[0]
  return first ? `${first[0]}=${first[1]}` : '—'
}
