import { useEffect, useMemo, useState } from 'react'
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { Plus } from 'lucide-react'
import { Button } from '../ui/Button'
import { Pill } from '../ui/Pill'
import { Sheet } from '../ui/Sheet'
import { Input } from '../ui/Input'
import { Textarea } from '../ui/Textarea'
import { ConfirmDialog } from '../ui/ConfirmDialog'
import { useToast } from '../ui/Toast'
import { apiErrorTitle, errorMessage } from '../lib/api'
import { useRowSelection } from '../lib/useRowSelection'
import { opsEndpoints, type DynamicRoleView, type DynamicRoleCreateInput, type IssuedCreds } from './endpoints'
import { useDynamicRoles, type EngineRow, type ProjectFilter } from './useAggregated'
import { ConfigPicker } from './ConfigPicker'
import { OpsTable, type OpsColumn, type OpsSort } from './ops-ui'
import { OpsSelectionBar } from './OpsSelectionBar'
import { IssuedCredsModal } from './IssuedCredsModal'
import { LeasesSheet } from './LeasesSheet'

const COLUMNS: OpsColumn[] = [
  { label: 'Project', key: 'project' },
  { label: 'Config', key: 'config' },
  { label: 'Role', key: 'role' },
  { label: 'Default TTL', key: 'default_ttl' },
  { label: 'Max TTL', key: 'max_ttl' },
]

// Next-cycle sort idiom (mirror the editor): none → asc → desc → none.
function cycleSort(prev: OpsSort, key: string): OpsSort {
  if (prev?.key !== key) return { key, dir: 'asc' }
  if (prev.dir === 'asc') return { key, dir: 'desc' }
  return null
}

function cfgLabel(r: EngineRow<DynamicRoleView>): string {
  return r.cfg ? `${r.cfg.envName}/${r.cfg.configName}` : ''
}

function compare(a: EngineRow<DynamicRoleView>, b: EngineRow<DynamicRoleView>, key: string): number {
  const s = (x: string, y: string) => x.localeCompare(y)
  switch (key) {
    case 'project': return s(a.projectName, b.projectName)
    case 'config': return s(cfgLabel(a), cfgLabel(b))
    case 'role': return s(a.data.name, b.data.name)
    case 'default_ttl': return a.data.default_ttl_seconds - b.data.default_ttl_seconds
    case 'max_ttl': return a.data.max_ttl_seconds - b.data.max_ttl_seconds
    default: return 0
  }
}

