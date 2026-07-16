import { useQuery } from '@tanstack/react-query'
import { useSync } from '../operations/useAggregated'
import { endpoints } from '../lib/endpoints'

/**
 * Per-field status for the integrations catalog. Tri-state values:
 *   undefined → still loading
 *   null      → neutral (403 no-permission, 404 not-configured, or error)
 *   value     → real count / enabled flag
 */
export interface IntegrationStatus {
  githubSync: number | null | undefined
  k8sSync: number | null | undefined
  federation: boolean | null | undefined
  oidcLogin: boolean | null | undefined
}

export function useIntegrationStatus(): IntegrationStatus {
  const sync = useSync('all')
  const fed = useQuery({ queryKey: ['integrations', 'federation'], queryFn: endpoints.getFederationConfig, retry: false })
  const oidc = useQuery({ queryKey: ['integrations', 'oidc-login'], queryFn: endpoints.oidcLoginStatus, retry: false })

  // Neutralise sync when a non-403 error occurred, or the user is forbidden on
  // every project (someForbidden with zero visible rows) — showing "0" would be
  // misleading. A partial view (some rows visible) shows the real count.
  const syncNeutral = sync.isError || (sync.someForbidden && sync.rows.length === 0)
  const count = (p: 'github' | 'k8s') => sync.rows.filter((r) => r.data.provider === p).length

  return {
    githubSync: sync.isLoading ? undefined : syncNeutral ? null : count('github'),
    k8sSync: sync.isLoading ? undefined : syncNeutral ? null : count('k8s'),
    federation: fed.isLoading ? undefined : fed.data ? fed.data.enabled : null,
    oidcLogin: oidc.isLoading ? undefined : oidc.data ? oidc.data.enabled : null,
  }
}
