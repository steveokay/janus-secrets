import { useQueryClient } from '@tanstack/react-query'
import { useLocation, useNavigate, matchPath } from 'react-router-dom'
import { endpoints } from '../lib/endpoints'
import type { Project, Environment, Config, MaskedSecret } from '../lib/endpoints'
import { useTheme } from '../theme/ThemeProvider'

export type PaletteGroup = 'Projects' | 'Configs' | 'Secrets' | 'Actions'

export interface PaletteItem {
  id: string
  group: PaletteGroup
  label: string
  sublabel?: string
  keywords: string
  to?: string // navigate on select…
  action?: () => void // …or run this (mutually exclusive; action wins)
}

// Downloads the FULL audit log as CSV (no filters). The audit log contains
// metadata only — actor/action/resource paths, never secret values — and the
// export is itself server-side audited. Filtered export stays on the AuditPage.
function downloadAuditCsv() {
  const a = document.createElement('a')
  a.href = endpoints.auditExportUrl({}, 'csv') // {} = no filters → all events
  a.download = 'audit.csv'
  document.body.appendChild(a)
  a.click()
  a.remove()
}

const NAV_ACTIONS: { label: string; to: string; keywords: string }[] = [
  { label: 'Go to Home', to: '/', keywords: 'home dashboard overview' },
  { label: 'Go to Projects', to: '/projects', keywords: 'projects list' },
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
  const navigate = useNavigate()
  const { resolved, setTheme } = useTheme()
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

  // Action commands (DO things rather than navigate). Names-only — no secret data.
  items.push({
    id: 'action:new-project', group: 'Actions', label: 'New project',
    keywords: 'new create project add', action: () => navigate('/projects?new=1'),
  })
  items.push({
    id: 'action:export-audit', group: 'Actions', label: 'Export audit (CSV)',
    keywords: 'export audit csv download events log',
    action: () => downloadAuditCsv(), // all events, CSV; server audits the export
  })
  items.push({
    id: 'action:toggle-theme', group: 'Actions', label: 'Toggle theme',
    keywords: 'theme dark light toggle appearance',
    action: () => setTheme(resolved === 'dark' ? 'light' : 'dark'),
  })

  return items
}
