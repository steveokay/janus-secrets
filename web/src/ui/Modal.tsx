import type { ReactNode } from 'react'
import * as D from '@radix-ui/react-dialog'
import { cn } from './cn'

// Accessible modal shell (focus-trap, Esc, aria-modal, restore-focus) via Radix
// Dialog. `label` names the dialog for screen readers (Radix requires a Title).
export function Modal({ open, onClose, label, className, children }: {
  open: boolean
  onClose: () => void
  label: string
  className?: string
  children: ReactNode
}) {
  return (
    <D.Root open={open} onOpenChange={(o) => { if (!o) onClose() }}>
      <D.Portal>
        <D.Overlay className="fixed inset-0 z-50 bg-ink/30 backdrop-blur-[8px]" />
        <D.Content
          aria-modal="true"
          aria-describedby={undefined}
          className={cn(
            'fixed left-1/2 top-1/2 z-50 max-w-[92vw] -translate-x-1/2 -translate-y-1/2',
            'rounded-card border border-line bg-elevated p-5 shadow-pop',
            className,
          )}
        >
          <D.Title className="sr-only">{label}</D.Title>
          {children}
        </D.Content>
      </D.Portal>
    </D.Root>
  )
}
