import { ReactNode } from 'react'
import { cn } from './cn'

export type Tone = 'success' | 'warning' | 'danger' | 'info' | 'brand' | 'muted'

const tones: Record<Tone, string> = {
  success: 'bg-success-soft text-success',
  warning: 'bg-warning-soft text-warning',
  danger: 'bg-danger-soft text-danger',
  info: 'bg-info-soft text-info',
  brand: 'bg-brand-soft text-brand-text',
  muted: 'bg-line-soft text-muted',
}

export function Pill({ tone, dot = false, className, children }: {
  tone: Tone
  dot?: boolean
  className?: string
  children: ReactNode
}) {
  return (
    <span className={cn('inline-flex items-center gap-1.5 rounded-full px-2.5 py-px text-[11.5px] font-semibold', tones[tone], className)}>
      {dot && <span data-dot className="h-1.5 w-1.5 rounded-full bg-current" />}
      {children}
    </span>
  )
}
