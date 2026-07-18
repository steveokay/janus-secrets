import type { ReactNode } from 'react'
import * as D from '@radix-ui/react-dialog'
import { X } from 'lucide-react'
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
          <D.Close
            aria-label="close"
            className="absolute right-3.5 top-3.5 flex h-6 w-6 items-center justify-center rounded text-ink-faint transition-nocturne hover:bg-surface-3 hover:text-ink"
          >
            <X size={15} strokeWidth={1.7} />
          </D.Close>
          {children}
        </D.Content>
      </D.Portal>
    </D.Root>
  )
}
