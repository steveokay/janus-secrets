import { useEffect, useState } from 'react'
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { Plus } from 'lucide-react'
import { Button } from '../ui/Button'
import { Pill } from '../ui/Pill'
import { Sheet } from '../ui/Sheet'
import { Input } from '../ui/Input'
import { Select } from '../ui/Select'
import { ConfirmDialog } from '../ui/ConfirmDialog'
import { useToast } from '../ui/Toast'
import { apiErrorTitle, errorMessage } from '../lib/api'
import { opsEndpoints, type SyncView, type SyncAddr, type SyncCreateInput } from './endpoints'
import { useSync, type EngineRow, type ProjectFilter } from './useAggregated'
import { ConfigPicker } from './ConfigPicker'
import { OpsTable, StatusPill, RelTime, LastError } from './ops-ui'
import { IntervalModal } from './RotationPanel'

export function SyncPanel({ filter }: { filter: ProjectFilter }) {
  const { rows, isLoading, isError, someForbidden } = useSync(filter)
  const [creating, setCreating] = useState(false)
  return (
    <div className="flex flex-col gap-3">
      <div className="flex justify-end">
        <Button variant="secondary" size="sm" onClick={() => setCreating(true)}>
          <Plus size={13} strokeWidth={1.8} /> New target
        </Button>
      </div>
      <OpsTable
        columns={['Project', 'Config', 'Provider', 'Destination', 'Prune', 'Status', 'Next', 'Last', 'Fails', '']}
        isLoading={isLoading}
        isError={isError}
        allForbidden={someForbidden && rows.length === 0}
        isEmpty={rows.length === 0}
        someForbidden={someForbidden}
        forbiddenHint="Ask a project admin for the sync role."
        emptyHint="No sync targets yet."
      >
        {rows.map((r) => (
          <SyncRow key={r.data.id} row={r} />
        ))}
      </OpsTable>
      <CreateSyncSheet open={creating} onOpenChange={setCreating} filter={filter} />
    </div>
  )
}

// Write-only create form. Secret fields (pat / ca_cert / token) live ONLY in
// this ephemeral state, are sent once in the POST body, and are never rendered
// from a fetched value (list + create responses are masked).
interface SyncForm {
  config_id: string
  provider: 'github' | 'k8s'
  prune: boolean
  interval_seconds: number
  owner: string
  repo: string
  environment: string
  pat: string
  api_url: string
  namespace: string
  secret_name: string
  ca_cert: string
  token: string
}

function emptySyncForm(): SyncForm {
  return {
    config_id: '', provider: 'github', prune: true, interval_seconds: 3600,
    owner: '', repo: '', environment: '', pat: '', api_url: '',
    namespace: '', secret_name: '', ca_cert: '', token: '',
  }
}

