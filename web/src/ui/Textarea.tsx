import { useId } from 'react'
import type { TextareaHTMLAttributes } from 'react'
import { cn } from './cn'
import { Field, FIELD } from './Input'

export function Textarea({ label, error, id, className, ...rest }: {
  label?: string; error?: string
} & TextareaHTMLAttributes<HTMLTextAreaElement>) {
  const auto = useId()
  const fid = id ?? auto
  return (
    <Field id={fid} label={label} error={error}>
      <textarea
        id={fid}
        {...rest}
        aria-invalid={error ? true : undefined}
        aria-describedby={error ? `${fid}-err` : undefined}
        className={cn(FIELD, className)}
      />
    </Field>
  )
}
