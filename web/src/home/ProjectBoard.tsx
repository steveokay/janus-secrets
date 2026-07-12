import { useState } from 'react'
import { Link, useParams } from 'react-router-dom'
import { useQueries } from '@tanstack/react-query'
import { Lock, Plus, Layers } from 'lucide-react'
import { endpoints, Config, Environment } from '../lib/endpoints'
import { useProjects, useEnvironments } from '../secrets/nav'
import { envTone, envDotClass } from '../ui/env'
import { EmptyState } from '../ui/EmptyState'
import { Pill } from '../ui/Pill'
import { cn } from '../ui/cn'
import { useTitle } from '../lib/title'
import { CreateEnvironmentForm, CreateConfigForm } from '../structure/CreateForms'
import { ProjectReadsStrip } from '../metrics/ReadsStrip'

function ConfigCard({ pid, config, depth }: { pid: string; config: Config; depth: number }) {
  return (
    <Link
      to={`/projects/${pid}/configs/${config.id}`}
      data-inherited={config.inherits_from ? 'true' : undefined}
      className={cn(
        'flex items-center gap-2 rounded border border-line bg-card px-3 py-2 hover:border-brand-line',
        depth > 0 && 'ml-4',
      )}
    >
      {depth > 0 && <span className="text-[11px] text-info">↳</span>}
      <Lock size={12} strokeWidth={1.7} className="text-faint" />
      <span className="font-mono text-[12.5px] text-ink">{config.name}</span>
    </Link>
  )
}

// `seen` = the ancestor id path; a child already on the path is dropped so a
// cyclic `inherits_from` (shouldn't reach the DB, but be defensive) can never
// recurse forever.
function ConfigNodes({ pid, roots, all, depth, seen = new Set<string>() }: {
  pid: string; roots: Config[]; all: Config[]; depth: number; seen?: Set<string>
}) {
  return (
    <>
      {roots.map((c) => {
        if (seen.has(c.id)) return null
        const next = new Set(seen).add(c.id)
        const children = all.filter((x) => x.inherits_from === c.id && !next.has(x.id))
        return (
          <div key={c.id} className="flex flex-col gap-1.5">
            <ConfigCard pid={pid} config={c} depth={depth} />
            <ConfigNodes pid={pid} roots={children} all={all} depth={depth + 1} seen={next} />
          </div>
        )
      })}
    </>
  )
}

function EnvColumn({ pid, env, configs, loading, error, onAddConfig }: {
  pid: string
  env: Environment
  configs: Config[]
  loading: boolean
  error: boolean
  onAddConfig: (env: Environment, bases: Config[]) => void
}) {
  const tone = envTone(env.name)
  const roots = configs.filter((c) => !c.inherits_from || !configs.some((x) => x.id === c.inherits_from))
  const count = loading ? '…' : error ? '—' : `${configs.length} config${configs.length === 1 ? '' : 's'}`
  return (
    <section className="w-[260px] shrink-0">
      <div className="mb-2 flex items-center justify-between">
        <h3 className="text-[13px] font-semibold text-ink">{env.name}</h3>
        <Pill tone="muted">{count}</Pill>
      </div>
      <div className={cn('mb-3 h-[3px] w-10 rounded-full', envDotClass[tone])} />
      <button
        type="button"
        onClick={() => onAddConfig(env, configs)}
        className="mb-2 flex w-full items-center justify-center gap-1.5 rounded border border-dashed border-line py-2 text-[12px] font-semibold text-faint hover:border-brand-line hover:text-brand-text"
      >
        <Plus size={13} strokeWidth={1.7} /> Add config
      </button>
      <div className="flex flex-col gap-1.5">
        {loading && <div aria-hidden className="h-9 rounded bg-line-soft" />}
        {error && <p role="alert" className="px-1 text-[12px] text-danger">Couldn't load configs.</p>}
        {!loading && !error && configs.length === 0 && <p className="px-1 text-[12px] text-faint">No configs yet</p>}
        {!error && <ConfigNodes pid={pid} roots={roots} all={configs} depth={0} />}
      </div>
    </section>
  )
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

  if (envs.isError) {
    return <p role="alert" className="mt-16 text-center text-danger">Could not load environments.</p>
  }

  return (
    <div>
      <h1 className="sr-only">{project?.name ?? 'Project'}</h1>
      <div className="mb-1 flex items-center gap-2 text-[13px]">
        <Link to="/projects" className="text-muted hover:text-ink">Projects</Link>
        <span className="text-faint">/</span>
        <span className="font-semibold text-ink">{project?.name ?? '…'}</span>
      </div>
      <p className="mb-5 text-[12.5px] text-faint">
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
            <button onClick={() => setCreatingEnv(true)} className="rounded bg-brand px-4 py-2 text-[13px] font-semibold text-white shadow-card">
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
              onAddConfig={(env, bases) => setAddConfig({ env, bases })}
            />
          ))}
          <button
            type="button"
            onClick={() => setCreatingEnv(true)}
            className="flex h-9 shrink-0 items-center gap-1.5 self-start rounded border border-dashed border-line px-3 text-[12px] font-semibold text-faint hover:border-brand-line hover:text-brand-text"
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