function CreateSyncSheet({ open, onOpenChange, filter }: {
  open: boolean
  onOpenChange: (open: boolean) => void
  filter: ProjectFilter
}) {
  const qc = useQueryClient()
  const toast = useToast()
  const [form, setForm] = useState<SyncForm>(emptySyncForm)

  // Reset the form (secrets → '') whenever the Sheet opens.
  useEffect(() => {
    if (open) setForm(emptySyncForm())
  }, [open])

  const create = useMutation({
    mutationFn: (body: SyncCreateInput) => opsEndpoints.sync.create(body),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['ops', 'sync'] })
      setForm(emptySyncForm())
      onOpenChange(false)
      toast({ title: 'Target created', tone: 'success' })
    },
    onError: (e) => toast({ title: errorMessage(e), tone: 'danger' }),
  })

  const isGh = form.provider === 'github'
  const canCreate = !!form.config_id && form.interval_seconds >= 1 &&
    (isGh
      ? !!form.owner.trim() && !!form.repo.trim() && !!form.pat
      : !!form.namespace.trim() && !!form.secret_name.trim() && !!form.api_url.trim() && !!form.token)

  function submit() {
    const addr: SyncCreateInput['addr'] = isGh
      ? { owner: form.owner.trim(), repo: form.repo.trim(), environment: form.environment.trim() || undefined }
      : { namespace: form.namespace.trim(), secret_name: form.secret_name.trim() }
    const creds: SyncCreateInput['creds'] = isGh
      ? { pat: form.pat, api_url: form.api_url.trim() || undefined }
      : { api_url: form.api_url.trim(), ca_cert: form.ca_cert || undefined, token: form.token }
    create.mutate({
      config_id: form.config_id,
      provider: form.provider,
      prune: form.prune,
      interval_seconds: form.interval_seconds,
      addr,
      creds,
    })
  }

  // Reset lifecycle: the open-edge useEffect re-seeds an empty form on every
  // open (+onSuccess), so close paths just close — no redundant reset here.
  function cancel() {
    onOpenChange(false)
  }

  return (
    <Sheet open={open} onOpenChange={onOpenChange} title="New sync target">
      <div className="flex flex-col gap-3">
        <ConfigPicker filter={filter} value={form.config_id} onChange={(id) => setForm((f) => ({ ...f, config_id: id }))} />

        <Select
          label="Provider"
          value={form.provider}
          onChange={(e) => setForm((f) => ({ ...f, provider: e.target.value as SyncForm['provider'] }))}
        >
          <option value="github">github</option>
          <option value="k8s">k8s</option>
        </Select>

        <label className="flex items-center gap-2 text-[12px] font-semibold text-ink">
          <input
            type="checkbox"
            checked={form.prune}
            onChange={(e) => setForm((f) => ({ ...f, prune: e.target.checked }))}
            className="h-3.5 w-3.5 rounded border-line accent-brand"
          />
          Prune destination keys not in this config
        </label>

        <Input
          label="Interval (seconds)"
          type="number"
          min={1}
          value={String(form.interval_seconds)}
          onChange={(e) => setForm((f) => ({ ...f, interval_seconds: Number(e.target.value) }))}
          autoComplete="off"
        />

        {isGh ? (
          <>
            <Input
              label="Owner"
              value={form.owner}
              onChange={(e) => setForm((f) => ({ ...f, owner: e.target.value }))}
              placeholder="acme"
              autoComplete="off"
            />
            <Input
              label="Repo"
              value={form.repo}
              onChange={(e) => setForm((f) => ({ ...f, repo: e.target.value }))}
              placeholder="widgets"
              autoComplete="off"
            />
            <Input
              label="Environment (optional)"
              value={form.environment}
              onChange={(e) => setForm((f) => ({ ...f, environment: e.target.value }))}
              placeholder="production"
              autoComplete="off"
            />
            <Input
              label="Personal access token"
              type="password"
              autoComplete="off"
              value={form.pat}
              onChange={(e) => setForm((f) => ({ ...f, pat: e.target.value }))}
            />
            <Input
              label="API URL (optional, GitHub Enterprise)"
              value={form.api_url}
              onChange={(e) => setForm((f) => ({ ...f, api_url: e.target.value }))}
              placeholder="https://github.example.com/api/v3"
              autoComplete="off"
            />
          </>
        ) : (
          <>
            <Input
              label="Namespace"
              value={form.namespace}
              onChange={(e) => setForm((f) => ({ ...f, namespace: e.target.value }))}
              placeholder="apps"
              autoComplete="off"
            />
            <Input
              label="Secret name"
              value={form.secret_name}
              onChange={(e) => setForm((f) => ({ ...f, secret_name: e.target.value }))}
              placeholder="app-secrets"
              autoComplete="off"
            />
            <Input
              label="API URL"
              value={form.api_url}
              onChange={(e) => setForm((f) => ({ ...f, api_url: e.target.value }))}
              placeholder="https://k8s.example.com"
              autoComplete="off"
            />
            <Input
              label="CA cert (optional)"
              type="password"
              autoComplete="off"
              value={form.ca_cert}
              onChange={(e) => setForm((f) => ({ ...f, ca_cert: e.target.value }))}
            />
            <Input
              label="Token"
              type="password"
              autoComplete="off"
              value={form.token}
              onChange={(e) => setForm((f) => ({ ...f, token: e.target.value }))}
            />
          </>
        )}

        {create.isError && (
          <p className="text-[11.5px] text-danger">{errorMessage(create.error)}</p>
        )}

        <div className="mt-1 flex justify-end gap-2">
          <Button variant="secondary" onClick={cancel}>Cancel</Button>
          <Button variant="primary" loading={create.isPending} disabled={!canCreate} onClick={submit}>Create</Button>
        </div>
      </div>
    </Sheet>
  )
}

