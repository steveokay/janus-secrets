import { useState } from 'react'
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { Button } from '../ui/Button'
import { Pill } from '../ui/Pill'
import { ConfirmDialog } from '../ui/ConfirmDialog'
import { useToast } from '../ui/Toast'
import { apiErrorTitle } from '../lib/api'
import { opsEndpoints, type SyncView, type SyncAddr } from './endpoints'
import { useSync, type EngineRow, type ProjectFilter } from './useAggregated'
import { OpsTable, StatusPill, RelTime, LastError } from './ops-ui'
import { IntervalModal } from './RotationPanel'

export function SyncPanel({ filter }: { filter: ProjectFilter }) {
  const { rows, isLoading, isError, someForbidden } = useSync(filter)
  return (
    <OpsTable
      columns={['Project', 'Config', 'Provider', 'Destination', 'Prune', 'Status', 'Next', 'Last', 'Fails', '']}
      isLoading={isLoading}
      isError={isError}
      allForbidden={someForbidden && rows.length === 0}
      isEmpty={rows.length === 0}
      someForbidden={someForbidden}
      forbiddenHint="Ask a project admin for the sync role."
      emptyHint="No sync targets. Create one with `janus sync create`."
    >
      {rows.map((r) => (
        <SyncRow key={r.data.id} row={r} />
      ))}
    </OpsTable>
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
    <tr className="border-b border-line-soft">
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
          <Button size="sm" variant="ghost" onClick={() => setConfirmDel(true)}>Delete</Button>
        </div>
      </td>
      <IntervalModal open={editing} onClose={() => setEditing(false)} current={t.interval_seconds} onSave={(n) => opsEndpoints.sync.setInterval(t.id, n)} afterSave={() => { setEditing(false); invalidate() }} onError={onErr} />
      <ConfirmDialog open={confirmDel} onOpenChange={setConfirmDel} title="Delete sync target?" body={<span>This stops replicating this config to <b>{destination(t.addr)}</b>. The destination is left as-is.</span>} confirmLabel="Delete" tone="danger" onConfirm={() => del.mutate()} />
    </tr>
  )
}
