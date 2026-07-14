import { AlertTriangle } from 'lucide-react'
import { Skeleton } from '../ui/Skeleton'
import { cn } from '../ui/cn'
import { useRotation, useSync, useDynamicRoles, type ProjectFilter } from './useAggregated'

type TabId = 'rotation' | 'sync' | 'dynamic'

export function HealthStrip({ filter, onGo }: { filter: ProjectFilter; onGo: (tab: TabId) => void }) {
  const rot = useRotation(filter)
  const syn = useSync(filter)
  const dyn = useDynamicRoles(filter)
  const loading = rot.isLoading || syn.isLoading || dyn.isLoading
  if (loading) {
    return (
      <div className="mb-3 flex gap-2">
        {[0, 1, 2].map((i) => (
          <Skeleton key={i} className="h-12 flex-1" />
        ))}
      </div>
    )
  }
  const count = (rows: { data: { status: string } }[], s: string) => rows.filter((r) => r.data.status === s).length
  return (
    <div className="mb-3 grid grid-cols-1 gap-2 sm:grid-cols-3">
      <StatusSegment
        label="Rotation"
        onClick={() => onGo('rotation')}
        active={count(rot.rows, 'active')}
        failing={count(rot.rows, 'failed')}
        paused={count(rot.rows, 'paused')}
      />
      <StatusSegment
        label="Sync"
        onClick={() => onGo('sync')}
        active={count(syn.rows, 'active')}
        failing={count(syn.rows, 'failed')}
        paused={count(syn.rows, 'paused')}
      />
      <RolesSegment label="Dynamic" onClick={() => onGo('dynamic')} roles={dyn.rows.length} />
    </div>
  )
}

const CARD =
  'rounded-card border border-line bg-surface-2 px-3 py-2 text-left hover:bg-row-hover transition-nocturne focus-visible:outline focus-visible:outline-2 focus-visible:outline-brand focus-visible:outline-offset-2'

function StatusSegment({
  label,
  active,
  failing,
  paused,
  onClick,
}: {
  label: string
  active: number
  failing: number
  paused: number
  onClick: () => void
}) {
  return (
    <button type="button" aria-label={`${label}: ${failing} failing`} onClick={onClick} className={CARD}>
      <div className="text-[11px] text-ink-mute">{label}</div>
      <div className="mt-0.5 text-[12.5px] text-ink-body">
        {active} active <span className="text-ink-faint">·</span>{' '}
        <span className={cn('inline-flex items-center gap-1', failing > 0 ? 'text-danger' : 'text-ink-mute')}>
          {failing > 0 && <AlertTriangle size={12} />}
          {failing} failing
        </span>{' '}
        <span className="text-ink-faint">·</span> {paused} paused
      </div>
    </button>
  )
}

function RolesSegment({ label, roles, onClick }: { label: string; roles: number; onClick: () => void }) {
  return (
    <button type="button" aria-label={`${label}: ${roles} roles`} onClick={onClick} className={CARD}>
      <div className="text-[11px] text-ink-mute">{label}</div>
      <div className="mt-0.5 text-[12.5px] text-ink-mute">
        {roles} role{roles === 1 ? '' : 's'}
      </div>
    </button>
  )
}
