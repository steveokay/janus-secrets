import { FormEvent, useEffect, useState } from 'react'
import { endpoints, OIDCLoginStatus } from '../lib/endpoints'
import { ApiError } from '../lib/api'
import { useAuth } from './AuthProvider'
import { useTitle } from '../lib/title'
import { AuthCard } from './AuthCard'
import { Input } from '../ui/Input'
import { Button, buttonClasses } from '../ui/Button'

export function LoginPage() {
  useTitle('Sign in')
  const { refresh } = useAuth()
  const [email, setEmail] = useState('')
  const [password, setPassword] = useState('')
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)
  const [oidc, setOidc] = useState<OIDCLoginStatus | null>(null)

  // Probe whether an OIDC provider is enabled to gate the SSO button. The status
  // call is unauthenticated; any failure is treated as "disabled" (button hidden).
  useEffect(() => {
    let active = true
    endpoints
      .oidcLoginStatus()
      .then((s) => { if (active) setOidc(s) })
      .catch(() => { if (active) setOidc({ enabled: false }) })
    return () => { active = false }
  }, [])

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
          <p className="text-[12.5px] text-ink-mute">Self-hosted secrets manager</p>
        </div>
        <Input label="Email" type="email" value={email} onChange={(e) => setEmail(e.target.value)} required />
        <Input label="Password" type="password" value={password} onChange={(e) => setPassword(e.target.value)} required />
        {error && <p role="alert" className="text-center text-[12.5px] text-danger">{error}</p>}
        <Button type="submit" block loading={busy}>Sign in</Button>
      </form>
      {oidc?.enabled && (
        <div className="mt-4 flex flex-col gap-3">
          <div className="flex items-center gap-3" aria-hidden="true">
            <span className="h-px flex-1 bg-line" />
            <span className="text-[11px] font-medium uppercase tracking-wide text-ink-mute">or</span>
            <span className="h-px flex-1 bg-line" />
          </div>
          {/* Real full-page navigation: the login flow is a 302 redirect chain to
              the IdP (sets janus_oidc_state cookie), so an anchor — not a fetch. */}
          <a href="/v1/auth/oidc/login" className={buttonClasses('secondary', 'md', 'w-full justify-center py-2.5')}>
            Sign in with {oidc.name ?? 'SSO'}
          </a>
        </div>
      )}
    </AuthCard>
  )
}
