import { useEffect, useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Sheet } from '../ui/Sheet'
import { Button } from '../ui/Button'
import { Skeleton } from '../ui/Skeleton'
import { EmptyState } from '../ui/EmptyState'
import { ConfirmDialog } from '../ui/ConfirmDialog'
import { useToast } from '../ui/Toast'
import { apiErrorTitle } from '../lib/api'
import { useRowSelection } from '../lib/useRowSelection'
import { opsEndpoints, type DynamicLeaseView } from './endpoints'
import { StatusPill, RelTime } from './ops-ui'
import { OpsSelectionBar } from './OpsSelectionBar'

function isTerminal(l: DynamicLeaseView): boolean {
  return l.status === 'revoked' || l.status === 'expired'
}

export function LeasesSheet({ roleId, roleName, onClose }: { roleId: string | null; roleName: string; onClose: () => void }) {
  const qc = useQueryClient()
  const toast = useToast()
  const sel = useRowSelection()
  const [confirmBulkRevoke, setConfirmBulkRevoke] = useState(false)
  const [bulkBusy, setBulkBusy] = useState(false)
  const q = useQuery({
    queryKey: ['ops', 'dynamic', 'leases', roleId],
    queryFn: () => opsEndpoints.dynamic.listLeases(roleId as string),
    enabled: !!roleId,
    refetchInterval: 15_000,
  })

  const leases = q.data ?? []
  // Only non-terminal leases are revocable/selectable.
  const selectableIds = leases.filter((l) => !isTerminal(l)).map((l) => l.id)
  useEffect(() => { sel.prune(selectableIds) }, [q.data]) // eslint-disable-line react-hooks/exhaustive-deps
  // Reset selection when the sheet closes.
  useEffect(() => { if (!roleId) sel.clear() }, [roleId]) // eslint-disable-line react-hooks/exhaustive-deps

  async function runBulkRevoke() {
    const targets = selectableIds.filter((id) => sel.isSelected(id))
    if (targets.length === 0) return
    setBulkBusy(true)
    const results = await Promise.allSettled(targets.map((id) => opsEndpoints.dynamic.revoke(id)))
    setBulkBusy(false)
    const failed = results.filter((r) => r.status === 'rejected').length
    const ok = results.length - failed
    qc.invalidateQueries({ queryKey: ['ops', 'dynamic', 'leases', roleId] })
    sel.clear()
    toast({ title: failed ? `Revoked ${ok} · ${failed} failed` : `Revoked ${ok}`, tone: failed ? 'danger' : 'success' })
  }

  return (
    <Sheet open={!!roleId} onOpenChange={(o) => { if (!o) onClose() }} title={`Leases · ${roleName}`}>
      {!roleId ? null : q.isLoading ? (
        <div className="space-y-2">{[0, 1, 2].map((i) => <Skeleton key={i} className="h-12 w-full" />)}</div>
      ) : leases.length === 0 ? (
        <EmptyState title="No leases" hint="Issue credentials to create one." />
      ) : (
        <>
          {sel.count > 0 && (
            <OpsSelectionBar
              count={sel.count}
              onClear={sel.clear}
              actions={[
                { label: 'Revoke', tone: 'danger', loading: bulkBusy, onClick: () => setConfirmBulkRevoke(true) },
              ]}
            />
          )}
          <ul className="space-y-2">
            {leases.map((l) => (
              <LeaseCard
                key={l.id}
                lease={l}
                roleId={roleId}
                selected={sel.isSelected(l.id)}
                onToggle={() => sel.toggle(l.id)}
              />
            ))}
          </ul>
          <ConfirmDialog
            open={confirmBulkRevoke}
            onOpenChange={setConfirmBulkRevoke}
            title={`Revoke ${sel.count} lease${sel.count === 1 ? '' : 's'}?`}
            body={<span>The DB users backing these leases lose access immediately.</span>}
            confirmLabel="Revoke"
            tone="danger"
            onConfirm={runBulkRevoke}
          />
        </>
      )}
    </Sheet>
  )
}

function LeaseCard({ lease, roleId, selected, onToggle }: {
  lease: DynamicLeaseView
  roleId: string
  selected: boolean
  onToggle: () => void
}) {
  const qc = useQueryClient()
  const toast = useToast()
  const invalidate = () => qc.invalidateQueries({ queryKey: ['ops', 'dynamic', 'leases', roleId] })
  const onErr = (e: unknown) => toast({ title: apiErrorTitle(e), tone: 'danger' })
  const renew = useMutation({ mutationFn: () => opsEndpoints.dynamic.renew(lease.id), onSuccess: () => { toast({ title: 'Lease renewed', tone: 'success' }); invalidate() }, onError: onErr })
  const revoke = useMutation({ mutationFn: () => opsEndpoints.dynamic.revoke(lease.id), onSuccess: () => { toast({ title: 'Lease revoked', tone: 'success' }); invalidate() }, onError: onErr })
  const terminal = isTerminal(lease)
  return (
    <li className="rounded border border-line bg-card p-2.5">
      <div className="flex items-center justify-between">
        <span className="flex items-center gap-2">
          <input
            type="checkbox"
            aria-label={`select ${lease.db_username}`}
            checked={selected}
            disabled={terminal}
            onChange={onToggle}
            className="accent-brand"
          />
          <span className="font-mono text-[12.5px] text-ink">{lease.db_username}</span>
        </span>
        <StatusPill status={lease.status} />
      </div>
      <div className="mt-1 text-[11px] text-ink-mute">Expires <RelTime iso={lease.expires_at} /> · max <RelTime iso={lease.max_expires_at} /></div>
      <div className="mt-2 flex justify-end gap-1">
        <Button size="sm" variant="ghost" disabled={terminal || lease.status !== 'active'} loading={renew.isPending} onClick={() => renew.mutate()}>Renew</Button>
        <Button size="sm" variant="danger" disabled={terminal} loading={revoke.isPending} onClick={() => revoke.mutate()}>Revoke</Button>
      </div>
    </li>
  )
}
