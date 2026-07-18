import { Copy, Check } from 'lucide-react'
import { Modal } from '../ui/Modal'
import { Button } from '../ui/Button'
import { useToast } from '../ui/Toast'
import { useCopyFeedback } from '../ui/useCopyFeedback'
import type { IssuedCreds } from './endpoints'

/**
 * Ephemeral display of a freshly-issued dynamic credential. The password
 * exists only in the `creds` prop held by the parent's local state; this
 * component never writes it to any cache or log, and the parent clears it
 * on close. There is no re-open.
 */
export function IssuedCredsModal({ creds, onClose }: { creds: IssuedCreds | null; onClose: () => void }) {
  const toast = useToast()
  const copyFeedback = useCopyFeedback()
  if (!creds) return null
  const copy = async (label: string, value: string) => {
    try {
      await navigator.clipboard.writeText(value)
      toast({ title: `${label} copied`, tone: 'success' })
      copyFeedback.markCopied(label)
    } catch {
      toast({ title: 'Copy failed', tone: 'danger' })
    }
  }
  return (
    <Modal open onClose={onClose} label="Issued credentials">
      <div className="space-y-3">
        <h2 className="text-sm font-semibold text-ink">Dynamic credentials issued</h2>
        <p className="text-[12px] text-danger">Shown once — copy the password now. It will not be shown again.</p>
        <Row label="Username" value={creds.username} copied={copyFeedback.isCopied('Username')} onCopy={() => copy('Username', creds.username)} />
        <Row label="Password" value={creds.password} copied={copyFeedback.isCopied('Password')} onCopy={() => copy('Password', creds.password)} mono />
        <p className="text-[11px] text-ink-faint">Expires {new Date(creds.expires_at).toLocaleString()}</p>
        <div className="flex justify-end">
          <Button size="sm" onClick={onClose}>Done</Button>
        </div>
      </div>
    </Modal>
  )
}

function Row({ label, value, onCopy, mono, copied }: { label: string; value: string; onCopy: () => void; mono?: boolean; copied?: boolean }) {
  return (
    <div className="flex items-center justify-between gap-2 rounded border border-line bg-card px-2 py-1.5">
      <div className="min-w-0">
        <div className="text-[10px] uppercase tracking-wide text-ink-faint">{label}</div>
        <div className={mono ? 'truncate font-mono text-[12.5px] text-ink' : 'truncate text-[12.5px] text-ink'}>{value}</div>
      </div>
      <button type="button" aria-label={copied ? `${label.toLowerCase()} copied` : `copy ${label}`} className="text-ink-mute hover:text-ink" onClick={onCopy}>
        {copied ? <Check size={14} className="text-success" /> : <Copy size={14} />}
      </button>
    </div>
  )
}
