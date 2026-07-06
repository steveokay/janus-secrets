import { ReactNode } from 'react'

// Shared empty-list treatment (spec slice-2 §1). Dumb and presentational —
// callers own the action button and any navigation.
export function EmptyState({ icon, title, hint, action }: {
  icon?: ReactNode
  title: string
  hint?: string
  action?: ReactNode
}) {
  return (
    <div className="mx-auto mt-16 flex max-w-sm flex-col items-center gap-3 text-center">
      {icon && (
        <div className="flex h-12 w-12 items-center justify-center rounded-full bg-brand-soft text-brand-deep">
          {icon}
        </div>
      )}
      <p className="text-[15px] font-semibold text-ink">{title}</p>
      {hint && <p className="text-[12.5px] text-muted">{hint}</p>}
      {action}
    </div>
  )
}
