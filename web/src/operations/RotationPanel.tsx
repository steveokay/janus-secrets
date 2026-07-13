import { useEffect, useState } from 'react'
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { Button } from '../ui/Button'
import { Pill } from '../ui/Pill'
import { Modal } from '../ui/Modal'
import { Input } from '../ui/Input'
import { ConfirmDialog } from '../ui/ConfirmDialog'
import { useToast } from '../ui/Toast'
import { apiErrorTitle } from '../lib/api'
import { opsEndpoints, type RotationView } from './endpoints'
import { useRotation, type EngineRow, type ProjectFilter } from './useAggregated'
import { OpsTable, StatusPill, RelTime, LastError } from './ops-ui'

export function RotationPanel({ filter }: { filter: ProjectFilter }) {
  const { rows, isLoading, isError, someForbidden } = useRotation(filter)
  return (
    <OpsTable
      columns={['Project', 'Config', 'Secret key', 'Type', 'Status', 'Next', 'Last', 'Fails', '']}
      isLoading={isLoading}
      isError={isError}
      allForbidden={someForbidden && rows.length === 0}
      isEmpty={rows.length === 0}
      someForbidden={someForbidden}
      forbiddenHint="Ask a project admin for the rotation role."
      emptyHint="No rotation policies. Create one with `janus rotation create`."
    >
      {rows.map((r) => (
        <RotationRow key={r.data.id} row={r} />
      ))}
    </OpsTable>
  )
}

function RotationRow({ row }: { row: EngineRow<RotationView> }) {
  const qc = useQueryClient()
  const toast = useToast()
  const p = row.data
  const [editing, setEditing] = useState(false)
  const [confirmDel, setConfirmDel] = useState(false)

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
