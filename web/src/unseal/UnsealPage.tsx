import { FormEvent, useEffect, useState } from 'react'
import { endpoints, SealStatus } from '../lib/endpoints'
import { AuthCard } from '../auth/AuthCard'
import { Input } from '../ui/Input'
import { Button } from '../ui/Button'
import { Pill } from '../ui/Pill'
import { cn } from '../ui/cn'
import { useTitle } from '../lib/title'

// UnsealPage drives a sealed server to unsealed. Shares live only in local state
// and are cleared immediately after each submit — never persisted or logged.
export function UnsealPage({ onUnsealed }: { onUnsealed: () => void }) {
  useTitle('Unseal')
  const [status, setStatus] = useState<SealStatus | null>(null)
  const [share, setShare] = useState('')
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)

  function apply(s: SealStatus) {
    setStatus(s)
    if (!s.sealed) onUnsealed()
  }
  useEffect(() => { endpoints.sealStatus().then(apply).catch(() => setError('Could not read seal status.')) }, [])
  // KMS servers auto-unseal; poll until unsealed.
  useEffect(() => {
    if (status?.type !== 'awskms' || !status.sealed) return
    const t = setInterval(() => endpoints.sealStatus().then(apply).catch(() => {}), 1500)
    return () => clearInterval(t)
  }, [status?.type, status?.sealed])

  async function submitShare(e: FormEvent) {
    e.preventDefault()
    setError('')
    setBusy(true)
    const s = share
    setShare('') // clear before the await; never keep the share around
    try {
      apply(await endpoints.unsealShare(s))
    } catch {
      setError('That share was rejected.')
    } finally {
      setBusy(false)
    }
  }
  async function reset() {
    setError('')
    try { setStatus(await endpoints.unsealReset()) } catch { setError('Reset failed.') }
  }

  if (!status) return <p className="mt-24 text-center text-ink-mute">Loading…</p>
  if (status.type === 'awskms')
    return <p className="mt-24 text-center text-ink-mute">Waiting for KMS auto-unseal…</p>

  return (
    <AuthCard>
      <Pill tone="danger" dot>Sealed</Pill>
      <h1 className="mt-3 text-[17px] font-semibold tracking-tight text-ink">Unseal Janus</h1>
      <p className="text-[12.5px] text-ink-mute">
        {status.progress?.submitted ?? 0} of {status.threshold} shares submitted
      </p>
      <div className="my-4 flex gap-1.5" aria-label={`Share progress: ${status.progress?.submitted ?? 0} of ${status.threshold}`}>
        {Array.from({ length: status.threshold ?? 0 }, (_, i) => (
          <span key={i} className={cn('h-1.5 flex-1 rounded-full', i < (status.progress?.submitted ?? 0) ? 'bg-success' : 'bg-line')} />
        ))}
      </div>
      <form onSubmit={submitShare} className="flex flex-col gap-3 text-left">
        <Input label="Key share" type="password" autoComplete="off" value={share}
          onChange={(e) => setShare(e.target.value)} required className="font-mono" />
        {error && <p role="alert" className="text-center text-[12.5px] text-danger">{error}</p>}
        <div className="flex gap-2">
          <Button type="submit" loading={busy} className="flex-1">Submit share</Button>
          <Button type="button" variant="secondary" onClick={reset}>Reset</Button>
        </div>
        <p className="text-[11.5px] text-ink-faint">Shares are held in memory only and never logged.</p>
      </form>
    </AuthCard>
  )
}
