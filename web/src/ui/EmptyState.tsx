import { ReactNode } from 'react'
import { cn } from './cn'

// Shared empty-list treatment (spec slice-2 §1). Dumb and presentational —
// callers own the action button and any navigation. `className` lets inline
// consumers (e.g. the secret editor) override the full-page mt-16 default.
export function EmptyState({ icon, title, hint, action, className }: {
  icon?: ReactNode
  title: string
  hint?: string
  action?: ReactNode
  className?: string
}) {
  return (
    <div className={cn('mx-auto mt-16 flex max-w-sm flex-col items-center gap-3 text-center', className)}>
      {icon && (
        <div className="flex h-12 w-12 items-center justify-center rounded-full bg-brand-soft text-brand-text">
          {icon}
        </div>
      )}
      <p className="text-[15px] font-semibold text-ink">{title}</p>
      {hint && <p className="text-[12.5px] text-muted">{hint}</p>}
      {action}
    </div>
  )
}
