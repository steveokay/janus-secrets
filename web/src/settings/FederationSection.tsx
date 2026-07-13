import { useEffect, useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Plus, Trash2 } from 'lucide-react'
import {
  endpoints,
  type FederationConfigView,
  type FederationBindingView,
  type FederationBindingInput,
} from '../lib/endpoints'
import { ApiError, errorMessage } from '../lib/api'
import { Card } from '../ui/Card'
import { Button } from '../ui/Button'
import { Input, FIELD } from '../ui/Input'
import { Pill } from '../ui/Pill'
import { Sheet } from '../ui/Sheet'
import { EmptyState } from '../ui/EmptyState'
import { ConfirmDialog } from '../ui/ConfirmDialog'
import { useToast } from '../ui/Toast'
import { cn } from '../ui/cn'

// ── Trust provider (config) card ─────────────────────────────────────────────

interface ConfigForm { issuer: string; audience: string; enabled: boolean }
const EMPTY_CONFIG: ConfigForm = { issuer: '', audience: '', enabled: true }

function TrustProviderCard() {
  const toast = useToast()
  const qc = useQueryClient()
  const q = useQuery({ queryKey: ['sys', 'fed'], queryFn: endpoints.getFederationConfig, retry: false })

  const [configuring, setConfiguring] = useState(false)
  const [form, setForm] = useState<ConfigForm>(EMPTY_CONFIG)
  const [confirmDelete, setConfirmDelete] = useState(false)

  useEffect(() => {
    if (q.data) setForm({ issuer: q.data.issuer, audience: q.data.audience, enabled: q.data.enabled })
  }, [q.data])

  const save = useMutation({
    mutationFn: (cfg: FederationConfigView) => endpoints.setFederationConfig(cfg),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['sys', 'fed'] })
      setConfiguring(false)
      toast({ title: 'Trust provider saved.' })
    },
    onError: (e) => toast({ title: errorMessage(e), tone: 'danger' }),
  })

  const del = useMutation({
    mutationFn: () => endpoints.deleteFederationConfig(),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['sys', 'fed'] })
      setConfiguring(false)
      toast({ title: 'Trust provider removed.' })
    },
    onError: (e) => toast({ title: errorMessage(e), tone: 'danger' }),
  })

  if (q.isLoading) return <p className="text-[12.5px] text-ink-mute">Loading…</p>

  const err = q.error
  if (err instanceof ApiError && err.status === 403) {
    return (
      <Card className="p-4">
        <h3 className="text-[15px] font-semibold text-ink">Trust provider</h3>
        <p className="mt-1 text-[12.5px] text-ink-mute">Managing CI federation requires the instance admin role.</p>
      </Card>
    )
  }

  const unconfigured = err instanceof ApiError && err.status === 404
  if (unconfigured && !configuring) {
    return (
      <EmptyState
        className="mt-6"
        title="No trust provider configured"
        hint="Point Janus at your CI's OIDC token issuer so workflows can exchange short-lived tokens."
        action={<Button onClick={() => { setForm(EMPTY_CONFIG); setConfiguring(true) }}>Configure provider</Button>}
      />
    )
  }

  if (err && !unconfigured) {
    return <p className="text-[12.5px] text-ink-mute">Unable to load federation configuration.</p>
  }

  const view = q.data

  function submit() {
    save.mutate({ issuer: form.issuer.trim(), audience: form.audience.trim(), enabled: form.enabled })
  }

  return (
    <Card className="p-4">
      <div className="mb-3 flex items-center gap-2">
        <h3 className="text-[15px] font-semibold text-ink">Trust provider</h3>
        {view && (view.enabled
          ? <Pill tone="success" dot>Enabled</Pill>
          : <Pill tone="muted" dot>Disabled</Pill>)}
      </div>
      <p className="mb-4 text-[12.5px] text-ink-mute">
        The OIDC issuer whose workflow tokens are exchanged for scoped Janus service tokens.
      </p>

      <div className="flex flex-col gap-3">
        <Input
          label="Issuer"
          value={form.issuer}
          onChange={(e) => setForm((f) => ({ ...f, issuer: e.target.value }))}
          placeholder="https://token.actions.githubusercontent.com"
          autoComplete="off"
        />
        <Input
          label="Audience"
          value={form.audience}
          onChange={(e) => setForm((f) => ({ ...f, audience: e.target.value }))}
          placeholder="urn:janus:your-org"
          autoComplete="off"
        />
        <label className="flex items-center gap-2 text-[12.5px] font-medium text-ink">
          <input
            type="checkbox"
            checked={form.enabled}
            onChange={(e) => setForm((f) => ({ ...f, enabled: e.target.checked }))}
            className="accent-brand"
          />
          Enable federation
        </label>
      </div>

      <div className="mt-4 flex items-center justify-between gap-2">
        <Button variant="primary" loading={save.isPending} onClick={submit}>Save</Button>
        {view && (
          <Button variant="danger" loading={del.isPending} onClick={() => setConfirmDelete(true)}>Delete provider</Button>
        )}
      </div>

      <ConfirmDialog
        open={confirmDelete}
        onOpenChange={setConfirmDelete}
        title="Delete trust provider?"
        body="CI workflows will no longer be able to exchange tokens until a provider is configured again."
        confirmLabel="Delete"
        tone="danger"
        onConfirm={() => del.mutate()}
      />
    </Card>
  )
}

