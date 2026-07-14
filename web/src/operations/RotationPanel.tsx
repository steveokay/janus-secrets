import { useEffect, useMemo, useState } from 'react'
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { Plus } from 'lucide-react'
import { Button } from '../ui/Button'
import { Pill } from '../ui/Pill'
import { Modal } from '../ui/Modal'
import { Sheet } from '../ui/Sheet'
import { Input } from '../ui/Input'
import { Select } from '../ui/Select'
import { ConfirmDialog } from '../ui/ConfirmDialog'
import { useToast } from '../ui/Toast'
import { apiErrorTitle, errorMessage } from '../lib/api'
import { useRowSelection } from '../lib/useRowSelection'
import { opsEndpoints, type RotationView, type RotationCreateInput } from './endpoints'
import { useRotation, type EngineRow, type ProjectFilter } from './useAggregated'
import { ConfigPicker } from './ConfigPicker'
import { OpsTable, StatusPill, RelTime, LastError, type OpsColumn, type OpsSort } from './ops-ui'
import { OpsSelectionBar } from './OpsSelectionBar'
import { RunHistorySheet } from './RunHistorySheet'

const COLUMNS: OpsColumn[] = [
  { label: 'Project', key: 'project' },
  { label: 'Config', key: 'config' },
  { label: 'Secret key', key: 'secret_key' },
  { label: 'Type', key: 'type' },
  { label: 'Status', key: 'status' },
  { label: 'Next', key: 'next' },
  { label: 'Last', key: 'last' },
  { label: 'Fails', key: 'fails' },
]

// Next-cycle sort idiom (mirror the editor): none → asc → desc → none.
function cycleSort(prev: OpsSort, key: string): OpsSort {
  if (prev?.key !== key) return { key, dir: 'asc' }
  if (prev.dir === 'asc') return { key, dir: 'desc' }
  return null
}

function cfgLabel(r: EngineRow<RotationView>): string {
  return r.cfg ? `${r.cfg.envName}/${r.cfg.configName}` : ''
}

function compare(a: EngineRow<RotationView>, b: EngineRow<RotationView>, key: string): number {
  const s = (x: string, y: string) => x.localeCompare(y)
  switch (key) {
    case 'project': return s(a.projectName, b.projectName)
    case 'config': return s(cfgLabel(a), cfgLabel(b))
    case 'secret_key': return s(a.data.secret_key, b.data.secret_key)
    case 'type': return s(a.data.type, b.data.type)
    case 'status': return s(a.data.status, b.data.status)
    case 'next': return s(a.data.next_rotation_at, b.data.next_rotation_at)
    case 'last': return nullableTime(a.data.last_rotated_at, b.data.last_rotated_at)
    case 'fails': return a.data.failure_count - b.data.failure_count
    default: return 0
  }
}

// Nulls sort last regardless of direction (applied before the dir multiplier).
function nullableTime(a?: string | null, b?: string | null): number {
  if (!a && !b) return 0
  if (!a) return 1
  if (!b) return -1
  return a.localeCompare(b)
}

