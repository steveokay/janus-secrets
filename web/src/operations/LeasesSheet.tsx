import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Sheet } from '../ui/Sheet'
import { Button } from '../ui/Button'
import { Skeleton } from '../ui/Skeleton'
import { EmptyState } from '../ui/EmptyState'
import { useToast } from '../ui/Toast'
import { apiErrorTitle } from '../lib/api'
import { opsEndpoints, type DynamicLeaseView } from './endpoints'
import { StatusPill, RelTime } from './ops-ui'

export function LeasesSheet({ roleId, roleName, onClose }: { roleId: string | null; roleName: string; onClose: () => void }) {
  const q = useQuery({
    queryKey: ['ops', 'dynamic', 'leases', roleId],
    queryFn: () => opsEndpoints.dynamic.listLeases(roleId as string),
    enabled: !!roleId,
    refetchInterval: 15_000,
  })
  return (
    <Sheet open={!!roleId} onOpenChange={(o) => { if (!o) onClose() }} title={`Leases · ${roleName}`}>
      {!roleId ? null : q.isLoading ? (
        <div className="space-y-2">{[0, 1, 2].map((i) => <Skeleton key={i} className="h-12 w-full" />)}</div>
      ) : (q.data ?? []).length === 0 ? (
        <EmptyState title="No leases" hint="Issue credentials to create one." />
      ) : (
        <ul className="space-y-2">
          {(q.data ?? []).map((l) => <LeaseCard key={l.id} lease={l} roleId={roleId} />)}
        </ul>
      )}
    </Sheet>
  )
}

function LeaseCard({ lease, roleId }: { lease: DynamicLeaseView; roleId: string }) {
  const qc = useQueryClient()
  const toast = useToast()
  const invalidate = () => qc.invalidateQueries({ queryKey: ['ops', 'dynamic', 'leases', roleId] })
  const onErr = (e: unknown) => toast({ title: apiErrorTitle(e), tone: 'danger' })
  const renew = useMutation({ mutationFn: () => opsEndpoints.dynamic.renew(lease.id), onSuccess: () => { toast({ title: 'Lease renewed', tone: 'success' }); invalidate() }, onError: onErr })
  const revoke = useMutation({ mutationFn: () => opsEndpoints.dynamic.revoke(lease.id), onSuccess: () => { toast({ title: 'Lease revoked', tone: 'success' }); invalidate() }, onError: onErr })
  const terminal = lease.status === 'revoked' || lease.status === 'expired'
  return (
    <li className="rounded border border-line bg-card p-2.5">
      <div className="flex items-center justify-between">
        <span className="font-mono text-[12.5px] text-ink">{lease.db_username}</span>
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
