import { FormEvent, useState } from 'react'
import { endpoints } from '../lib/endpoints'
import { errorMessage } from '../lib/api'
import { Input } from '../ui/Input'
import { Button } from '../ui/Button'

export function ChangePasswordForm({ onDone, onClose }: { onDone: () => void; onClose: () => void }) {
  const [current, setCurrent] = useState('')
  const [next, setNext] = useState('')
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)
  async function submit(e: FormEvent) {
    e.preventDefault()
    setError(''); setBusy(true)
    try { await endpoints.changePassword(current, next); onDone() }
    catch (err) { setError(errorMessage(err, 'Could not change password.')) }
    finally { setBusy(false) }
  }
  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-ink/30 backdrop-blur-[8px]">
      <form onSubmit={submit} className="flex w-80 flex-col gap-2 rounded-card border border-line bg-elevated p-5 shadow-pop">
        <h2 className="mb-1 text-lg font-semibold">Change password</h2>
        <Input label="Current password" type="password" value={current} onChange={(e) => setCurrent(e.target.value)} required />
        <Input label="New password" type="password" value={next} onChange={(e) => setNext(e.target.value)} required />
        {error && <p role="alert" className="text-sm text-danger">{error}</p>}
        <div className="mt-1 flex justify-end gap-2">
          <Button type="button" variant="secondary" onClick={onClose}>Cancel</Button>
          <Button type="submit" loading={busy}>Change password</Button>
        </div>
      </form>
    </div>
  )
}
