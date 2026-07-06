import { FormEvent, useEffect, useState } from 'react'
import { endpoints, SealStatus } from '../lib/endpoints'
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

  if (!status) return <p className="mt-24 text-center text-muted">Loading…</p>
  if (status.type === 'awskms')
    return <p className="mt-24 text-center text-muted">Waiting for KMS auto-unseal…</p>

  return (
    <div className="flex min-h-screen items-center justify-center bg-page px-4">
      <form onSubmit={submitShare} className="flex w-[330px] flex-col gap-3 rounded-card border border-line bg-card p-7 shadow-pop">
        <Pill tone="danger" dot className="self-start">Sealed</Pill>
        <div>
          <h1 className="text-[17px] font-semibold tracking-tight">Unseal Janus</h1>
          <p className="text-[12.5px] text-muted">
            {status.progress?.submitted ?? 0} of {status.threshold} shares submitted
          </p>
        </div>
        <div className="flex gap-1.5" aria-hidden>
          {Array.from({ length: status.threshold ?? 0 }, (_, i) => (
            <span key={i} className={cn('h-1.5 flex-1 rounded-full', i < (status.progress?.submitted ?? 0) ? 'bg-brand' : 'bg-line-soft')} />
          ))}
        </div>
        <label className="flex flex-col gap-1 text-[12px] font-semibold">Unseal key share
          <input type="password" autoComplete="off" value={share} onChange={(e) => setShare(e.target.value)} required
            className="rounded border border-line bg-card px-3 py-2 font-mono text-[13px] font-normal" />
        </label>
        {error && <p role="alert" className="text-sm text-danger">{error}</p>}
        <div className="flex gap-2">
          <button type="submit" disabled={busy}
            className="flex-1 rounded bg-brand p-2 text-[13px] font-semibold text-white shadow-card disabled:opacity-50">
            Submit share
          </button>
          <button type="button" onClick={reset}
            className="rounded border border-line bg-card px-4 py-2 text-[13px] font-semibold">
            Reset
          </button>
        </div>
        <p className="text-[11.5px] text-faint">Shares are held in memory only and never logged.</p>
      </form>
    </div>
  )
}
