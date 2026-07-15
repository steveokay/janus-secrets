import { useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { endpoints } from '../lib/endpoints'
import { errorMessage } from '../lib/api'
import { downloadBackup } from '../lib/backup'
import { Card } from '../ui/Card'
import { Button } from '../ui/Button'
import { Input } from '../ui/Input'
import { Pill } from '../ui/Pill'
import { Modal } from '../ui/Modal'
import { useToast } from '../ui/Toast'
import { MasterKeySection } from './MasterKeySection'

// Seal (POST /v1/sys/seal) and backup (GET /v1/sys/backup) have no cheap
// capability probe — unlike OIDC/federation these caps (SysSeal / SysBackup) are
// distinct from OIDCManage — so both actions always render and surface a 403 via
// errorMessage/toast if the caller isn't authorized. `seal-status` is
// unauthenticated, so this section is always visible.
export function InstanceSection() {
  const toast = useToast()
  const status = useQuery({ queryKey: ['sys', 'seal-status'], queryFn: endpoints.sealStatus })
  const [confirmOpen, setConfirmOpen] = useState(false)
  const [phrase, setPhrase] = useState('')
  const [sealing, setSealing] = useState(false)
  const [downloading, setDownloading] = useState(false)

  const s = status.data

  async function seal() {
    setSealing(true)
    try {
      await endpoints.seal()
      // No success toast: the reload-to-UnsealPage below is the feedback and
      // would unmount before any toast could render. The Gate (App.tsx) only
      // flips to UnsealPage on the next 503; a full reload gives an immediate,
      // unambiguous transition after a manual seal.
      window.location.reload()
    } catch (e) {
      toast({ title: errorMessage(e), tone: 'danger' })
    } finally {
      setSealing(false)
    }
  }

  async function backup() {
    setDownloading(true)
    try {
      await downloadBackup()
    } catch (e) {
      toast({ title: errorMessage(e), tone: 'danger' })
    } finally {
      setDownloading(false)
    }
  }

  return (
    <div className="flex flex-col gap-4">
      <Card className="p-4">
        <h3 className="text-[15px] font-semibold text-ink">Seal status</h3>
        <p className="mb-3 text-[12.5px] text-ink-mute">The unseal method and current state of this instance.</p>
        {status.isLoading ? (
          <p className="text-[12.5px] text-ink-mute">Loading…</p>
        ) : s ? (
          <dl className="flex flex-col gap-2.5">
            <div className="flex items-center gap-2">
              <dt className="w-24 text-[12px] text-ink-mute">State</dt>
              <dd>
                {s.sealed
                  ? <Pill tone="danger" dot>Sealed</Pill>
                  : <Pill tone="success" dot>Unsealed</Pill>}
              </dd>
            </div>
            <div className="flex items-center gap-2">
              <dt className="w-24 text-[12px] text-ink-mute">Method</dt>
              <dd className="text-[12.5px] font-medium text-ink">{s.type}</dd>
            </div>
            {s.type === 'shamir' && s.threshold !== undefined && s.shares !== undefined && (
              <div className="flex items-center gap-2">
                <dt className="w-24 text-[12px] text-ink-mute">Shares</dt>
                <dd className="text-[12.5px] font-medium text-ink">{s.threshold} of {s.shares}</dd>
              </div>
            )}
            {/* version omitted: no GET /v1/sys/version yet (ui-redo §4.5) */}
          </dl>
        ) : (
          <p className="text-[12.5px] text-ink-mute">Unable to read seal status.</p>
        )}
      </Card>

      <Card className="p-4">
        <h3 className="text-[15px] font-semibold text-ink">Backup</h3>
        <p className="mb-3 text-[12.5px] text-ink-mute">Encrypted snapshot of all data (no plaintext secrets).</p>
        <Button variant="secondary" loading={downloading} onClick={backup}>Download backup</Button>
      </Card>

      <MasterKeySection />

      <Card className="p-4">
        <h3 className="text-[15px] font-semibold text-ink">Seal instance</h3>
        <p className="mb-3 text-[12.5px] text-ink-mute">
          Sealing takes the instance offline. Secret operations return 503 until it is unsealed again.
        </p>
        <Button variant="danger" onClick={() => { setPhrase(''); setConfirmOpen(true) }}>Seal instance</Button>
      </Card>

      <Modal open={confirmOpen} onClose={() => setConfirmOpen(false)} label="Confirm seal" className="w-[420px]">
        <h3 className="text-[15px] font-semibold text-ink">Seal this instance?</h3>
        <p className="mt-1.5 mb-4 text-[12.5px] text-ink-mute">
          This takes Janus offline until it is unsealed. Type <span className="font-semibold text-ink">SEAL</span> to confirm.
        </p>
        <Input
          label="Type SEAL to confirm"
          value={phrase}
          onChange={(e) => setPhrase(e.target.value)}
          autoComplete="off"
        />
        <div className="mt-4 flex justify-end gap-2">
          <Button variant="secondary" onClick={() => setConfirmOpen(false)}>Cancel</Button>
          <Button variant="danger" loading={sealing} disabled={phrase !== 'SEAL'} onClick={seal}>Seal</Button>
        </div>
      </Modal>
    </div>
  )
}
