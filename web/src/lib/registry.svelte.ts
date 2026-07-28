/* Registry store: hydrates the project → environment → config tree plus
   dashboard metrics from the real API after login. */

import { api, type Reads24h, type VerifyResult, type HistBucket, type ApiAuditEvent } from './api'

export type EnvKind = 'dev' | 'staging' | 'prod'

export interface ViewConfig {
  id: string
  name: string
  inheritsFrom: string | null
  createdAt: string
  reads24h: number
  requireApproval: boolean
}
export interface ViewEnv {
  id: string
  slug: string
  name: string
  kind: EnvKind
  /** True when EVERY config in this environment requires four-eyes approval,
   *  including ones created later. Derived from the configs' effective flags,
   *  which the server computes as config OR environment. */
  requireApproval: boolean
  configs: ViewConfig[]
}
export interface ViewProject {
  id: string
  slug: string
  name: string
  /** Advisory owner label; null when unset. Never an authorization input. */
  owner: string | null
  createdAt: string
  lastActivityAt: string | null
  environments: ViewEnv[]
}

export function envKind(slugOrName: string): EnvKind {
  const s = slugOrName.toLowerCase()
  if (s.includes('prod')) return 'prod'
  if (s.includes('stag') || s === 'stg' || s.includes('stage')) return 'staging'
  return 'dev'
}

let projects = $state<ViewProject[]>([])
let metrics = $state<Reads24h | null>(null)
let verify = $state<VerifyResult | null>(null)
let histogram = $state<HistBucket[]>([])
let recentEvents = $state<ApiAuditEvent[]>([])
let denied24h = $state(0)
let loading = $state(false)
let loaded = $state(false)
let error = $state('')

async function loadTree(): Promise<ViewProject[]> {
  const ps = await api.listProjects()
  const readsByConfig = new Map((metrics?.top_configs ?? []).map(c => [c.config_id, c.reads]))
  return Promise.all(
    ps.map(async p => {
      const envs = await api.listEnvironments(p.id)
      const environments = await Promise.all(
        envs.map(async e => {
          const configs = await api.listConfigs(p.id, e.id)
          // Every config in a protected environment carries
          // environment_require_approval, so any one of them reports the
          // environment's own flag. An environment with no configs yet cannot
          // say — it renders unprotected until it has one, which is honest:
          // there is nothing to protect.
          const envProtected = configs.some(c => c.environment_require_approval)
          return {
            id: e.id,
            slug: e.slug,
            name: e.name,
            kind: envKind(e.slug || e.name),
            requireApproval: envProtected,
            configs: configs.map(c => ({
              id: c.id,
              name: c.name,
              inheritsFrom: c.inherits_from,
              createdAt: c.created_at,
              reads24h: readsByConfig.get(c.id) ?? 0,
              requireApproval: c.effective_require_approval ?? c.require_approval ?? false,
            })),
          }
        }),
      )
      const order: EnvKind[] = ['dev', 'staging', 'prod']
      environments.sort((a, b) => order.indexOf(a.kind) - order.indexOf(b.kind))
      return {
        id: p.id,
        slug: p.slug,
        owner: p.owner ?? null,
        name: p.name,
        createdAt: p.created_at ?? '',
        lastActivityAt: p.last_activity_at ?? null,
        environments,
      }
    }),
  )
}

export const registry = {
  get projects() { return projects },
  get metrics() { return metrics },
  get verify() { return verify },
  get histogram() { return histogram },
  get recentEvents() { return recentEvents },
  get denied24h() { return denied24h },
  get loading() { return loading },
  get loaded() { return loaded },
  get error() { return error },

  get totalReads24h() { return metrics?.reads_24h ?? 0 },
  get configCount() {
    return projects.reduce((a, p) => a + p.environments.reduce((b, e) => b + e.configs.length, 0), 0)
  },

  findProject(id: string) {
    return projects.find(p => p.id === id || p.slug === id || p.name === id)
  },
  findConfig(cid: string) {
    for (const p of projects)
      for (const e of p.environments)
        for (const c of e.configs)
          if (c.id === cid) return { project: p, env: e, config: c }
    return null
  },
  configLabel(cid: string): string {
    const hit = this.findConfig(cid)
    return hit ? `${hit.project.name} / ${hit.env.slug} / ${hit.config.name}` : cid
  },

  async hydrate(force = false) {
    if (loading || (loaded && !force)) return
    loading = true
    error = ''
    try {
      // Metrics first so config reads can be joined into the tree.
      metrics = await api.metricsReads24h().catch(() => null)
      const from = new Date(Date.now() - 24 * 3600_000).toISOString()
      const [tree, ver, hist, events] = await Promise.all([
        loadTree(),
        api.verifyAudit().catch(() => null),
        api.auditHistogram('hour', from).catch(() => []),
        api.listAuditEvents({ limit: 12 }).catch(() => ({ events: [], next_cursor: null })),
      ])
      projects = tree
      verify = ver
      histogram = hist
      recentEvents = events.events
      denied24h = hist.reduce((a, b) => a + b.denied, 0)
      loaded = true
    } catch (e) {
      error = 'Failed to load the registry.'
    } finally {
      loading = false
    }
  },

  reset() {
    projects = []
    metrics = null
    verify = null
    histogram = []
    recentEvents = []
    loaded = false
  },
}
