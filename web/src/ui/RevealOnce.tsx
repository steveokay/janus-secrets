import * as D from '@radix-ui/react-dialog'
import { useToast } from './Toast'

// One-time secret display (minted tokens, initial passwords). The secret lives
// only in the caller's state; never cache, log, or render it anywhere else.
export function RevealOnce({ open, onClose, title, secret, hint }: {
  open: boolean
  onClose: () => void
  title: string
  secret: string
  hint: string
}) {
  const toast = useToast()
  return (
    <D.Root open={open} onOpenChange={(o) => { if (!o) onClose() }}>
      <D.Portal>
        <D.Overlay className="fixed inset-0 z-50 bg-ink/30 backdrop-blur-[8px]" />
        <D.Content className="fixed left-1/2 top-1/2 z-50 w-[420px] max-w-[calc(100vw-2rem)] -translate-x-1/2 -translate-y-1/2 rounded-card border border-line bg-elevated p-5 shadow-pop">
          <D.Title className="mb-1 text-[15px] font-semibold tracking-tight">{title}</D.Title>
          <D.Description className="mb-3 text-[12.5px] text-ink-mute">{hint}</D.Description>
          <div className="mb-3 select-all break-all rounded border border-warning/40 bg-warning-soft px-3 py-2 font-mono text-[12.5px]">
            {secret}
          </div>
          <div className="flex justify-end gap-2">
            <button
              type="button"
              onClick={() => {
                // Never place the secret value into a toast title or log.
                const clipboard = navigator.clipboard
                if (!clipboard) {
                  toast({ title: 'Copy failed', tone: 'danger' })
                  return
                }
                clipboard.writeText(secret).then(
                  () => { toast({ title: "Copied — store it now, it won't be shown again" }) },
                  () => { toast({ title: 'Copy failed', tone: 'danger' }) },
                )
              }}
              className="rounded border border-line bg-card px-3 py-1.5 text-[13px] font-semibold"
            >
              Copy
            </button>
            <button type="button" onClick={onClose} className="rounded bg-brand px-3 py-1.5 text-[13px] font-semibold text-white">
              I've stored it
            </button>
          </div>
        </D.Content>
      </D.Portal>
    </D.Root>
  )
}
