import { useId } from 'react'
import type { InputHTMLAttributes, ReactNode } from 'react'
import { cn } from './cn'

export const FIELD =
  'w-full rounded border border-line bg-surface-3 px-3 py-1.5 text-[12.5px] text-ink placeholder:text-ink-faint focus:border-brand-line focus:shadow-glow-soft focus-visible:outline-2 focus-visible:outline-brand transition-nocturne'

// Shared label + error wrapper. id ties the label and error text to the control.
export function Field({ id, label, error, children }: {
  id: string; label?: string; error?: string; children: ReactNode
}) {
  return (
    <div className="flex flex-col gap-1">
      {label && <label htmlFor={id} className="text-[12px] font-semibold text-ink">{label}</label>}
      {children}
      {error && <p id={`${id}-err`} className="text-[11.5px] text-danger">{error}</p>}
    </div>
  )
}

export function Input({ label, error, id, className, ...rest }: {
  label?: string; error?: string
} & InputHTMLAttributes<HTMLInputElement>) {
  const auto = useId()
  const fid = id ?? auto
  return (
    <Field id={fid} label={label} error={error}>
      <input
        id={fid}
        {...rest}
        aria-invalid={error ? true : undefined}
        aria-describedby={error ? `${fid}-err` : undefined}
        className={cn(FIELD, className)}
      />
    </Field>
  )
}
