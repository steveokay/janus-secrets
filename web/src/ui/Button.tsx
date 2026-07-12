import type { ButtonHTMLAttributes, ReactNode } from 'react'
import { Loader2 } from 'lucide-react'
import { cn } from './cn'

type Variant = 'primary' | 'secondary' | 'ghost' | 'danger'
const variants: Record<Variant, string> = {
  primary: 'bg-brand-grad text-ink-hi shadow-glow-soft hover:shadow-glow transition-nocturne',
  secondary: 'bg-surface-3 text-ink border border-line hover:bg-surface-3 hover:border-brand-line transition-nocturne',
  ghost: 'bg-transparent text-ink-mute hover:bg-brand-soft hover:text-brand-text transition-nocturne',
  danger: 'bg-danger-soft text-danger border border-line hover:bg-danger-soft transition-nocturne',
}

export function Button({
  variant = 'primary', size = 'md', block = false, loading = false,
  className, disabled, children, ...rest
}: {
  variant?: Variant
  size?: 'md' | 'sm'
  block?: boolean
  loading?: boolean
  children?: ReactNode
} & ButtonHTMLAttributes<HTMLButtonElement>) {
  return (
    <button
      {...rest}
      disabled={disabled || loading}
      className={cn(
        'inline-flex items-center gap-[7px] rounded font-semibold text-[13px] px-3.5 py-2',
        'focus-visible:outline focus-visible:outline-2 focus-visible:outline-brand focus-visible:outline-offset-2',
        variants[variant],
        size === 'sm' && 'text-[12px] px-2.5 py-1.5',
        block && 'w-full justify-center py-2.5',
        (disabled || loading) && 'opacity-40 cursor-not-allowed',
        className,
      )}
    >
      {loading && <Loader2 size={14} strokeWidth={1.8} className="animate-spin" />}
      {children}
    </button>
  )
}