export function RotationPanel({ filter }: { filter: ProjectFilter }) {
  const { rows, isLoading, isError, someForbidden } = useRotation(filter)
  const [creating, setCreating] = useState(false)
  const [sort, setSort] = useState<OpsSort>(null)
  const sel = useRowSelection()
  const qc = useQueryClient()
  const toast = useToast()
  const [confirmBulkDel, setConfirmBulkDel] = useState(false)
  const [bulkBusy, setBulkBusy] = useState(false)

  const sorted = useMemo(() => {
    const list = [...rows]
    if (sort === null) {
      // Default: failing first, then soonest next-rotation.
      return list.sort((a, b) => {
        const af = a.data.status === 'failed' ? 0 : 1
        const bf = b.data.status === 'failed' ? 0 : 1
        if (af !== bf) return af - bf
        return a.data.next_rotation_at.localeCompare(b.data.next_rotation_at)
      })
    }
    const dir = sort.dir === 'asc' ? 1 : -1
    return list.sort((a, b) => compare(a, b, sort.key) * dir)
  }, [rows, sort])

  const ids = sorted.map((r) => r.data.id)
  useEffect(() => { sel.prune(ids) }, [sorted]) // eslint-disable-line react-hooks/exhaustive-deps
  const allSelected = sorted.length > 0 && sorted.every((r) => sel.isSelected(r.data.id))

  async function runBulk(label: string, fn: (id: string) => Promise<unknown>) {
    const targets = ids.filter((id) => sel.isSelected(id))
    if (targets.length === 0) return
    setBulkBusy(true)
    const results = await Promise.allSettled(targets.map((id) => fn(id)))
    setBulkBusy(false)
    const failed = results.filter((r) => r.status === 'rejected').length
    const ok = results.length - failed
    qc.invalidateQueries({ queryKey: ['ops', 'rotation'] })
    sel.clear()
    toast({ title: failed ? `${label} ${ok} · ${failed} failed` : `${label} ${ok}`, tone: failed ? 'danger' : 'success' })
  }

  return (
    <div className="flex flex-col gap-3">
      <div className="flex justify-end">
        <Button variant="secondary" size="sm" onClick={() => setCreating(true)}>
          <Plus size={13} strokeWidth={1.8} /> New policy
        </Button>
      </div>
      {sel.count > 0 && (
        <OpsSelectionBar
          count={sel.count}
          onClear={sel.clear}
          actions={[
            { label: 'Pause', onClick: () => runBulk('Paused', (id) => opsEndpoints.rotation.setStatus(id, 'paused')), loading: bulkBusy },
            { label: 'Resume', onClick: () => runBulk('Resumed', (id) => opsEndpoints.rotation.setStatus(id, 'active')), loading: bulkBusy },
            { label: 'Rotate now', onClick: () => runBulk('Rotated', (id) => opsEndpoints.rotation.rotateNow(id)), loading: bulkBusy },
            { label: 'Delete', tone: 'danger', onClick: () => setConfirmBulkDel(true), loading: bulkBusy },
          ]}
        />
      )}
      <OpsTable
        columns={[...COLUMNS, '']}
        isLoading={isLoading}
        isError={isError}
        allForbidden={someForbidden && rows.length === 0}
        isEmpty={rows.length === 0}
        someForbidden={someForbidden}
        forbiddenHint="Ask a project admin for the rotation role."
        emptyHint="No rotation policies yet."
        sort={sort}
        onSort={(key) => setSort((prev) => cycleSort(prev, key))}
        selectable
        allSelected={allSelected}
        onToggleAll={() => sel.setAll(ids)}
      >
        {sorted.map((r) => (
          <RotationRow key={r.data.id} row={r} selected={sel.isSelected(r.data.id)} onToggle={() => sel.toggle(r.data.id)} />
        ))}
      </OpsTable>
      <CreateRotationSheet open={creating} onOpenChange={setCreating} filter={filter} />
      <ConfirmDialog
        open={confirmBulkDel}
        onOpenChange={setConfirmBulkDel}
        title={`Delete ${sel.count} rotation ${sel.count === 1 ? 'policy' : 'policies'}?`}
        body={<span>This stops scheduled rotation for the selected policies. Current secret values are unchanged.</span>}
        confirmLabel="Delete"
        tone="danger"
        onConfirm={() => runBulk('Deleted', (id) => opsEndpoints.rotation.remove(id))}
      />
    </div>
  )
}

// Write-only create form. Secret fields (admin_dsn / hmac_key / notify_hmac_key)
// live ONLY in this ephemeral state, are sent once in the POST body, and are
// never rendered from a fetched value (list + create responses are masked).
interface RotationForm {
  config_id: string
  secret_key: string
  type: 'postgres' | 'webhook'
  interval_seconds: number
  admin_dsn: string
  role: string
  password_len: number
  url: string
  hmac_key: string
  notify_url: string
  notify_hmac_key: string
}

function emptyRotationForm(): RotationForm {
  return {
    config_id: '', secret_key: '', type: 'postgres', interval_seconds: 3600,
    admin_dsn: '', role: '', password_len: 32,
    url: '', hmac_key: '', notify_url: '', notify_hmac_key: '',
  }
}

