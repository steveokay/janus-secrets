import { FormEvent, useEffect, useState } from 'react'
import { endpoints, SealStatus } from '../lib/endpoints'

// UnsealPage drives a sealed server to unsealed. Shares live only in local state
// and are cleared immediately after each submit — never persisted or logged.
export function UnsealPage({ onUnsealed }: { onUnsealed: () => void }) {
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

  if (!status) return <p className="mt-24 text-center">Loading…</p>
  if (status.type === 'awskms')
    return <p className="mt-24 text-center">Waiting for KMS auto-unseal…</p>

  return (
    <form onSubmit={submitShare} className="mx-auto mt-24 flex w-96 flex-col gap-3">
      <h1 className="text-xl font-semibold">Unseal Janus</h1>
      <p className="text-sm text-gray-500">
        {(status.progress ?? 0)} of {status.threshold} shares submitted
      </p>
      <label className="flex flex-col text-sm">Unseal key share
        <input type="password" autoComplete="off" value={share} onChange={(e) => setShare(e.target.value)} required className="rounded border p-2" />
      </label>
      {error && <p role="alert" className="text-sm text-red-600">{error}</p>}
      <div className="flex gap-2">
        <button type="submit" disabled={busy} className="rounded bg-blue-600 p-2 text-white disabled:opacity-50">Submit share</button>
        <button type="button" onClick={reset} className="rounded border p-2">Reset</button>
      </div>
    </form>
  )
}
