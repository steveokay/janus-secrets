import type { ReactNode } from 'react'
import { Link } from 'react-router-dom'
import { Card } from '../ui/Card'
import { Pill } from '../ui/Pill'
import { Skeleton } from '../ui/Skeleton'
import { buttonClasses } from '../ui/Button'

export interface CardAction {
  label: string
  to: string
}

/**
 * Tri-state status renderer:
 *   undefined → loading skeleton
 *   null      → neutral em-dash (no permission / not configured)
 *   number    → count pill
 *   boolean   → enabled/disabled pill
 */
export function StatusLine({ label, value }: { label: string; value: number | boolean | null | undefined }) {
  return (
    <div className="flex items-center justify-between text-[12px]">
      <span className="text-ink-mute">{label}</span>
      {value === undefined ? (
        <Skeleton className="h-4 w-10" />
      ) : value === null ? (
        <span className="text-ink-mute">—</span>
      ) : typeof value === 'number' ? (
        <Pill tone="muted">{value}</Pill>
      ) : (
        <Pill tone={value ? 'success' : 'muted'} dot>
          {value ? 'enabled' : 'disabled'}
        </Pill>
      )}
    </div>
  )
}

export function ConnectorCard({
  icon,
  title,
  description,
  statuses,
  actions,
}: {
  icon: ReactNode
  title: string
  description: string
  statuses: ReactNode
  actions: CardAction[]
}) {
  return (
    <Card className="flex flex-col gap-3 p-4">
      <div className="flex items-center gap-2.5">
        <span className="text-ink-mute">{icon}</span>
        <h3 className="text-[14px] font-semibold text-ink">{title}</h3>
      </div>
      <p className="text-[12.5px] text-ink-mute">{description}</p>
      <div className="flex flex-col gap-1.5">{statuses}</div>
      <div className="mt-auto flex flex-wrap gap-2 pt-1">
        {actions.map((a) => (
          <Link key={a.to + a.label} to={a.to} className={buttonClasses('secondary', 'sm')}>
            {a.label}
          </Link>
        ))}
      </div>
    </Card>
  )
}
