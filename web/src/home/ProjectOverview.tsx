import { useState } from 'react'
import { Link, useParams } from 'react-router-dom'
import { useQueries } from '@tanstack/react-query'
import { Layers } from 'lucide-react'
import { endpoints, Config, MaskedSecret } from '../lib/endpoints'
import { useProjects, useEnvironments } from '../secrets/nav'
import { envTone, envDotClass } from '../ui/env'
import { EmptyState } from '../ui/EmptyState'
import { Pill } from '../ui/Pill'
import { cn } from '../ui/cn'
import { useTitle } from '../lib/title'
import { timeAgo } from '../lib/time'
import { CreateEnvironmentForm } from '../structure/CreateForms'

function ConfigRow({ pid, config, meta }: { pid: string; config: Config; meta?: Record<string, MaskedSecret> }) {
  const keys = meta ? Object.keys(meta).length : undefined
  const last = meta
    ? Object.values(meta).reduce<string | null>((acc, m) => (!acc || m.created_at > acc ? m.created_at : acc), null)
    : null
  return (
    <Link
      to={`/projects/${pid}/configs/${config.id}`}
      className="flex items-center justify-between border-t border-line-soft px-4 py-2.5 hover:bg-line-soft/50"
    >
      <span className="text-[13px] font-medium text-ink">
        {config.name}
        {config.inherits_from && <span className="ml-1 text-[11px] text-info">↳</span>}
      </span>
      <span className="text-[11.5px] tabular-nums text-faint">
        {keys === undefined ? '— keys' : keys === 0 ? '0 keys' : `${keys} keys · ${timeAgo(last!)}`}
      </span>
    </Link>
  )
}

function EnvCard({ pid, name, configs, error }: { pid: string; name: string; configs?: Config[]; error: boolean }) {
  // Same queryKey as SecretEditor's masked query — cache-shared, unaudited metadata.
  const metas = useQueries({
    queries: (configs ?? []).map((c) => ({
      queryKey: ['config', c.id, 'masked'],
      queryFn: () => endpoints.maskedSecrets(c.id),
    })),
  })
  return (
    <section className="rounded-card border border-line bg-card shadow-card">
      <header className="flex items-center gap-2 px-4 py-2.5">
        <span className={cn('h-[7px] w-[7px] rounded-[2px]', envDotClass[envTone(name)])} />
        <span className="text-[12px] font-semibold uppercase tracking-[.08em] text-muted">{name}</span>
        <span className="ml-auto text-[11px] tabular-nums text-faint">
          {configs ? `${configs.length} configs` : ''}
        </span>
      </header>
      {!configs && !error && <div className="mx-4 mb-3 h-4 rounded bg-line-soft" />}
      {error && <p className="border-t border-line-soft px-4 py-2.5 text-[12.5px] text-danger">Couldn't load configs.</p>}
      {configs?.length === 0 && (
        <p className="border-t border-line-soft px-4 py-2.5 text-[12.5px] text-faint">No configs yet</p>
      )}
      {configs?.map((c, i) => <ConfigRow key={c.id} pid={pid} config={c} meta={metas[i]?.data} />)}
    </section>
  )
}

export function ProjectOverview() {
  const { projectId } = useParams()
  const pid = projectId!
  const projects = useProjects()
  const envs = useEnvironments(pid)
  const [creatingEnv, setCreatingEnv] = useState(false)
  const project = projects.data?.find((p) => p.id === pid)
  useTitle(project?.name)

  // Same queryKey as Sidebar/Breadcrumb — cache-shared.
  const configLists = useQueries({
    queries: (envs.data ?? []).map((e) => ({
      queryKey: ['configs', pid, e.id],
      queryFn: () => endpoints.listConfigs(pid, e.id),
    })),
  })
  const totalConfigs =
    configLists.length > 0 && configLists.every((q) => q.data)
      ? configLists.reduce((n, q) => n + (q.data?.length ?? 0), 0)
      : undefined

  if (envs.isError) {
    return <p role="alert" className="mt-16 text-center text-danger">Could not load environments.</p>
  }

  return (
    <div>
      <div className="mb-4 flex items-center justify-between">
        <div>
          <h3 className="text-[17px] font-semibold tracking-tight">{project?.name ?? '…'}</h3>
          <p className="text-[12.5px] text-faint">
            {envs.data ? `${envs.data.length} environments` : '…'}
            {totalConfigs !== undefined && ` · ${totalConfigs} configs`}
          </p>
        </div>
        {/* Placeholder until Phase-2D usage metrics; becomes a real stat then. */}
        <Pill tone="muted">Reads 24h · soon</Pill>
      </div>
      {envs.data?.length === 0 ? (
        <EmptyState
          icon={<Layers size={22} strokeWidth={1.7} />}
          title="No environments yet"
          hint="Environments hold your configs — dev, staging, prod."
          action={
            <button
              onClick={() => setCreatingEnv(true)}
              className="rounded bg-brand px-4 py-2 text-[13px] font-semibold text-white shadow-card"
            >
              Create environment
            </button>
          }
        />
      ) : (
        <div className="grid gap-4 md:grid-cols-2">
          {envs.data?.map((e, i) => (
            <EnvCard key={e.id} pid={pid} name={e.name} configs={configLists[i]?.data} error={!!configLists[i]?.error} />
          ))}
        </div>
      )}
      {creatingEnv && (
        <CreateEnvironmentForm pid={pid} onCreated={() => setCreatingEnv(false)} onClose={() => setCreatingEnv(false)} />
      )}
    </div>
  )
}