export function DynamicPanel({ filter }: { filter: ProjectFilter }) {
  const { rows, isLoading, isError, someForbidden } = useDynamicRoles(filter)
  const [issued, setIssued] = useState<IssuedCreds | null>(null)
  const [leasesFor, setLeasesFor] = useState<{ id: string; name: string } | null>(null)
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
      // Roles are stateless (no status) — default by project then role name.
      return list.sort((a, b) => {
        const p = a.projectName.localeCompare(b.projectName)
        if (p !== 0) return p
        return a.data.name.localeCompare(b.data.name)
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
    qc.invalidateQueries({ queryKey: ['ops', 'dynamic'] })
    sel.clear()
    toast({ title: failed ? `${label} ${ok} · ${failed} failed` : `${label} ${ok}`, tone: failed ? 'danger' : 'success' })
  }

  return (
    <div className="flex flex-col gap-3">
      <div className="flex justify-end">
        <Button variant="secondary" size="sm" onClick={() => setCreating(true)}>
          <Plus size={13} strokeWidth={1.8} /> New role
        </Button>
      </div>
      {sel.count > 0 && (
        <OpsSelectionBar
          count={sel.count}
          onClear={sel.clear}
          actions={[
            { label: 'Delete role', tone: 'danger', loading: bulkBusy, onClick: () => setConfirmBulkDel(true) },
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
        forbiddenHint="Listing dynamic roles needs the dynamic:manage role (admin/owner)."
        emptyHint="No dynamic roles yet."
        sort={sort}
        onSort={(key) => setSort((prev) => cycleSort(prev, key))}
        selectable
        allSelected={allSelected}
        onToggleAll={() => sel.setAll(ids)}
      >
        {sorted.map((r) => (
          <DynamicRow
            key={r.data.id}
            row={r}
            selected={sel.isSelected(r.data.id)}
            onToggle={() => sel.toggle(r.data.id)}
            onIssued={setIssued}
            onViewLeases={(id, name) => setLeasesFor({ id, name })}
          />
        ))}
      </OpsTable>
      <CreateRoleSheet open={creating} onOpenChange={setCreating} filter={filter} />
      <IssuedCredsModal creds={issued} onClose={() => setIssued(null)} />
      <LeasesSheet roleId={leasesFor?.id ?? null} roleName={leasesFor?.name ?? ''} onClose={() => setLeasesFor(null)} />
      <ConfirmDialog
        open={confirmBulkDel}
        onOpenChange={setConfirmBulkDel}
        title={`Delete ${sel.count} dynamic role${sel.count === 1 ? '' : 's'}?`}
        body={<span>This revokes each role's live leases first, then removes the role.</span>}
        confirmLabel="Delete"
        tone="danger"
        onConfirm={() => runBulk('Deleted', (id) => opsEndpoints.dynamic.deleteRole(id))}
      />
    </div>
  )
}

// Write-only create form. The secret field (admin_dsn) lives ONLY in this
// ephemeral state, is sent once in the POST body, and is never rendered from a
// fetched value (list + create responses are masked — no admin_dsn). The SQL
// templates are admin input (not secrets); {{password}} in them is the literal
// interpolation marker, not a real credential.
interface RoleForm {
  config_id: string
  name: string
  default_ttl_seconds: number
  max_ttl_seconds: number
  admin_dsn: string
  creation_statements: string
  revocation_statements: string
  renew_statements: string
}

function emptyRoleForm(): RoleForm {
  return {
    config_id: '', name: '', default_ttl_seconds: 3600, max_ttl_seconds: 86400,
    admin_dsn: '', creation_statements: '', revocation_statements: '', renew_statements: '',
  }
}

function CreateRoleSheet({ open, onOpenChange, filter }: {
  open: boolean
  onOpenChange: (open: boolean) => void
  filter: ProjectFilter
}) {
  const qc = useQueryClient()
  const toast = useToast()
  const [form, setForm] = useState<RoleForm>(emptyRoleForm)

  // Reset the form (secret → '') whenever the Sheet opens.
  useEffect(() => {
    if (open) setForm(emptyRoleForm())
  }, [open])

  const create = useMutation({
    mutationFn: (body: DynamicRoleCreateInput) => opsEndpoints.dynamic.createRole(body),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['ops', 'dynamic'] })
      setForm(emptyRoleForm())
      onOpenChange(false)
      toast({ title: 'Role created', tone: 'success' })
    },
    onError: (e) => toast({ title: errorMessage(e), tone: 'danger' }),
  })

  const canCreate = !!form.config_id && !!form.name.trim() && !!form.admin_dsn &&
    !!form.creation_statements.trim() &&
    form.default_ttl_seconds >= 1 && form.max_ttl_seconds >= 1

  function submit() {
    const config: DynamicRoleCreateInput['config'] = {
      admin_dsn: form.admin_dsn,
      creation_statements: form.creation_statements.trim(),
    }
    if (form.revocation_statements.trim()) config.revocation_statements = form.revocation_statements.trim()
    if (form.renew_statements.trim()) config.renew_statements = form.renew_statements.trim()
    create.mutate({
      config_id: form.config_id,
      name: form.name.trim(),
      default_ttl_seconds: form.default_ttl_seconds,
      max_ttl_seconds: form.max_ttl_seconds,
      config,
    })
  }

  // Reset lifecycle: the open-edge useEffect re-seeds an empty form on every
  // open (+onSuccess), so close paths just close — no redundant reset here.
  function cancel() {
    onOpenChange(false)
  }

  return (
    <Sheet open={open} onOpenChange={onOpenChange} title="New dynamic role">
      <div className="flex flex-col gap-3">
        <ConfigPicker filter={filter} value={form.config_id} onChange={(id) => setForm((f) => ({ ...f, config_id: id }))} />

        <Input
          label="Name"
          value={form.name}
          onChange={(e) => setForm((f) => ({ ...f, name: e.target.value }))}
          placeholder="readonly"
          autoComplete="off"
          className="font-mono"
        />

        <Input
          label="Default TTL (seconds)"
          type="number"
          min={1}
          value={String(form.default_ttl_seconds)}
          onChange={(e) => setForm((f) => ({ ...f, default_ttl_seconds: Number(e.target.value) }))}
          autoComplete="off"
        />
        <Input
          label="Max TTL (seconds)"
          type="number"
          min={1}
          value={String(form.max_ttl_seconds)}
          onChange={(e) => setForm((f) => ({ ...f, max_ttl_seconds: Number(e.target.value) }))}
          autoComplete="off"
        />

        <Input
          label="Admin DSN"
          type="password"
          autoComplete="off"
          value={form.admin_dsn}
          onChange={(e) => setForm((f) => ({ ...f, admin_dsn: e.target.value }))}
          placeholder="postgres://admin@host/db"
        />

        <div className="flex flex-col gap-1.5">
          <div className="flex flex-wrap items-center gap-1.5">
            <span className="text-[11px] text-ink-mute">Placeholders:</span>
            <Pill tone="muted" className="font-mono">{'{{name}}'}</Pill>
            <Pill tone="muted" className="font-mono">{'{{password}}'}</Pill>
            <Pill tone="muted" className="font-mono">{'{{expiration}}'}</Pill>
          </div>
          <Textarea
            label="Creation statements"
            value={form.creation_statements}
            onChange={(e) => setForm((f) => ({ ...f, creation_statements: e.target.value }))}
            placeholder={'CREATE ROLE "{{name}}" LOGIN PASSWORD \'{{password}}\' VALID UNTIL \'{{expiration}}\';'}
            rows={4}
            autoComplete="off"
            spellCheck={false}
            className="font-mono"
          />
        </div>

        <Textarea
          label="Revocation statements (optional)"
          value={form.revocation_statements}
          onChange={(e) => setForm((f) => ({ ...f, revocation_statements: e.target.value }))}
          placeholder={'DROP ROLE "{{name}}";'}
          rows={2}
          autoComplete="off"
          spellCheck={false}
          className="font-mono"
        />
        <Textarea
          label="Renew statements (optional)"
          value={form.renew_statements}
          onChange={(e) => setForm((f) => ({ ...f, renew_statements: e.target.value }))}
          placeholder={'ALTER ROLE "{{name}}" VALID UNTIL \'{{expiration}}\';'}
          rows={2}
          autoComplete="off"
          spellCheck={false}
          className="font-mono"
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

function DynamicRow({
  row, selected, onToggle, onIssued, onViewLeases,
}: {
  row: EngineRow<DynamicRoleView>
  selected: boolean
  onToggle: () => void
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
      issue.reset() // drop the plaintext from the mutation object; parent state now owns it (cleared on modal close)
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
    <tr className="border-b border-line-soft hover:bg-row-hover transition-nocturne">
      <td className="px-2 py-1.5">
        <input
          type="checkbox"
          aria-label={`select ${r.name}`}
          checked={selected}
          onChange={onToggle}
          className="accent-brand"
        />
      </td>
      <td className="px-2 py-1.5">{row.projectName}</td>
      <td className="px-2 py-1.5">{row.cfg ? `${row.cfg.envName}/${row.cfg.configName}` : '—'}</td>
      <td className="px-2 py-1.5 font-mono">{r.name}</td>
      <td className="px-2 py-1.5">{r.default_ttl_seconds}s</td>
      <td className="px-2 py-1.5">{r.max_ttl_seconds}s</td>
      <td className="px-2 py-1.5">
        <div className="flex justify-end gap-1">
          <Button size="sm" variant="secondary" loading={issue.isPending} onClick={() => issue.mutate()}>Issue</Button>
          <Button size="sm" variant="ghost" onClick={() => onViewLeases(r.id, r.name)}>Leases</Button>
          <Button size="sm" variant="danger" onClick={() => setConfirmDel(true)}>Delete</Button>
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
