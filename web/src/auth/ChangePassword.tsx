import { FormEvent, useState } from 'react'
import { endpoints } from '../lib/endpoints'
import { ApiError } from '../lib/api'

export function ChangePasswordForm({ onDone, onClose }: { onDone: () => void; onClose: () => void }) {
  const [current, setCurrent] = useState('')
  const [next, setNext] = useState('')
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)
  async function submit(e: FormEvent) {
    e.preventDefault()
    setError(''); setBusy(true)
    try { await endpoints.changePassword(current, next); onDone() }
    catch (err) { setError(err instanceof ApiError ? err.message : 'Could not change password.') }
    finally { setBusy(false) }
  }
  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-ink/30">
      <form onSubmit={submit} className="w-80 rounded-card border border-line bg-card p-5 shadow-pop">
        <h2 className="mb-3 text-lg font-semibold">Change password</h2>
        <label className="mb-2 flex flex-col text-sm">Current password
          <input type="password" value={current} onChange={(e) => setCurrent(e.target.value)} required className="rounded border border-line px-3 py-2 text-[13px] font-normal" />
        </label>
        <label className="mb-2 flex flex-col text-sm">New password
          <input type="password" value={next} onChange={(e) => setNext(e.target.value)} required className="rounded border border-line px-3 py-2 text-[13px] font-normal" />
        </label>
        {error && <p role="alert" className="text-sm text-danger">{error}</p>}
        <div className="flex justify-end gap-2">
          <button type="button" onClick={onClose} className="rounded border border-line bg-card px-3 py-1.5 text-[13px] font-semibold">Cancel</button>
          <button type="submit" disabled={busy} className="rounded bg-brand px-3 py-1.5 text-[13px] font-semibold text-white disabled:opacity-50">Change password</button>
        </div>
      </form>
    </div>
  )
}
