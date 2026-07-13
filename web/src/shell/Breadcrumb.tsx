import { Fragment } from 'react'
import { useLocation, matchPath } from 'react-router-dom'
import { useQueries } from '@tanstack/react-query'
import { endpoints } from '../lib/endpoints'
import { useProjects, useEnvironments } from '../secrets/nav'

// Rendered as a sibling of <Routes> (like Sidebar), so route params come from
// matchPath on the location, not useParams.
export function Breadcrumb() {
  const location = useLocation()
  const projectId = matchPath('/projects/:projectId/*', location.pathname)?.params.projectId
  const configId = matchPath('/projects/:projectId/configs/:configId', location.pathname)?.params.configId

  const projects = useProjects()
  const envs = useEnvironments(projectId)
  // Same queryKey shape the Sidebar uses, so these hit the cache after nav.
  const configLists = useQueries({
    queries: (envs.data ?? []).map((e) => ({
      queryKey: ['configs', projectId, e.id],
      queryFn: () => endpoints.listConfigs(projectId!, e.id),
      enabled: !!projectId && !!configId,
    })),
  })

  if (!projectId) return null

  const project = projects.data?.find((p) => p.id === projectId)
  const config = configLists.flatMap((q) => q.data ?? []).find((c) => c.id === configId)
  const env = config && envs.data?.find((e) => e.id === config.environment_id)

  const parts = [
    { key: 'project', label: project?.name, strong: true },
    { key: 'env', label: env?.name, strong: false },
    { key: 'config', label: config?.name, strong: true },
  ].filter((p): p is { key: string; label: string; strong: boolean } => !!p.label)

  return (
    <nav aria-label="breadcrumb" className="flex items-center gap-1.5 text-[13px] text-ink-mute">
      {parts.map((p, i) => (
        <Fragment key={p.key}>
          {i > 0 && <span aria-hidden className="text-line">/</span>}
          <span
            aria-current={i === parts.length - 1 ? 'page' : undefined}
            className={p.strong ? 'font-semibold text-ink' : undefined}
          >
            {p.label}
          </span>
        </Fragment>
      ))}
    </nav>
  )
}
