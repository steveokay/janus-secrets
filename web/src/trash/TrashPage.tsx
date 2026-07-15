import { useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Trash2 } from 'lucide-react'
import { endpoints } from '../lib/endpoints'
import type { Trash } from '../lib/endpoints'
import { ApiError } from '../lib/api'
import { Button } from '../ui/Button'
import { EmptyState } from '../ui/EmptyState'
import { Modal } from '../ui/Modal'
import { Skeleton } from '../ui/Skeleton'
import { useToast } from '../ui/Toast'
import { useTitle } from '../lib/title'
import { relativeTime } from '../lib/relativeTime'

type DestroyTarget = { name: string; run: () => void }

interface Row {
  key: string
  title: string
  sub?: string
  deletedAt: string
  onRestore: () => void
  onDestroy: DestroyTarget
}

function Section({ title, rows, onOpenDestroy }: {
  title: string; rows: Row[]; onOpenDestroy: (t: DestroyTarget) => void
}) {
  if (rows.length === 0) return null
  return (
    <section className="mb-6">
      <h3 className="mb-2 text-[10.5px] font-bold uppercase tracking-[.12em] text-ink-faint">{title}</h3>
      <ul className="flex flex-col gap-1.5">
        {rows.map((r) => (
          <li
            key={r.key}
            className="flex items-center gap-3 rounded-card border border-line bg-card px-4 py-2.5 shadow-elev-1"
          >
            <div className="min-w-0 flex-1">
              <div className="truncate text-[13px] font-semibold text-ink">{r.title}</div>
              {r.sub && <div className="truncate font-mono text-[11.5px] text-ink-faint">{r.sub}</div>}
            </div>
            <span className="shrink-0 text-[11.5px] text-ink-faint">deleted {relativeTime(r.deletedAt)}</span>
            <Button variant="secondary" size="sm" aria-label={`restore ${r.title}`} onClick={r.onRestore}>Restore</Button>
            <Button variant="danger" size="sm" aria-label={`destroy ${r.title}`} onClick={() => onOpenDestroy(r.onDestroy)}>Destroy</Button>
          </li>
        ))}
      </ul>
    </section>
  )
}

function DestroyModal({ target, onClose }: { target: DestroyTarget | null; onClose: () => void }) {
  const [typed, setTyped] = useState('')
  if (!target) return null
  const armed = typed === target.name
  return (
    <Modal open onClose={onClose} label={`Permanently destroy ${target.name}`}>
      <div className="flex flex-col gap-3 p-1">
        <p className="text-[13px] font-semibold text-ink">Permanently destroy “{target.name}”?</p>
        <p className="text-[12.5px] text-ink-mute">
          This is irreversible. Destroying a project also destroys its environments and configs.
          Type the name to confirm.
        </p>
        <label className="flex flex-col gap-1 text-[11px] uppercase tracking-[.08em] text-ink-faint">
          Type the name to confirm
          <input
            aria-label="type the name to confirm"
            value={typed}
            onChange={(e) => setTyped(e.target.value)}
            className="rounded border border-line bg-surface-3 px-2.5 py-1.5 font-mono text-[12.5px] normal-case tracking-normal text-ink focus:border-brand-line"
          />
        </label>
        <div className="flex justify-end gap-2">
          <Button variant="ghost" size="sm" onClick={onClose}>Cancel</Button>
          <Button variant="danger" size="sm" disabled={!armed} onClick={() => { target.run(); onClose() }}>
            Permanently destroy
          </Button>
        </div>
      </div>
    </Modal>
  )
}

export function TrashPage() {
  useTitle('Trash')
  const qc = useQueryClient()
  const toast = useToast()
  const [destroy, setDestroy] = useState<DestroyTarget | null>(null)
  const trash = useQuery({ queryKey: ['trash'], queryFn: endpoints.listTrash, retry: false })

  const refresh = () => {
    void qc.invalidateQueries({ queryKey: ['trash'] })
    void qc.invalidateQueries({ queryKey: ['projects'] })
    void qc.invalidateQueries({ queryKey: ['envs'] })
    void qc.invalidateQueries({ queryKey: ['configs'] })
  }
  const restore = useMutation({
    mutationFn: (fn: () => Promise<unknown>) => fn(),
    onSuccess: () => { toast({ title: 'Restored' }); refresh() },
    onError: () => toast({ title: 'Restore failed', tone: 'danger' }),
  })
  const purge = useMutation({
    mutationFn: (fn: () => Promise<unknown>) => fn(),
    onSuccess: () => { toast({ title: 'Permanently destroyed' }); refresh() },
    onError: () => toast({ title: 'Destroy failed', tone: 'danger' }),
  })

  const forbidden = trash.error instanceof ApiError && trash.error.status === 403
  const data: Trash = forbidden
    ? { projects: [], environments: [], configs: [] }
    : (trash.data ?? { projects: [], environments: [], configs: [] })

  if (trash.isLoading && !forbidden) {
    return (
      <div aria-hidden className="flex flex-col gap-1.5">
        {[0, 1, 2].map((i) => <Skeleton key={i} className="h-12 w-full rounded-card" />)}
      </div>
    )
  }
  if (trash.isError && !forbidden) {
    return <p role="alert" className="text-[12.5px] text-danger">Couldn't load Trash.</p>
  }

  const projectRows: Row[] = data.projects.map((p) => ({
    key: `p:${p.id}`, title: p.name, sub: p.slug, deletedAt: p.deleted_at,
    onRestore: () => restore.mutate(() => endpoints.restoreProject(p.id)),
    onDestroy: { name: p.name, run: () => purge.mutate(() => endpoints.destroyProject(p.id)) },
  }))
  const envRows: Row[] = data.environments.map((e) => ({
    key: `e:${e.id}`, title: e.name, sub: `${e.project_name} / ${e.name}`, deletedAt: e.deleted_at,
    onRestore: () => restore.mutate(() => endpoints.restoreEnvironment(e.project_id, e.id)),
    onDestroy: { name: e.name, run: () => purge.mutate(() => endpoints.destroyEnvironment(e.project_id, e.id)) },
  }))
  const configRows: Row[] = data.configs.map((c) => ({
    key: `c:${c.id}`, title: c.name, sub: `${c.environment_name} / ${c.name}`, deletedAt: c.deleted_at,
    onRestore: () => restore.mutate(() => endpoints.restoreConfig(c.id)),
    onDestroy: { name: c.name, run: () => purge.mutate(() => endpoints.destroyConfig(c.id)) },
  }))
  const empty = projectRows.length + envRows.length + configRows.length === 0

  return (
    <div>
      <div className="mb-4">
        <h2 className="text-[17px] font-semibold tracking-tight text-ink">Trash</h2>
        <p className="text-[12.5px] text-ink-faint">
          Soft-deleting a project keeps its environments and configs — restoring brings them back.
          Destroying is permanent and cascades.
        </p>
      </div>
      {empty ? (
        <EmptyState icon={<Trash2 size={22} strokeWidth={1.7} />} title="Trash is empty" />
      ) : (
        <>
          <Section title="Projects" rows={projectRows} onOpenDestroy={setDestroy} />
          <Section title="Environments" rows={envRows} onOpenDestroy={setDestroy} />
          <Section title="Configs" rows={configRows} onOpenDestroy={setDestroy} />
        </>
      )}
      <DestroyModal target={destroy} onClose={() => setDestroy(null)} />
    </div>
  )
}
