import { useState, type ReactNode } from 'react'
import { AlertTriangle, ChevronUp, ChevronDown } from 'lucide-react'
import { Pill, type Tone } from '../ui/Pill'
import { Tooltip } from '../ui/Tooltip'
import { Skeleton } from '../ui/Skeleton'
import { EmptyState } from '../ui/EmptyState'
import { cn } from '../ui/cn'

export type OpsColumn = { label: string; key: string }
export type OpsSort = { key: string; dir: 'asc' | 'desc' } | null

const STATUS_TONE: Record<string, Tone> = {
  active: 'success',
  paused: 'muted',
  failed: 'danger',
  creating: 'info',
  expired: 'muted',
  revoked: 'muted',
  revoke_failed: 'danger',
}

export function StatusPill({ status }: { status: string }) {
  return <Pill tone={STATUS_TONE[status] ?? 'muted'} dot>{status}</Pill>
}

export function RelTime({ iso }: { iso?: string | null }) {
  if (!iso) return <span className="text-ink-faint">—</span>
  const t = new Date(iso).getTime()
  if (Number.isNaN(t)) return <span className="text-ink-faint">—</span>
  return (
    <Tooltip content={new Date(iso).toLocaleString()}>
      <span className="text-ink-mute">{relative(t)}</span>
    </Tooltip>
  )
}

function relative(t: number): string {
  const diff = t - Date.now()
  const abs = Math.abs(diff)
  const mins = Math.round(abs / 60_000)
  if (mins < 1) return 'just now'
  const unit = mins < 60 ? `${mins}m` : mins < 1440 ? `${Math.round(mins / 60)}h` : `${Math.round(mins / 1440)}d`
  return diff >= 0 ? `in ${unit}` : `${unit} ago`
}

export function LastError({ text }: { text?: string | null }) {
  const [open, setOpen] = useState(false)
  if (!text) return null
  return (
    <span className="inline-flex flex-col items-start gap-1">
      <button
        type="button"
        aria-label="last error"
        aria-expanded={open}
        onClick={() => setOpen((v) => !v)}
        className="inline-flex items-center gap-1 rounded text-[11px] font-medium text-danger hover:opacity-80 transition-nocturne focus-visible:outline focus-visible:outline-2 focus-visible:outline-brand focus-visible:outline-offset-2"
      >
        <AlertTriangle size={13} />
        Error
      </button>
      {open && (
        <span className="block max-w-[240px] break-words rounded border border-danger-line bg-surface-3 p-2 text-[12px] text-ink-body">
          {text}
        </span>
      )}
    </span>
  )
}

export function OpsTable({
  columns,
  isLoading,
  isError,
  allForbidden,
  isEmpty,
  forbiddenHint,
  someForbidden,
  emptyTitle = 'Nothing here yet',
  emptyHint,
  sort,
  onSort,
  children,
}: {
  columns: Array<string | OpsColumn>
  isLoading: boolean
  isError: boolean
  allForbidden: boolean
  isEmpty: boolean
  forbiddenHint?: string
  someForbidden?: boolean
  emptyTitle?: string
  emptyHint?: string
  sort?: OpsSort
  onSort?: (key: string) => void
  children: ReactNode
}) {
  if (isLoading) {
    return (
      <div className="space-y-2">
        {[0, 1, 2].map((i) => (
          <Skeleton key={i} className="h-9 w-full" />
        ))}
      </div>
    )
  }
  if (allForbidden) return <EmptyState title="Access required" hint={forbiddenHint} />
  if (isError) return <p role="alert" className="text-danger">Couldn't load. Try again shortly.</p>
  if (isEmpty) return <EmptyState title={emptyTitle} hint={emptyHint} />
  return (
    <div className="overflow-x-auto rounded-card border border-line bg-surface-2 shadow-elev-1">
      <table className="w-full min-w-[720px] text-[12.5px]">
        <thead>
          <tr className="border-b border-line bg-surface-1 text-left text-ink-faint">
            {columns.map((c) => {
              if (typeof c === 'string') {
                return <th key={c} className="px-2 py-1.5 font-medium">{c}</th>
              }
              if (!onSort) {
                return <th key={c.key} className="px-2 py-1.5 font-medium">{c.label}</th>
              }
              const active = sort?.key === c.key
              return (
                <th key={c.key} className="px-2 py-1.5 font-medium">
                  <button
                    type="button"
                    aria-label={`sort by ${c.label.toLowerCase()}`}
                    onClick={() => onSort(c.key)}
                    className={cn('inline-flex items-center gap-1 font-medium transition-nocturne',
                      active ? 'text-brand-text' : 'text-ink-faint hover:text-ink-mute')}
                  >
                    {c.label}
                    {active && (sort!.dir === 'asc' ? <ChevronUp size={12} strokeWidth={2} /> : <ChevronDown size={12} strokeWidth={2} />)}
                  </button>
                </th>
              )
            })}
          </tr>
        </thead>
        <tbody>{children}</tbody>
      </table>
      {someForbidden && (
        <p className={cn('px-2 py-2 text-[11px] text-ink-faint')}>Some projects are hidden — you don't manage this here.</p>
      )}
    </div>
  )
}
