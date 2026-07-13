import { useState } from 'react'
import { Link, useParams } from 'react-router-dom'
import { useQueries, useQuery } from '@tanstack/react-query'
import { Lock, Plus, Layers } from 'lucide-react'
import { endpoints, Config, Environment } from '../lib/endpoints'
import { useProjects, useEnvironments } from '../secrets/nav'
import { envTone, envDotClass } from '../ui/env'
import { EmptyState } from '../ui/EmptyState'
import { Pill } from '../ui/Pill'
import { cn } from '../ui/cn'
import { useTitle } from '../lib/title'
import { relativeTime } from '../lib/relativeTime'
import { opsEndpoints, type RotationView, type SyncView } from '../operations/endpoints'
import { CreateEnvironmentForm, CreateConfigForm } from '../structure/CreateForms'
import { ProjectReadsStrip } from '../metrics/ReadsStrip'

// Ops health lookups keyed by config_id, threaded down to config cards.
// Both queries are 403-tolerant (see useOpsHealth): on error the maps are
// simply empty, so cards render with no chips — never an error state.
interface OpsHealth {
  rotation: Map<string, RotationView[]>
  sync: Map<string, SyncView[]>
}

function OpsChips({ configId, ops }: { configId: string; ops: OpsHealth }) {
  const rot = ops.rotation.get(configId)
  const syncs = ops.sync.get(configId)
  const rotFailed = rot?.some((p) => p.status === 'failed') ?? false
  const syncFailed = syncs?.some((t) => t.status === 'failed') ?? false
  return (
    <>
      {rot && rot.length > 0 && (
        <Pill tone={rotFailed ? 'warning' : 'success'} className="text-[10px]">
          rotation{rotFailed ? ' ⚠' : ' ✓'}
          <span className="sr-only">{rotFailed ? ' has failing policies' : ' healthy'}</span>
        </Pill>
      )}
      {syncs && syncs.length > 0 && (
        <Pill tone={syncFailed ? 'warning' : 'success'} className="text-[10px]">
          sync{syncFailed ? ' ⚠' : ' ✓'}
          <span className="sr-only">{syncFailed ? ' has failing targets' : ' healthy'}</span>
        </Pill>
      )}
    </>
  )
}

function ConfigCard({ pid, config, depth, ops }: { pid: string; config: Config; depth: number; ops: OpsHealth }) {
  return (
    <Link
      to={`/projects/${pid}/configs/${config.id}`}
      data-inherited={config.inherits_from ? 'true' : undefined}
      className={cn(
        'flex flex-col gap-1 rounded border border-line bg-card px-3 py-2 hover:border-brand-line',
        depth > 0 && 'ml-4',
      )}
    >
      <div className="flex items-center gap-2">
        {depth > 0 && <span className="text-[11px] text-info">↳</span>}
        <Lock size={12} strokeWidth={1.7} className="text-ink-faint" />
        <span className="font-mono text-[12.5px] text-ink">{config.name}</span>
      </div>
      <div className="flex flex-wrap items-center gap-1.5">
        <OpsChips configId={config.id} ops={ops} />
        <span className="text-[10px] text-ink-faint">created {relativeTime(config.created_at)}</span>
      </div>
    </Link>
  )
}

// `seen` = the ancestor id path; a child already on the path is dropped so a
// cyclic `inherits_from` (shouldn't reach the DB, but be defensive) can never
// recurse forever.
function ConfigNodes({ pid, roots, all, depth, ops, seen = new Set<string>() }: {
  pid: string; roots: Config[]; all: Config[]; depth: number; ops: OpsHealth; seen?: Set<string>
}) {
  return (
    <>
      {roots.map((c) => {
        if (seen.has(c.id)) return null
        const next = new Set(seen).add(c.id)
        const children = all.filter((x) => x.inherits_from === c.id && !next.has(x.id))
        return (
          <div key={c.id} className="flex flex-col gap-1.5">
            <ConfigCard pid={pid} config={c} depth={depth} ops={ops} />
            <ConfigNodes pid={pid} roots={children} all={all} depth={depth + 1} ops={ops} seen={next} />
          </div>
        )
      })}
    </>
  )
}

