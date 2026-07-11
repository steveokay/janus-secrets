import { Copy } from 'lucide-react'
import { Modal } from '../ui/Modal'
import { Button } from '../ui/Button'
import { useToast } from '../ui/Toast'
import type { IssuedCreds } from './endpoints'

/**
 * Ephemeral display of a freshly-issued dynamic credential. The password
 * exists only in the `creds` prop held by the parent's local state; this
 * component never writes it to any cache or log, and the parent clears it
 * on close. There is no re-open.
 */
export function IssuedCredsModal({ creds, onClose }: { creds: IssuedCreds | null; onClose: () => void }) {
  const toast = useToast()
  if (!creds) return null
  const copy = async (label: string, value: string) => {
    try {
      await navigator.clipboard.writeText(value)
      toast({ title: `${label} copied`, tone: 'success' })
    } catch {
      toast({ title: 'Copy failed', tone: 'danger' })
    }
  }
  return (
    <Modal open onClose={onClose} label="Issued credentials">
      <div className="space-y-3">
        <h2 className="text-sm font-semibold text-ink">Dynamic credentials issued</h2>
        <p className="text-[12px] text-danger">Shown once — copy the password now. It will not be shown again.</p>
        <Row label="Username" value={creds.username} onCopy={() => copy('Username', creds.username)} />
        <Row label="Password" value={creds.password} onCopy={() => copy('Password', creds.password)} mono />
        <p className="text-[11px] text-faint">Expires {new Date(creds.expires_at).toLocaleString()}</p>
        <div className="flex justify-end">
          <Button size="sm" onClick={onClose}>Done</Button>
        </div>
      </div>
    </Modal>
  )
}

function Row({ label, value, onCopy, mono }: { label: string; value: string; onCopy: () => void; mono?: boolean }) {
  return (
    <div className="flex items-center justify-between gap-2 rounded border border-line bg-card px-2 py-1.5">
      <div className="min-w-0">
        <div className="text-[10px] uppercase tracking-wide text-faint">{label}</div>
        <div className={mono ? 'truncate font-mono text-[12.5px] text-ink' : 'truncate text-[12.5px] text-ink'}>{value}</div>
      </div>
      <button type="button" aria-label={`copy ${label}`} className="text-muted hover:text-ink" onClick={onCopy}>
        <Copy size={14} />
      </button>
    </div>
  )
}
