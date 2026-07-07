import { useId } from 'react'
import type { SelectHTMLAttributes, ReactNode } from 'react'
import { cn } from './cn'
import { Field, FIELD } from './Input'

export function Select({ label, error, id, className, children, ...rest }: {
  label?: string; error?: string; children: ReactNode
} & SelectHTMLAttributes<HTMLSelectElement>) {
  const auto = useId()
  const fid = id ?? auto
  return (
    <Field id={fid} label={label} error={error}>
      <select
        id={fid}
        {...rest}
        aria-invalid={error ? true : undefined}
        aria-describedby={error ? `${fid}-err` : undefined}
        className={cn(FIELD, className)}
      >
        {children}
      </select>
    </Field>
  )
}