function destination(addr: SyncAddr): string {
  if (addr.owner || addr.repo) return `${addr.owner ?? '?'}/${addr.repo ?? '?'}${addr.environment ? `:${addr.environment}` : ''}`
  if (addr.namespace || addr.secret_name) return `${addr.namespace ?? '?'}/${addr.secret_name ?? '?'}`
  return '—'
}

function SyncRow({ row }: { row: EngineRow<SyncView> }) {
  const qc = useQueryClient()
  const toast = useToast()
  const t = row.data
  const [editing, setEditing] = useState(false)
  const [confirmDel, setConfirmDel] = useState(false)
  const invalidate = () => qc.invalidateQueries({ queryKey: ['ops', 'sync'] })
  const onErr = (e: unknown) => toast({ title: apiErrorTitle(e), tone: 'danger' })

  const syncNow = useMutation({ mutationFn: () => opsEndpoints.sync.syncNow(t.id), onSuccess: () => { toast({ title: 'Synced', tone: 'success' }); invalidate() }, onError: onErr })
  const toggle = useMutation({ mutationFn: () => opsEndpoints.sync.setStatus(t.id, t.status === 'paused' ? 'active' : 'paused'), onSuccess: invalidate, onError: onErr })
  const del = useMutation({ mutationFn: () => opsEndpoints.sync.remove(t.id), onSuccess: () => { toast({ title: 'Target deleted', tone: 'success' }); invalidate() }, onError: onErr })

  return (
    <tr className="border-b border-line-soft hover:bg-row-hover transition-nocturne">
      <td className="px-2 py-1.5">{row.projectName}</td>
      <td className="px-2 py-1.5">{row.cfg ? `${row.cfg.envName}/${row.cfg.configName}` : '—'}</td>
      <td className="px-2 py-1.5"><Pill tone="muted">{t.provider}</Pill></td>
      <td className="px-2 py-1.5 font-mono">{destination(t.addr)}</td>
      <td className="px-2 py-1.5">{t.prune ? 'on' : 'off'}</td>
      <td className="px-2 py-1.5"><span className="inline-flex items-center gap-1"><StatusPill status={t.status} /><LastError text={t.last_error} /></span></td>
      <td className="px-2 py-1.5"><RelTime iso={t.next_sync_at} /></td>
      <td className="px-2 py-1.5"><RelTime iso={t.last_synced_at} /></td>
      <td className="px-2 py-1.5">{t.failure_count}</td>
      <td className="px-2 py-1.5">
        <div className="flex justify-end gap-1">
          <Button size="sm" variant="secondary" loading={syncNow.isPending} onClick={() => syncNow.mutate()}>Sync now</Button>
          <Button size="sm" variant="ghost" loading={toggle.isPending} onClick={() => toggle.mutate()}>{t.status === 'paused' ? 'Resume' : 'Pause'}</Button>
          <Button size="sm" variant="ghost" onClick={() => setEditing(true)}>Interval</Button>
          <Button size="sm" variant="danger" onClick={() => setConfirmDel(true)}>Delete</Button>
        </div>
      </td>
      <IntervalModal open={editing} onClose={() => setEditing(false)} current={t.interval_seconds} onSave={(n) => opsEndpoints.sync.setInterval(t.id, n)} afterSave={() => { setEditing(false); invalidate() }} onError={onErr} />
      <ConfirmDialog open={confirmDel} onOpenChange={setConfirmDel} title="Delete sync target?" body={<span>This stops replicating this config to <b>{destination(t.addr)}</b>. The destination is left as-is.</span>} confirmLabel="Delete" tone="danger" onConfirm={() => del.mutate()} />
    </tr>
  )
}
