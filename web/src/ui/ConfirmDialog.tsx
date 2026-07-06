import { ReactNode } from 'react'
import * as AD from '@radix-ui/react-alert-dialog'
import { cn } from './cn'

export function ConfirmDialog({ open, onOpenChange, title, body, confirmLabel, tone = 'brand', onConfirm }: {
  open: boolean
  onOpenChange: (open: boolean) => void
  title: string
  body: ReactNode
  confirmLabel: string
  tone?: 'brand' | 'danger'
  onConfirm: () => void
}) {
  return (
    <AD.Root open={open} onOpenChange={onOpenChange}>
      <AD.Portal>
        <AD.Overlay className="fixed inset-0 z-50 bg-ink/30" />
        <AD.Content className="fixed left-1/2 top-1/2 z-50 w-80 -translate-x-1/2 -translate-y-1/2 rounded-card border border-line bg-card p-5 shadow-pop">
          <AD.Title className="mb-2 text-[15px] font-semibold tracking-tight">{title}</AD.Title>
          <AD.Description className="mb-4 text-[12.5px] text-muted">{body}</AD.Description>
          <div className="flex justify-end gap-2">
            <AD.Cancel className="rounded border border-line bg-card px-3 py-1.5 text-[13px] font-semibold">Cancel</AD.Cancel>
            <AD.Action
              onClick={onConfirm}
              className={cn('rounded px-3 py-1.5 text-[13px] font-semibold text-white', tone === 'danger' ? 'bg-danger' : 'bg-brand')}
            >
              {confirmLabel}
            </AD.Action>
          </div>
        </AD.Content>
      </AD.Portal>
    </AD.Root>
  )
}
