import { useQueries, useQuery } from '@tanstack/react-query'
import { ApiError } from '../lib/api'
import { endpoints, type Project } from '../lib/endpoints'

export type ProjectFilter = string | 'all'

export interface ConfigInfo {
  configId: string
  configName: string
  envName: string
  projectId: string
  projectName: string
}

const REFETCH_MS = 15_000

/**
 * Enumerates projects → environments → configs to build a
 * config_id → {names} map used to render the Project/Config columns.
 * A 403 on any sub-list just leaves those entries out of the map (rows
 * fall back to a truncated id); it is never surfaced as an error.
 */
export function useProjectConfigMap(filter: ProjectFilter): {
  map: Map<string, ConfigInfo>
  projects: Project[]
  isLoading: boolean
} {
  const projectsQ = useQuery({ queryKey: ['projects'], queryFn: endpoints.listProjects })
  const all = projectsQ.data ?? []
  const projects = filter === 'all' ? all : all.filter((p) => p.id === filter)

  const envQs = useQueries({
    queries: projects.map((p) => ({
      queryKey: ['ops', 'envs', p.id],
      queryFn: () => endpoints.listEnvironments(p.id),
    })),
  })

  const pairs: { p: Project; eid: string; envName: string }[] = []
  projects.forEach((p, i) => {
    for (const e of envQs[i]?.data ?? []) pairs.push({ p, eid: e.id, envName: e.name })
  })

  const cfgQs = useQueries({
    queries: pairs.map(({ p, eid }) => ({
      queryKey: ['ops', 'configs', p.id, eid],
      queryFn: () => endpoints.listConfigs(p.id, eid),
    })),
  })

  const map = new Map<string, ConfigInfo>()
  pairs.forEach(({ p, envName }, i) => {
    for (const c of cfgQs[i]?.data ?? []) {
      map.set(c.id, { configId: c.id, configName: c.name, envName, projectId: p.id, projectName: p.name })
    }
  })

  const isLoading =
    projectsQ.isLoading || envQs.some((q) => q.isLoading) || cfgQs.some((q) => q.isLoading)
  return { map, projects, isLoading }
}

export interface ScopeResult<T> {
  id: string
  data: T[]
}

/**
 * Runs listFn once per scope in parallel. A 403 result is dropped (empty
 * data + someForbidden=true); any other error sets isError. Single shared
 * shape for rotation (scope=project), sync (scope=project), and dynamic
 * roles (scope=config).
 */
export function useFanOut<T>(
  scopes: { id: string }[],
  keyPrefix: readonly unknown[],
  listFn: (id: string) => Promise<T[]>,
): { perScope: ScopeResult<T>[]; isLoading: boolean; isError: boolean; someForbidden: boolean } {
  const qs = useQueries({
    queries: scopes.map((s) => ({
      queryKey: [...keyPrefix, s.id],
      queryFn: () => listFn(s.id),
      refetchInterval: REFETCH_MS,
    })),
  })

  let someForbidden = false
  let isError = false
  const perScope: ScopeResult<T>[] = scopes.map((s, i) => {
    const q = qs[i]
    if (q?.error) {
      if (q.error instanceof ApiError && q.error.status === 403) someForbidden = true
      else isError = true
      return { id: s.id, data: [] }
    }
    return { id: s.id, data: (q?.data ?? []) as T[] }
  })

  const isLoading = qs.some((q) => q?.isLoading)
  return { perScope, isLoading, isError, someForbidden }
}
