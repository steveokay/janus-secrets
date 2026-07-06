import { FormEvent, useState } from 'react'
import { endpoints } from '../lib/endpoints'
import { ApiError } from '../lib/api'
import { useAuth } from './AuthProvider'
import { Brand } from '../ui/Brand'
import { useTitle } from '../lib/title'

export function LoginPage() {
  useTitle('Sign in')
  const { refresh } = useAuth()
  const [email, setEmail] = useState('')
  const [password, setPassword] = useState('')
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)

  async function submit(e: FormEvent) {
    e.preventDefault()
    setError('')
    setBusy(true)
    try {
      await endpoints.login(email, password)
      await refresh()
    } catch (err) {
      if (err instanceof ApiError && err.status === 429) setError('Too many attempts — wait a moment and try again.')
      else setError('Invalid email or password.')
    } finally {
      setBusy(false)
    }
  }

  return (
    <div className="flex min-h-screen items-center justify-center bg-page px-4">
      <form onSubmit={submit} aria-label="login" className="flex w-[330px] flex-col gap-3 rounded-card border border-line bg-card p-7 shadow-pop">
        <Brand />
        <div>
          <h1 className="text-[17px] font-semibold tracking-tight">Sign in to Janus</h1>
          <p className="text-[12.5px] text-muted">Self-hosted secrets manager</p>
        </div>
        <label className="flex flex-col gap-1 text-[12px] font-semibold">Email
          <input type="email" value={email} onChange={(e) => setEmail(e.target.value)} required
            className="rounded border border-line bg-card px-3 py-2 text-[13px] font-normal" />
        </label>
        <label className="flex flex-col gap-1 text-[12px] font-semibold">Password
          <input type="password" value={password} onChange={(e) => setPassword(e.target.value)} required
            className="rounded border border-line bg-card px-3 py-2 text-[13px] font-normal" />
        </label>
        {error && <p role="alert" className="text-sm text-danger">{error}</p>}
        <button type="submit" disabled={busy}
          className="rounded bg-brand p-2 text-[13px] font-semibold text-white shadow-card disabled:opacity-50">
          {busy ? 'Signing in…' : 'Sign in'}
        </button>
      </form>
    </div>
  )
}
