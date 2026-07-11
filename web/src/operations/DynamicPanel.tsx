import { useState } from 'react'
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { Button } from '../ui/Button'
import { ConfirmDialog } from '../ui/ConfirmDialog'
import { useToast } from '../ui/Toast'
import { apiErrorTitle } from '../lib/api'
import { opsEndpoints, type DynamicRoleView, type IssuedCreds } from './endpoints'
import { useDynamicRoles, type EngineRow, type ProjectFilter } from './useAggregated'
import { OpsTable } from './ops-ui'
import { IssuedCredsModal } from './IssuedCredsModal'
import { LeasesSheet } from './LeasesSheet'

export function DynamicPanel({ filter }: { filter: ProjectFilter }) {
  const { rows, isLoading, isError, someForbidden } = useDynamicRoles(filter)
  const [issued, setIssued] = useState<IssuedCreds | null>(null)
  const [leasesFor, setLeasesFor] = useState<{ id: string; name: string } | null>(null)

  return (
    <>
      <OpsTable
        columns={['Project', 'Config', 'Role', 'Default TTL', 'Max TTL', '']}
        isLoading={isLoading}
        isError={isError}
        allForbidden={someForbidden && rows.length === 0}
        isEmpty={rows.length === 0}
        someForbidden={someForbidden}
        forbiddenHint="Listing dynamic roles needs the dynamic:manage role (admin/owner)."
        emptyHint="No dynamic roles. Create one with `janus dynamic roles create`."
      >
        {rows.map((r) => (
          <DynamicRow key={r.data.id} row={r} onIssued={setIssued} onViewLeases={(id, name) => setLeasesFor({ id, name })} />
        ))}
      </OpsTable>
      <IssuedCredsModal creds={issued} onClose={() => setIssued(null)} />
      <LeasesSheet roleId={leasesFor?.id ?? null} roleName={leasesFor?.name ?? ''} onClose={() => setLeasesFor(null)} />
    </>
  )
}

function DynamicRow({
  row, onIssued, onViewLeases,
}: {
  row: EngineRow<DynamicRoleView>
  onIssued: (c: IssuedCreds) => void
  onViewLeases: (id: string, name: string) => void
}) {
  const qc = useQueryClient()
  const toast = useToast()
  const r = row.data
  const [confirmDel, setConfirmDel] = useState(false)
  const onErr = (e: unknown) => toast({ title: apiErrorTitle(e), tone: 'danger' })

  const issue = useMutation({
    mutationFn: () => opsEndpoints.dynamic.issue(r.id),
    onSuccess: (creds) => {
      onIssued(creds)
      qc.invalidateQueries({ queryKey: ['ops', 'dynamic', 'leases', r.id] })
    },
    onError: onErr,
  })
  const del = useMutation({
    mutationFn: () => opsEndpoints.dynamic.deleteRole(r.id),
    onSuccess: () => { toast({ title: 'Role deleted', tone: 'success' }); qc.invalidateQueries({ queryKey: ['ops', 'dynamic', 'roles'] }) },
    onError: onErr,
  })

  return (
    <tr className="border-b border-line-soft">
      <td className="px-2 py-1.5">{row.projectName}</td>
      <td className="px-2 py-1.5">{row.cfg ? `${row.cfg.envName}/${row.cfg.configName}` : '—'}</td>
      <td className="px-2 py-1.5 font-mono">{r.name}</td>
      <td className="px-2 py-1.5">{r.default_ttl_seconds}s</td>
      <td className="px-2 py-1.5">{r.max_ttl_seconds}s</td>
      <td className="px-2 py-1.5">
        <div className="flex justify-end gap-1">
          <Button size="sm" variant="secondary" loading={issue.isPending} onClick={() => issue.mutate()}>Issue</Button>
          <Button size="sm" variant="ghost" onClick={() => onViewLeases(r.id, r.name)}>Leases</Button>
          <Button size="sm" variant="ghost" onClick={() => setConfirmDel(true)}>Delete</Button>
        </div>
      </td>
      <ConfirmDialog
        open={confirmDel}
        onOpenChange={setConfirmDel}
        title="Delete dynamic role?"
        body={<span>This revokes every live lease for <b>{r.name}</b> first, then removes the role.</span>}
        confirmLabel="Delete"
        tone="danger"
        onConfirm={() => del.mutate()}
      />
    </tr>
  )
}