function CreateRotationSheet({ open, onOpenChange, filter }: {
  open: boolean
  onOpenChange: (open: boolean) => void
  filter: ProjectFilter
}) {
  const qc = useQueryClient()
  const toast = useToast()
  const [form, setForm] = useState<RotationForm>(emptyRotationForm)

  // Reset the form (secrets → '') whenever the Sheet opens.
  useEffect(() => {
    if (open) setForm(emptyRotationForm())
  }, [open])

  const create = useMutation({
    mutationFn: (body: RotationCreateInput) => opsEndpoints.rotation.create(body),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['ops', 'rotation'] })
      setForm(emptyRotationForm())
      onOpenChange(false)
      toast({ title: 'Policy created', tone: 'success' })
    },
    onError: (e) => toast({ title: errorMessage(e), tone: 'danger' }),
  })

  const isPg = form.type === 'postgres'
  const canCreate = !!form.config_id && !!form.secret_key.trim() &&
    form.interval_seconds >= 1 &&
    (isPg
      ? !!form.admin_dsn && form.password_len >= 8
      : !!form.url.trim() && !!form.hmac_key)

  function submit() {
    const config: RotationCreateInput['config'] = isPg
      ? { admin_dsn: form.admin_dsn, role: form.role.trim() || undefined, password_len: form.password_len }
      : { url: form.url.trim(), hmac_key: form.hmac_key }
    if (form.notify_url.trim()) config.notify_url = form.notify_url.trim()
    if (form.notify_hmac_key) config.notify_hmac_key = form.notify_hmac_key
    create.mutate({
      config_id: form.config_id,
      secret_key: form.secret_key.trim(),
      type: form.type,
      interval_seconds: form.interval_seconds,
      config,
    })
  }

  // Reset lifecycle: the open-edge useEffect re-seeds an empty form on every
  // open (+onSuccess), so close paths just close — no redundant reset here.
  function cancel() {
    onOpenChange(false)
  }

  return (
    <Sheet open={open} onOpenChange={onOpenChange} title="New rotation policy">
      <div className="flex flex-col gap-3">
        <ConfigPicker filter={filter} value={form.config_id} onChange={(id) => setForm((f) => ({ ...f, config_id: id }))} />

        <Input
          label="Secret key"
          value={form.secret_key}
          onChange={(e) => setForm((f) => ({ ...f, secret_key: e.target.value }))}
          placeholder="DB_PASSWORD"
          autoComplete="off"
          className="font-mono"
        />

        <Select
          label="Type"
          value={form.type}
          onChange={(e) => setForm((f) => ({ ...f, type: e.target.value as RotationForm['type'] }))}
        >
          <option value="postgres">postgres</option>
          <option value="webhook">webhook</option>
        </Select>

        <Input
          label="Interval (seconds)"
          type="number"
          min={1}
          value={String(form.interval_seconds)}
          onChange={(e) => setForm((f) => ({ ...f, interval_seconds: Number(e.target.value) }))}
          autoComplete="off"
        />

        {isPg ? (
          <>
            <Input
              label="Admin DSN"
              type="password"
              autoComplete="off"
              value={form.admin_dsn}
              onChange={(e) => setForm((f) => ({ ...f, admin_dsn: e.target.value }))}
              placeholder="postgres://admin@host/db"
            />
            <Input
              label="Role"
              value={form.role}
              onChange={(e) => setForm((f) => ({ ...f, role: e.target.value }))}
              placeholder="app_user (defaults to secret key)"
              autoComplete="off"
            />
            <Input
              label="Password length"
              type="number"
              min={8}
              value={String(form.password_len)}
              onChange={(e) => setForm((f) => ({ ...f, password_len: Number(e.target.value) }))}
              autoComplete="off"
            />
          </>
        ) : (
          <>
            <Input
              label="URL"
              value={form.url}
              onChange={(e) => setForm((f) => ({ ...f, url: e.target.value }))}
              placeholder="https://hooks.example.com/rotate"
              autoComplete="off"
            />
            <Input
              label="HMAC key"
              type="password"
              autoComplete="off"
              value={form.hmac_key}
              onChange={(e) => setForm((f) => ({ ...f, hmac_key: e.target.value }))}
            />
          </>
        )}

        <Input
          label="Notify URL (optional)"
          value={form.notify_url}
          onChange={(e) => setForm((f) => ({ ...f, notify_url: e.target.value }))}
          placeholder="https://hooks.example.com/notify"
          autoComplete="off"
        />
        <Input
          label="Notify HMAC key (optional)"
          type="password"
          autoComplete="off"
          value={form.notify_hmac_key}
          onChange={(e) => setForm((f) => ({ ...f, notify_hmac_key: e.target.value }))}
        />

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

function RotationRow({ row, selected, onToggle }: { row: EngineRow<RotationView>; selected: boolean; onToggle: () => void }) {
  const qc = useQueryClient()
  const toast = useToast()
  const p = row.data
  const [editing, setEditing] = useState(false)
  const [confirmDel, setConfirmDel] = useState(false)
  const [showRuns, setShowRuns] = useState(false)

  const invalidate = () => qc.invalidateQueries({ queryKey: ['ops', 'rotation'] })
  const onErr = (e: unknown) => toast({ title: apiErrorTitle(e), tone: 'danger' })

  const rotate = useMutation({
    mutationFn: () => opsEndpoints.rotation.rotateNow(p.id),
    onSuccess: (r) => { toast({ title: `Rotated → v${r.config_version}`, tone: 'success' }); invalidate() },
    onError: onErr,
  })
  const toggle = useMutation({
    mutationFn: () => opsEndpoints.rotation.setStatus(p.id, p.status === 'paused' ? 'active' : 'paused'),
    onSuccess: () => { invalidate() },
    onError: onErr,
  })
  const del = useMutation({
    mutationFn: () => opsEndpoints.rotation.remove(p.id),
    onSuccess: () => { toast({ title: 'Policy deleted', tone: 'success' }); invalidate() },
    onError: onErr,
  })

  return (
    <tr className="border-b border-line-soft hover:bg-row-hover transition-nocturne">
      <td className="px-2 py-1.5">
        <input
          type="checkbox"
          aria-label={`select ${p.secret_key}`}
          checked={selected}
          onChange={onToggle}
          className="accent-brand"
        />
      </td>
      <td className="px-2 py-1.5">{row.projectName}</td>
      <td className="px-2 py-1.5">{row.cfg ? `${row.cfg.envName}/${row.cfg.configName}` : short(p.config_id)}</td>
      <td className="px-2 py-1.5 font-mono">{p.secret_key}</td>
      <td className="px-2 py-1.5"><Pill tone="muted">{p.type}</Pill></td>
      <td className="px-2 py-1.5"><span className="inline-flex items-center gap-1"><StatusPill status={p.status} /><LastError text={p.last_error} /></span></td>
      <td className="px-2 py-1.5"><RelTime iso={p.next_rotation_at} /></td>
      <td className="px-2 py-1.5"><RelTime iso={p.last_rotated_at} /></td>
      <td className="px-2 py-1.5">{p.failure_count}</td>
      <td className="px-2 py-1.5">
        <div className="flex justify-end gap-1">
          <Button size="sm" variant="secondary" loading={rotate.isPending} onClick={() => rotate.mutate()}>Rotate now</Button>
          <Button size="sm" variant="ghost" loading={toggle.isPending} onClick={() => toggle.mutate()}>{p.status === 'paused' ? 'Resume' : 'Pause'}</Button>
          <Button size="sm" variant="ghost" onClick={() => setEditing(true)}>Interval</Button>
          <Button size="sm" variant="ghost" onClick={() => setShowRuns(true)}>Runs</Button>
          <Button size="sm" variant="danger" onClick={() => setConfirmDel(true)}>Delete</Button>
        </div>
      </td>
      <IntervalModal
        open={editing}
        onClose={() => setEditing(false)}
        current={p.interval_seconds}
        onSave={(n) => opsEndpoints.rotation.setInterval(p.id, n)}
        afterSave={() => { setEditing(false); invalidate() }}
        onError={onErr}
      />
      <ConfirmDialog
        open={confirmDel}
        onOpenChange={setConfirmDel}
        title="Delete rotation policy?"
        body={<span>This stops scheduled rotation of <b>{p.secret_key}</b>. The current secret value is unchanged.</span>}
        confirmLabel="Delete"
        tone="danger"
        onConfirm={() => del.mutate()}
      />
      <RunHistorySheet
        open={showRuns}
        onOpenChange={setShowRuns}
        title={`Runs · ${p.secret_key}`}
        load={(c) => opsEndpoints.rotation.runs(p.id, c)}
      />
    </tr>
  )
}

export function IntervalModal({
  open, onClose, current, onSave, afterSave, onError,
}: {
  open: boolean
  onClose: () => void
  current: number
  onSave: (n: number) => Promise<unknown>
  afterSave: () => void
  onError: (e: unknown) => void
}) {
  const [val, setVal] = useState(String(current))
  useEffect(() => {
    if (open) setVal(String(current))
    // reseed only on open; deps intentionally exclude `current` so a
    // background refetch can't clobber an in-progress edit
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open])
  const save = useMutation({ mutationFn: () => onSave(Number(val)), onSuccess: afterSave, onError })
  return (
    <Modal open={open} onClose={onClose} label="Edit interval">
      <div className="space-y-3">
        <h2 className="text-sm font-semibold text-ink">Edit interval</h2>
        <Input label="Seconds" type="number" min={1} value={val} onChange={(e) => setVal(e.target.value)} />
        <div className="flex justify-end gap-2">
          <Button variant="ghost" size="sm" onClick={onClose}>Cancel</Button>
          <Button size="sm" loading={save.isPending} disabled={!val || Number(val) < 1} onClick={() => save.mutate()}>Save</Button>
        </div>
      </div>
    </Modal>
  )
}

function short(id: string): string {
  return id.length > 8 ? `${id.slice(0, 8)}…` : id
}