// ── Trust bindings card ──────────────────────────────────────────────────────

// A small key/value editor row for match_claims. `repository` is seeded and
// required; callers may add extra claim rows.
interface ClaimRow { key: string; value: string }

interface BindingForm {
  name: string
  claims: ClaimRow[]
  scope_kind: 'config' | 'environment'
  scope_id: string
  access: 'read' | 'readwrite'
  ttl_seconds: number
  enabled: boolean
}

function emptyBindingForm(): BindingForm {
  return {
    name: '', claims: [{ key: 'repository', value: '' }],
    scope_kind: 'config', scope_id: '', access: 'read', ttl_seconds: 900, enabled: true,
  }
}

function serializeClaims(rows: ClaimRow[]): Record<string, string> {
  const out: Record<string, string> = {}
  for (const { key, value } of rows) {
    const k = key.trim()
    if (k) out[k] = value
  }
  return out
}

function TrustBindingsCard() {
  const toast = useToast()
  const qc = useQueryClient()
  const q = useQuery({ queryKey: ['sys', 'fed', 'bindings'], queryFn: endpoints.listFederationBindings, retry: false })

  const [creating, setCreating] = useState(false)
  const [form, setForm] = useState<BindingForm>(emptyBindingForm)
  const [confirmDelete, setConfirmDelete] = useState<FederationBindingView | null>(null)

  const create = useMutation({
    mutationFn: (b: FederationBindingInput) => endpoints.createFederationBinding(b),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['sys', 'fed', 'bindings'] })
      setCreating(false)
      toast({ title: 'Trust binding created.' })
    },
    // Surfaced inline in the Sheet AND as a toast so validation errors are visible.
    onError: (e) => toast({ title: errorMessage(e), tone: 'danger' }),
  })

  const del = useMutation({
    mutationFn: (id: string) => endpoints.deleteFederationBinding(id),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['sys', 'fed', 'bindings'] })
      setConfirmDelete(null)
      toast({ title: 'Trust binding deleted.' })
    },
    onError: (e) => toast({ title: errorMessage(e), tone: 'danger' }),
  })

  const err = q.error
  // 403 is already surfaced by the provider card's hint; keep this card quiet to
  // avoid a duplicate hint. Any other load error shows a short line.
  if (err instanceof ApiError && err.status === 403) return null
  if (q.isLoading) return null
  if (err) {
    return (
      <Card className="p-4">
        <h3 className="text-[15px] font-semibold text-ink">Trust bindings</h3>
        <p className="mt-1 text-[12.5px] text-ink-mute">Unable to load trust bindings.</p>
      </Card>
    )
  }

  const bindings = q.data ?? []

  const claims = serializeClaims(form.claims)
  const canCreate = form.name.trim().length > 0 && (claims.repository ?? '').trim().length > 0

  function submit() {
    create.mutate({
      name: form.name.trim(),
      match_claims: serializeClaims(form.claims),
      scope_kind: form.scope_kind,
      scope_id: form.scope_id.trim(),
      access: form.access,
      ttl_seconds: form.ttl_seconds,
      enabled: form.enabled,
    })
  }

  return (
    <Card className="p-4">
      <div className="mb-3 flex items-center justify-between gap-2">
        <h3 className="text-[15px] font-semibold text-ink">Trust bindings</h3>
        <Button
          variant="secondary"
          size="sm"
          onClick={() => { setForm(emptyBindingForm()); setCreating(true) }}
        >
          <Plus size={13} strokeWidth={1.8} /> New binding
        </Button>
      </div>
      <p className="mb-4 text-[12.5px] text-ink-mute">
        Each binding maps CI token claims to a scoped, short-lived Janus token.
      </p>

      {bindings.length === 0 ? (
        <p className="text-[12.5px] text-ink-mute">No trust bindings yet.</p>
      ) : (
        <ul className="flex flex-col divide-y divide-line rounded border border-line">
          {bindings.map((b) => (
            <li key={b.id} className="flex items-center justify-between gap-3 px-3 py-2.5">
              <div className="min-w-0">
                <div className="flex items-center gap-2">
                  <span className="truncate text-[12.5px] font-semibold text-ink">{b.name}</span>
                  <Pill tone={b.access === 'readwrite' ? 'warning' : 'info'}>{b.access}</Pill>
                  {!b.enabled && <Pill tone="muted">Disabled</Pill>}
                </div>
                <div className="mt-0.5 flex flex-wrap gap-x-3 text-[11.5px] text-ink-mute">
                  <span className="font-mono">{b.match_claims.repository ?? '—'}</span>
                  <span>{b.scope_kind}: <span className="font-mono">{b.scope_id}</span></span>
                  <span>ttl {b.ttl_seconds}s</span>
                </div>
              </div>
              <Button
                variant="ghost"
                size="sm"
                aria-label="delete binding"
                onClick={() => setConfirmDelete(b)}
              >
                <Trash2 size={14} strokeWidth={1.7} />
              </Button>
            </li>
          ))}
        </ul>
      )}

      <Sheet open={creating} onOpenChange={setCreating} title="New trust binding">
        <div className="flex flex-col gap-3">
          <Input
            label="Name"
            value={form.name}
            onChange={(e) => setForm((f) => ({ ...f, name: e.target.value }))}
            placeholder="ci-deploy"
            autoComplete="off"
          />

          <div className="flex flex-col gap-1.5">
            <span className="text-[12px] font-semibold text-ink">Match claims</span>
            <p className="text-[11.5px] text-ink-mute">A non-empty <span className="font-mono">repository</span> claim is required.</p>
            {form.claims.map((row, i) => (
              <div key={i} className="flex items-center gap-2">
                <input
                  aria-label={i === 0 ? 'repository claim key' : `claim key ${i + 1}`}
                  className={cn(FIELD, 'flex-1 font-mono')}
                  value={row.key}
                  readOnly={i === 0}
                  onChange={(e) => setForm((f) => {
                    const claims = [...f.claims]; claims[i] = { ...claims[i], key: e.target.value }; return { ...f, claims }
                  })}
                />
                <input
                  aria-label={row.key === 'repository' ? 'repository' : `claim value ${i + 1}`}
                  className={cn(FIELD, 'flex-1 font-mono')}
                  placeholder={row.key === 'repository' ? 'org/repo' : 'value'}
                  value={row.value}
                  onChange={(e) => setForm((f) => {
                    const claims = [...f.claims]; claims[i] = { ...claims[i], value: e.target.value }; return { ...f, claims }
                  })}
                />
                {i > 0 && (
                  <Button
                    variant="ghost"
                    size="sm"
                    aria-label={`remove claim ${i + 1}`}
                    onClick={() => setForm((f) => ({ ...f, claims: f.claims.filter((_, j) => j !== i) }))}
                  >
                    <Trash2 size={13} strokeWidth={1.7} />
                  </Button>
                )}
              </div>
            ))}
            <Button
              variant="ghost"
              size="sm"
              className="self-start"
              onClick={() => setForm((f) => ({ ...f, claims: [...f.claims, { key: '', value: '' }] }))}
            >
              <Plus size={13} strokeWidth={1.8} /> Add claim
            </Button>
          </div>

          <label className="flex flex-col gap-1 text-[12px] font-semibold text-ink">
            Scope kind
            <select
              className={FIELD}
              value={form.scope_kind}
              onChange={(e) => setForm((f) => ({ ...f, scope_kind: e.target.value as BindingForm['scope_kind'] }))}
            >
              <option value="config">config</option>
              <option value="environment">environment</option>
            </select>
          </label>

          <Input
            label="Scope ID"
            value={form.scope_id}
            onChange={(e) => setForm((f) => ({ ...f, scope_id: e.target.value }))}
            placeholder="config or environment UUID"
            autoComplete="off"
          />

          <label className="flex flex-col gap-1 text-[12px] font-semibold text-ink">
            Access
            <select
              className={FIELD}
              value={form.access}
              onChange={(e) => setForm((f) => ({ ...f, access: e.target.value as BindingForm['access'] }))}
            >
              <option value="read">read</option>
              <option value="readwrite">readwrite</option>
            </select>
          </label>

          <Input
            label="TTL (seconds)"
            type="number"
            value={String(form.ttl_seconds)}
            onChange={(e) => setForm((f) => ({ ...f, ttl_seconds: Number(e.target.value) }))}
            placeholder="900"
            autoComplete="off"
          />
          <p className="-mt-1.5 text-[11.5px] text-ink-mute">Default 900, max 3600.</p>

          <label className="flex items-center gap-2 text-[12.5px] font-medium text-ink">
            <input
              type="checkbox"
              checked={form.enabled}
              onChange={(e) => setForm((f) => ({ ...f, enabled: e.target.checked }))}
              className="accent-brand"
            />
            Enable binding
          </label>

          {create.isError && (
            <p className="text-[11.5px] text-danger">{errorMessage(create.error)}</p>
          )}

          <div className="mt-1 flex justify-end gap-2">
            <Button variant="secondary" onClick={() => setCreating(false)}>Cancel</Button>
            <Button variant="primary" loading={create.isPending} disabled={!canCreate} onClick={submit}>Create binding</Button>
          </div>
        </div>
      </Sheet>

      <ConfirmDialog
        open={confirmDelete !== null}
        onOpenChange={(open) => { if (!open) setConfirmDelete(null) }}
        title="Delete trust binding?"
        body={confirmDelete ? `The binding "${confirmDelete.name}" will stop issuing tokens immediately.` : ''}
        confirmLabel="Delete"
        tone="danger"
        onConfirm={() => { if (confirmDelete) del.mutate(confirmDelete.id) }}
      />
    </Card>
  )
}

export function FederationSection() {
  return (
    <div className="flex flex-col gap-6">
      <TrustProviderCard />
      <TrustBindingsCard />
    </div>
  )
}
