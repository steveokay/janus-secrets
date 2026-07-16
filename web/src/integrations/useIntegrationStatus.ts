import { useEffect, useState } from 'react'
import { ApiError } from '../lib/api'
import { endpoints } from '../lib/endpoints'
import { opsEndpoints, type SyncView } from '../operations/endpoints'

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

const isForbidden = (e: unknown) => e instanceof ApiError && e.status === 403

/**
 * Deliberately plain fetch (useEffect + useState) rather than
 * @tanstack/react-query: this hook only needs a one-shot fetch-on-mount with
 * no caching/retry/refetch semantics, and going through react-query's
 * notifyManager adds an extra render/commit cycle per hop that this simple
 * status strip doesn't need.
 */
export function useIntegrationStatus(): IntegrationStatus {
  const [status, setStatus] = useState<IntegrationStatus>({
    githubSync: undefined,
    k8sSync: undefined,
    federation: undefined,
    oidcLogin: undefined,
  })

  useEffect(() => {
    let cancelled = false

    // Cross-project, 403-tolerant fan-out of sync targets: list projects,
    // then list sync targets per project. A forbidden project contributes no
    // rows; if EVERY project is forbidden (or the project list itself is
    // forbidden), the counts go neutral (null) rather than showing "0".
    endpoints
      .listProjects()
      .then(async (projects) => {
        const results = await Promise.all(
          projects.map((p) =>
            opsEndpoints.sync.list(p.id).then(
              (targets): { ok: true; targets: SyncView[] } => ({ ok: true, targets }),
              (err): { ok: false; forbidden: boolean } => ({ ok: false, forbidden: isForbidden(err) }),
            ),
          ),
        )
        if (cancelled) return
        const unexpectedError = results.some((r) => !r.ok && !r.forbidden)
        const allForbidden = projects.length > 0 && results.every((r) => !r.ok && r.forbidden)
        if (unexpectedError || allForbidden) {
          setStatus((s) => ({ ...s, githubSync: null, k8sSync: null }))
          return
        }
        const rows = results.flatMap((r) => (r.ok ? r.targets : []))
        setStatus((s) => ({
          ...s,
          githubSync: rows.filter((t) => t.provider === 'github').length,
          k8sSync: rows.filter((t) => t.provider === 'k8s').length,
        }))
      })
      .catch((err) => {
        if (cancelled) return
        setStatus((s) => ({ ...s, githubSync: null, k8sSync: null }))
        void err
      })

    endpoints
      .getFederationConfig()
      .then((cfg) => {
        if (!cancelled) setStatus((s) => ({ ...s, federation: cfg.enabled }))
      })
      .catch(() => {
        if (!cancelled) setStatus((s) => ({ ...s, federation: null }))
      })

    endpoints
      .oidcLoginStatus()
      .then((st) => {
        if (!cancelled) setStatus((s) => ({ ...s, oidcLogin: st.enabled }))
      })
      .catch(() => {
        if (!cancelled) setStatus((s) => ({ ...s, oidcLogin: null }))
      })

    return () => {
      cancelled = true
    }
  }, [])

  return status
}
