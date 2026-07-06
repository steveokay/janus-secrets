import { useState } from 'react'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { endpoints, VersionMeta } from '../lib/endpoints'
import { Pill } from '../ui/Pill'
import { ConfirmDialog } from '../ui/ConfirmDialog'
import { useToast } from '../ui/Toast'
import { timeAgo } from '../lib/time'
import { cn } from '../ui/cn'

function DiffView({ cid, version }: { cid: string; version: number }) {
  // Key NAMES only — the server never returns values on this surface.
  const diff = useQuery({
    queryKey: ['config', cid, 'diff', version - 1, version],
    queryFn: () => endpoints.diffVersions(cid, version - 1, version),
  })
  if (diff.isLoading) return <p className="text-[12px] text-faint">Loading…</p>
  if (diff.isError) return <p className="text-[12px] text-danger">Couldn't load diff.</p>
  const d = diff.data!
  const groups = [
    { label: 'Added', keys: d.added, tone: 'success' as const },
    { label: 'Changed', keys: d.changed, tone: 'warning' as const },
    { label: 'Removed', keys: d.removed, tone: 'danger' as const },
  ].filter((g) => g.keys.length > 0)
  if (!groups.length) return <p className="text-[12px] text-faint">No key changes</p>
  return (
    <div className="flex flex-col gap-2">
      {groups.map((g) => (
        <div key={g.label}>
          <p className="mb-1 text-[10.5px] font-bold uppercase tracking-[.1em] text-faint">{g.label}</p>
          <div className="flex flex-wrap gap-1">
            {g.keys.map((k) => <Pill key={k} tone={g.tone} className="font-mono text-[11px]">{k}</Pill>)}
          </div>
        </div>
      ))}
    </div>
  )
}

export function VersionHistory({ cid, dirty }: { cid: string; dirty: boolean }) {
  const qc = useQueryClient()
  const toast = useToast()
  const versions = useQuery({ queryKey: ['config', cid, 'versions'], queryFn: () => endpoints.listVersions(cid) })
  const [openDiff, setOpenDiff] = useState<number | null>(null)
  const [confirming, setConfirming] = useState<VersionMeta | null>(null)

  const rollback = useMutation({
    mutationFn: (v: VersionMeta) => endpoints.rollback(cid, v.version, `Rollback to v${v.version}`),
    onSuccess: (res, v) => {
      toast({ title: `Rolled back to v${v.version} — saved as v${res.version}` })
      void qc.invalidateQueries({ queryKey: ['config', cid] })
    },
    onError: () => toast({ title: 'Rollback failed.', tone: 'danger' }),
  })

  if (versions.isLoading) return <p className="text-[12.5px] text-faint">Loading…</p>
  if (versions.isError) return <p role="alert" className="text-[12.5px] text-danger">Couldn't load versions.</p>
  const list = [...(versions.data ?? [])].sort((x, y) => y.version - x.version)
  const latest = list[0]?.version

  return (
    <ul className="flex flex-col gap-1.5">
      {list.map((v) => (
        <li key={v.version} className="rounded-card border border-line-soft">
          <button
            type="button"
            onClick={() => setOpenDiff((s) => (s === v.version ? null : v.version))}
            className="flex w-full items-center gap-2 px-3 py-2 text-left hover:bg-line-soft/50"
          >
            <Pill tone={v.version === latest ? 'success' : 'brand'}>v{v.version}</Pill>
            <span className={cn('flex-1 truncate text-[13px]', v.message ? 'text-ink' : 'text-faint')}>
              {v.message || 'no message'}
            </span>
          </button>
          <div className="flex items-center justify-between px-3 pb-2 text-[11.5px] text-faint">
            <span>{v.created_by} · {timeAgo(v.created_at)}</span>
            {v.version === latest ? (
              <span className="text-[10.5px] font-bold uppercase tracking-[.1em]">current</span>
            ) : (
              <button
                type="button"
                disabled={dirty || rollback.isPending}
                title={dirty ? 'Save or discard your changes first' : undefined}
                onClick={() => setConfirming(v)}
                className="rounded border border-line bg-card px-2 py-0.5 text-[11.5px] font-semibold disabled:opacity-40"
              >
                Roll back
              </button>
            )}
          </div>
          {openDiff === v.version && (
            <div className="border-t border-line-soft px-3 py-2">
              {v.version === 1
                ? <p className="text-[12px] text-faint">Initial version</p>
                : <DiffView cid={cid} version={v.version} />}
            </div>
          )}
        </li>
      ))}
      <ConfirmDialog
        open={confirming !== null}
        onOpenChange={(o) => { if (!o) setConfirming(null) }}
        title={`Roll back to v${confirming?.version}?`}
        body={`This creates a new version that restores v${confirming?.version}'s keys — nothing is deleted.`}
        confirmLabel="Roll back"
        onConfirm={() => { if (confirming) rollback.mutate(confirming); setConfirming(null) }}
      />
    </ul>
  )
}
