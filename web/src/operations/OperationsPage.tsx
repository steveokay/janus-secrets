import { useState } from 'react'
import { useSearchParams } from 'react-router-dom'
import { useQuery } from '@tanstack/react-query'
import { endpoints } from '../lib/endpoints'
import { Select } from '../ui/Select'
import { cn } from '../ui/cn'
import { RotationPanel } from './RotationPanel'
import { SyncPanel } from './SyncPanel'
import { DynamicPanel } from './DynamicPanel'
import type { ProjectFilter } from './useAggregated'

const TABS = [
  { id: 'rotation', label: 'Rotation' },
  { id: 'sync', label: 'Sync' },
  { id: 'dynamic', label: 'Dynamic' },
] as const

type TabId = (typeof TABS)[number]['id']

export function OperationsPage() {
  const [params, setParams] = useSearchParams()
  const raw = params.get('tab')
  const tab: TabId = TABS.some((t) => t.id === raw) ? (raw as TabId) : 'rotation'
  const [filter, setFilter] = useState<ProjectFilter>('all')
  const projectsQ = useQuery({ queryKey: ['projects'], queryFn: endpoints.listProjects })

  return (
    <div className="mx-auto max-w-6xl px-6 py-6">
      <header className="mb-4">
        <h1 className="text-lg font-semibold text-ink">Operations</h1>
        <p className="text-[12.5px] text-ink-mute">Rotation, sync, and dynamic credentials across your projects.</p>
      </header>

      <div className="mb-3 flex items-center justify-between gap-3">
        <div role="tablist" aria-label="Operations engines" className="flex gap-1">
          {TABS.map((t) => (
            <button
              key={t.id}
              role="tab"
              aria-selected={tab === t.id}
              className={cn(
                'rounded px-3 py-1.5 text-[12.5px]',
                tab === t.id ? 'bg-brand-soft text-brand-text' : 'text-ink-mute hover:bg-line-soft',
              )}
              onClick={() => setParams((p) => { p.set('tab', t.id); return p }, { replace: true })}
            >
              {t.label}
            </button>
          ))}
        </div>
        <div className="w-56">
          <Select aria-label="Project filter" value={filter} onChange={(e) => setFilter(e.target.value)}>
            <option value="all">All projects</option>
            {(projectsQ.data ?? []).map((p) => (
              <option key={p.id} value={p.id}>{p.name}</option>
            ))}
          </Select>
        </div>
      </div>

      <div role="tabpanel">
        {tab === 'rotation' && <RotationPanel filter={filter} />}
        {tab === 'sync' && <SyncPanel filter={filter} />}
        {tab === 'dynamic' && <DynamicPanel filter={filter} />}
      </div>
    </div>
  )
}
