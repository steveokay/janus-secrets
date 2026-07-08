import { FormEvent, useState } from 'react'
import { endpoints } from '../lib/endpoints'
import { ApiError } from '../lib/api'
import { useAuth } from './AuthProvider'
import { useTitle } from '../lib/title'
import { AuthCard } from './AuthCard'
import { Input } from '../ui/Input'
import { Button } from '../ui/Button'

export function LoginPage() {
  useTitle('Sign in')
  const { refresh } = useAuth()
  const [email, setEmail] = useState('')
  const [password, setPassword] = useState('')
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)

  async function submit(e: FormEvent) {
    e.preventDefault()
    setError(''); setBusy(true)
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
    <AuthCard>
      <form onSubmit={submit} aria-label="login" className="flex flex-col gap-3 text-left">
        <div className="text-center">
          <h1 className="text-[17px] font-semibold tracking-tight text-ink">Sign in to Janus</h1>
          <p className="text-[12.5px] text-muted">Self-hosted secrets manager</p>
        </div>
        <Input label="Email" type="email" value={email} onChange={(e) => setEmail(e.target.value)} required />
        <Input label="Password" type="password" value={password} onChange={(e) => setPassword(e.target.value)} required />
        {error && <p role="alert" className="text-center text-[12.5px] text-danger">{error}</p>}
        <Button type="submit" block loading={busy}>Sign in</Button>
      </form>
    </AuthCard>
  )
}
