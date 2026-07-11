import { useQueryClient } from '@tanstack/react-query'
import { useLocation, matchPath } from 'react-router-dom'
import type { Project, Environment, Config, MaskedSecret } from '../lib/endpoints'

export type PaletteGroup = 'Projects' | 'Configs' | 'Secrets' | 'Actions'

export interface PaletteItem {
  id: string
  group: PaletteGroup
  label: string
  sublabel?: string
  keywords: string
  to: string // route to navigate to on select
}

const NAV_ACTIONS: { label: string; to: string; keywords: string }[] = [
  { label: 'Go to Projects', to: '/', keywords: 'projects home' },
  { label: 'Go to Activity', to: '/audit', keywords: 'activity audit log events' },
  { label: 'Go to Members', to: '/members', keywords: 'members users roles team' },
  { label: 'Go to Tokens', to: '/tokens', keywords: 'tokens service api' },
  { label: 'Go to Operations', to: '/operations', keywords: 'operations ops rotation sync dynamic leases credentials' },
  { label: 'Go to Settings', to: '/settings', keywords: 'settings config' },
]

// Builds palette items from ALREADY-CACHED query data only (no fetches). Secret
// entries are KEY NAMES from unaudited masked metadata — never values.
export function usePaletteItems(): PaletteItem[] {
  const qc = useQueryClient()
  const loc = useLocation()
  const pid = matchPath('/projects/:projectId/*', loc.pathname)?.params.projectId
    ?? matchPath('/projects/:projectId', loc.pathname)?.params.projectId

  const items: PaletteItem[] = []

  const projects = qc.getQueryData<Project[]>(['projects']) ?? []
  for (const p of projects) {
    items.push({
      id: `project:${p.id}`, group: 'Projects', label: p.name,
      sublabel: p.slug, keywords: `${p.name} ${p.slug}`, to: `/projects/${p.id}`,
    })
  }

  if (pid) {
    const envs = qc.getQueryData<Environment[]>(['envs', pid]) ?? []
    for (const e of envs) {
      const configs = qc.getQueryData<Config[]>(['configs', pid, e.id]) ?? []
      for (const c of configs) {
        const to = `/projects/${pid}/configs/${c.id}`
        items.push({
          id: `config:${c.id}`, group: 'Configs', label: c.name,
          sublabel: e.name, keywords: `${c.name} ${e.name}`, to,
        })
        const masked = qc.getQueryData<Record<string, MaskedSecret>>(['config', c.id, 'masked'])
        if (masked) {
          for (const key of Object.keys(masked)) {
            items.push({
              id: `secret:${c.id}:${key}`, group: 'Secrets', label: key,
              sublabel: `${e.name} / ${c.name}`, keywords: `${key} ${c.name} ${e.name}`, to,
            })
          }
        }
      }
    }
  }

  for (const a of NAV_ACTIONS) {
    items.push({ id: `action:${a.to}`, group: 'Actions', label: a.label, keywords: a.keywords, to: a.to })
  }

  return items
}
