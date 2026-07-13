import { useEffect, useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { endpoints, type OIDCProviderView, type OIDCConfigInput } from '../lib/endpoints'
import { ApiError, errorMessage } from '../lib/api'
import { Card } from '../ui/Card'
import { Button } from '../ui/Button'
import { Input } from '../ui/Input'
import { Pill } from '../ui/Pill'
import { EmptyState } from '../ui/EmptyState'
import { ConfirmDialog } from '../ui/ConfirmDialog'
import { useToast } from '../ui/Toast'

interface FormState {
  name: string; issuer: string; client_id: string; redirect_url: string
  scopes: string; enabled: boolean
}

const EMPTY: FormState = {
  name: 'default', issuer: '', client_id: '', redirect_url: '', scopes: 'openid email profile', enabled: true,
}

// Scopes render as a space/comma-separated list and parse back to string[].
function parseScopes(s: string): string[] {
  return s.split(/[\s,]+/).map((x) => x.trim()).filter(Boolean)
}
function formatScopes(scopes: string[]): string {
  return scopes.join(' ')
}

function toForm(v: OIDCProviderView): FormState {
  return {
    name: v.name, issuer: v.issuer, client_id: v.client_id,
    redirect_url: v.redirect_url, scopes: formatScopes(v.scopes), enabled: v.enabled,
  }
}

export function OIDCSection() {
  const toast = useToast()
  const qc = useQueryClient()
  const q = useQuery({ queryKey: ['sys', 'oidc'], queryFn: endpoints.getOIDCConfig, retry: false })

  // Distinguish "unconfigured (404)" from "start configuring" — the empty state
  // shows a Configure button that reveals the empty form.
  const [configuring, setConfiguring] = useState(false)
  const [form, setForm] = useState<FormState>(EMPTY)
  // The client secret is WRITE-ONLY: it starts EMPTY and is NEVER seeded from
  // any fetched value (the GET never returns it). It is sent on every save.
  const [secret, setSecret] = useState('')
  const [confirmDelete, setConfirmDelete] = useState(false)

  // Seed the editable form from the fetched view when it loads/changes. This
  // seeds metadata ONLY — the secret input is deliberately left untouched.
  useEffect(() => {
    if (q.data) setForm(toForm(q.data))
  }, [q.data])

  const save = useMutation({
    mutationFn: (cfg: OIDCConfigInput) => endpoints.setOIDCConfig(cfg),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['sys', 'oidc'] })
      setSecret('')
      setConfiguring(false)
      toast({ title: 'OIDC provider saved.' })
    },
    onError: (e) => toast({ title: errorMessage(e), tone: 'danger' }),
  })

  const del = useMutation({
    mutationFn: () => endpoints.deleteOIDCConfig(),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['sys', 'oidc'] })
      setConfiguring(false)
      setSecret('')
      toast({ title: 'OIDC provider removed.' })
    },
    onError: (e) => toast({ title: errorMessage(e), tone: 'danger' }),
  })

  if (q.isLoading) return <p className="text-[12.5px] text-ink-mute">Loading…</p>

  const err = q.error
  if (err instanceof ApiError && err.status === 403) {
    return (
      <Card className="p-4">
        <h3 className="text-[15px] font-semibold text-ink">OIDC provider</h3>
        <p className="mt-1 text-[12.5px] text-ink-mute">Managing OIDC requires the instance admin role.</p>
      </Card>
    )
  }

  const unconfigured = err instanceof ApiError && err.status === 404
  if (unconfigured && !configuring) {
    return (
      <EmptyState
        title="No OIDC provider configured"
        hint="Add a generic OIDC provider so members can sign in with SSO."
        action={<Button onClick={() => { setForm(EMPTY); setSecret(''); setConfiguring(true) }}>Configure provider</Button>}
      />
    )
  }

  if (err && !unconfigured) {
    return <p className="text-[12.5px] text-ink-mute">Unable to load OIDC configuration.</p>
  }

  const view = q.data
  const secretSet = view?.secret_set ?? false
  // Full-replace contract: the backend REQUIRES client_secret (400 on empty),
  // so Save stays disabled until a non-empty secret is (re-)entered.
  const canSave = secret.trim().length > 0

  function submit() {
    save.mutate({
      name: form.name.trim() || 'default',
      issuer: form.issuer.trim(),
      client_id: form.client_id.trim(),
      client_secret: secret,
      scopes: parseScopes(form.scopes),
      redirect_url: form.redirect_url.trim(),
      enabled: form.enabled,
    })
  }

  return (
    <Card className="p-4">
      <div className="mb-3 flex items-center gap-2">
        <h3 className="text-[15px] font-semibold text-ink">OIDC provider</h3>
        {view && (view.enabled
          ? <Pill tone="success" dot>Enabled</Pill>
          : <Pill tone="muted" dot>Disabled</Pill>)}
      </div>
      <p className="mb-4 text-[12.5px] text-ink-mute">
        Generic OIDC provider used for browser sign-in. All fields are replaced on save.
      </p>

      <div className="flex flex-col gap-3">
        <Input
          label="Name"
          value={form.name}
          onChange={(e) => setForm((f) => ({ ...f, name: e.target.value }))}
          autoComplete="off"
        />
        <Input
          label="Issuer"
          value={form.issuer}
          onChange={(e) => setForm((f) => ({ ...f, issuer: e.target.value }))}
          placeholder="https://accounts.example.com"
          autoComplete="off"
        />
        <Input
          label="Client ID"
          value={form.client_id}
          onChange={(e) => setForm((f) => ({ ...f, client_id: e.target.value }))}
          autoComplete="off"
        />
        <Input
          label="Client secret"
          type="password"
          value={secret}
          onChange={(e) => setSecret(e.target.value)}
          placeholder="Enter the client secret"
          autoComplete="off"
        />
        {secretSet && (
          <p className="-mt-1.5 text-[11.5px] text-ink-mute">
            A client secret is currently set — re-enter to save changes.
          </p>
        )}
        <Input
          label="Redirect URL"
          value={form.redirect_url}
          onChange={(e) => setForm((f) => ({ ...f, redirect_url: e.target.value }))}
          placeholder="https://app.example.com/v1/auth/oidc/callback"
          autoComplete="off"
        />
        <Input
          label="Scopes"
          value={form.scopes}
          onChange={(e) => setForm((f) => ({ ...f, scopes: e.target.value }))}
          placeholder="openid email profile"
          autoComplete="off"
        />
        <label className="flex items-center gap-2 text-[12.5px] font-medium text-ink">
          <input
            type="checkbox"
            checked={form.enabled}
            onChange={(e) => setForm((f) => ({ ...f, enabled: e.target.checked }))}
            className="accent-brand"
          />
          Enable provider
        </label>
      </div>

      <div className="mt-4 flex items-center justify-between gap-2">
        <Button variant="primary" loading={save.isPending} disabled={!canSave} onClick={submit}>Save</Button>
        {view && (
          <Button variant="danger" loading={del.isPending} onClick={() => setConfirmDelete(true)}>Delete provider</Button>
        )}
      </div>

      <ConfirmDialog
        open={confirmDelete}
        onOpenChange={setConfirmDelete}
        title="Delete OIDC provider?"
        body="SSO sign-in will be disabled until a provider is configured again."
        confirmLabel="Delete"
        tone="danger"
        onConfirm={() => del.mutate()}
      />
    </Card>
  )
}