function EnvColumn({ pid, env, configs, loading, error, ops, onAddConfig }: {
  pid: string
  env: Environment
  configs: Config[]
  loading: boolean
  error: boolean
  ops: OpsHealth
  onAddConfig: (env: Environment, bases: Config[]) => void
}) {
  const tone = envTone(env.name)
  const roots = configs.filter((c) => !c.inherits_from || !configs.some((x) => x.id === c.inherits_from))
  const count = loading ? '…' : error ? '—' : `${configs.length} config${configs.length === 1 ? '' : 's'}`
  return (
    <section
      className={cn(
        'w-[260px] shrink-0 rounded-card border p-2',
        tone === 'danger' ? 'border-danger-line' : 'border-transparent',
      )}
    >
      <div className="mb-2 flex items-center justify-between">
        <h3 className="text-[13px] font-semibold text-ink">{env.name}</h3>
        <Pill tone="muted">{count}</Pill>
      </div>
      <div className={cn('mb-3 h-[3px] w-10 rounded-full', envDotClass[tone])} />
      <button
        type="button"
        onClick={() => onAddConfig(env, configs)}
        className="mb-2 flex w-full items-center justify-center gap-1.5 rounded border border-dashed border-line py-2 text-[12px] font-semibold text-ink-faint hover:border-brand-line hover:text-brand-text"
      >
        <Plus size={13} strokeWidth={1.7} /> Add config
      </button>
      <div className="flex flex-col gap-1.5">
        {loading && <div aria-hidden className="h-9 rounded bg-line-soft" />}
        {error && <p role="alert" className="px-1 text-[12px] text-danger">Couldn't load configs.</p>}
        {!loading && !error && configs.length === 0 && <p className="px-1 text-[12px] text-ink-faint">No configs yet</p>}
        {!error && <ConfigNodes pid={pid} roots={roots} all={configs} depth={0} ops={ops} />}
      </div>
    </section>
  )
}

/**
 * Project-scoped rotation/sync health, keyed by config_id. Shares the same
 * queryKey prefixes (['ops','rotation',pid] / ['ops','sync',pid]) as
 * useFanOut in web/src/operations/useAggregated.ts so the cache entry is
 * reused with /operations and the home StatCards fan-outs. 403-tolerant:
 * on any error the map is simply empty (no chips), never an error state.
 */
function useOpsHealth(pid: string): OpsHealth {
  const rotationQ = useQuery({ queryKey: ['ops', 'rotation', pid], queryFn: () => opsEndpoints.rotation.list(pid), retry: false })
  const syncQ = useQuery({ queryKey: ['ops', 'sync', pid], queryFn: () => opsEndpoints.sync.list(pid), retry: false })

  const rotation = new Map<string, RotationView[]>()
  for (const p of rotationQ.data ?? []) {
    const list = rotation.get(p.config_id) ?? []
    list.push(p)
    rotation.set(p.config_id, list)
  }
  const sync = new Map<string, SyncView[]>()
  for (const t of syncQ.data ?? []) {
    const list = sync.get(t.config_id) ?? []
    list.push(t)
    sync.set(t.config_id, list)
  }
  return { rotation, sync }
}

export function ProjectBoard() {
  const { projectId } = useParams()
  const pid = projectId!
  const projects = useProjects()
  const envs = useEnvironments(pid)
  const project = projects.data?.find((p) => p.id === pid)
  useTitle(project?.name)
  const [creatingEnv, setCreatingEnv] = useState(false)
  const [addConfig, setAddConfig] = useState<null | { env: Environment; bases: Config[] }>(null)

  const configLists = useQueries({
    queries: (envs.data ?? []).map((e) => ({
      queryKey: ['configs', pid, e.id],
      queryFn: () => endpoints.listConfigs(pid, e.id),
    })),
  })
  const ops = useOpsHealth(pid)

  if (envs.isError) {
    return <p role="alert" className="mt-16 text-center text-danger">Could not load environments.</p>
  }

  return (
    <div>
      <h1 className="sr-only">{project?.name ?? 'Project'}</h1>
      <div className="mb-1 flex items-center gap-2 text-[13px]">
        <Link to="/projects" className="text-ink-mute hover:text-ink">Projects</Link>
        <span className="text-ink-faint">/</span>
        <span className="font-semibold text-ink">{project?.name ?? '…'}</span>
      </div>
      <p className="mb-5 text-[12.5px] text-ink-faint">
        Inject secrets with the Janus CLI — <code className="rounded bg-brand-soft px-1.5 py-0.5 font-mono text-[11.5px] text-brand-text">janus run</code>
      </p>
      <ProjectReadsStrip pid={pid} />

      {envs.isPending ? (
        <div className="flex gap-5 overflow-x-auto pb-2" aria-hidden>
          {[0, 1, 2].map((i) => (
            <div key={i} className="h-40 w-[260px] shrink-0 rounded-card bg-line-soft" />
          ))}
        </div>
      ) : envs.data?.length === 0 ? (
        <EmptyState
          icon={<Layers size={22} strokeWidth={1.7} />}
          title="No environments yet"
          hint="Environments hold your configs — dev, staging, prod."
          action={
            <button onClick={() => setCreatingEnv(true)} className="rounded bg-brand px-4 py-2 text-[13px] font-semibold text-white shadow-elev-1">
              Create environment
            </button>
          }
        />
      ) : (
        <div className="flex gap-5 overflow-x-auto pb-2">
          {envs.data?.map((e, i) => (
            <EnvColumn
              key={e.id}
              pid={pid}
              env={e}
              configs={configLists[i]?.data ?? []}
              loading={configLists[i]?.isPending ?? true}
              error={configLists[i]?.isError ?? false}
              ops={ops}
              onAddConfig={(env, bases) => setAddConfig({ env, bases })}
            />
          ))}
          <button
            type="button"
            onClick={() => setCreatingEnv(true)}
            className="flex h-9 shrink-0 items-center gap-1.5 self-start rounded border border-dashed border-line px-3 text-[12px] font-semibold text-ink-faint hover:border-brand-line hover:text-brand-text"
          >
            <Plus size={13} strokeWidth={1.7} /> Add environment
          </button>
        </div>
      )}

      {creatingEnv && (
        <CreateEnvironmentForm pid={pid} onCreated={() => setCreatingEnv(false)} onClose={() => setCreatingEnv(false)} />
      )}
      {addConfig && (
        <CreateConfigForm
          pid={pid}
          eid={addConfig.env.id}
          bases={addConfig.bases}
          onCreated={() => setAddConfig(null)}
          onClose={() => setAddConfig(null)}
        />
      )}
    </div>
  )
}
