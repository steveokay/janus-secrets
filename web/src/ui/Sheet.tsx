import { ReactNode } from 'react'
import * as D from '@radix-ui/react-dialog'
import { X } from 'lucide-react'

// Right-side slide-over panel (shadcn-lean Radix Dialog variant).
export function Sheet({ open, onOpenChange, title, children }: {
  open: boolean
  onOpenChange: (open: boolean) => void
  title: string
  children: ReactNode
}) {
  return (
    <D.Root open={open} onOpenChange={onOpenChange}>
      <D.Portal>
        <D.Overlay className="fixed inset-0 z-50 bg-ink/30 backdrop-blur-[8px]" />
        <D.Content aria-describedby={undefined} className="fixed inset-y-0 right-0 z-50 w-[380px] max-w-full overflow-y-auto border-l border-line bg-elevated p-5 shadow-pop">
          <div className="mb-4 flex items-center justify-between">
            <D.Title className="text-[15px] font-semibold tracking-tight">{title}</D.Title>
            <D.Close aria-label="close" className="flex h-6 w-6 items-center justify-center rounded text-ink-faint hover:bg-surface-3 hover:text-ink">
              <X size={15} strokeWidth={1.7} />
            </D.Close>
          </div>
          {children}
        </D.Content>
      </D.Portal>
    </D.Root>
  )
}
