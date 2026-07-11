import type { ReactNode } from 'react'
import { AlertTriangle } from 'lucide-react'
import { Pill, type Tone } from '../ui/Pill'
import { Tooltip } from '../ui/Tooltip'
import { Skeleton } from '../ui/Skeleton'
import { EmptyState } from '../ui/EmptyState'
import { cn } from '../ui/cn'

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
  if (!iso) return <span className="text-faint">—</span>
  const t = new Date(iso).getTime()
  if (Number.isNaN(t)) return <span className="text-faint">—</span>
  return (
    <Tooltip content={new Date(iso).toLocaleString()}>
      <span className="text-muted">{relative(t)}</span>
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
  if (!text) return null
  return (
    <Tooltip content={text}>
      <span aria-label="last error" className="inline-flex text-danger">
        <AlertTriangle size={14} />
      </span>
    </Tooltip>
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
  children,
}: {
  columns: string[]
  isLoading: boolean
  isError: boolean
  allForbidden: boolean
  isEmpty: boolean
  forbiddenHint?: string
  someForbidden?: boolean
  emptyTitle?: string
  emptyHint?: string
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
    <div className="overflow-x-auto">
      <table className="w-full min-w-[720px] text-[12.5px]">
        <thead>
          <tr className="border-b border-line text-left text-faint">
            {columns.map((c) => (
              <th key={c} className="px-2 py-1.5 font-medium">{c}</th>
            ))}
          </tr>
        </thead>
        <tbody>{children}</tbody>
      </table>
      {someForbidden && (
        <p className={cn('mt-2 text-[11px] text-faint')}>Some projects are hidden — you don't manage this here.</p>
      )}
    </div>
  )
}
