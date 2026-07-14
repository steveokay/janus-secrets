import { useEffect, useState } from 'react'
import { Link, useParams } from 'react-router-dom'
import { useMutation } from '@tanstack/react-query'
import { ChevronUp, ChevronDown, GitBranch } from 'lucide-react'
import type { Environment } from '../lib/endpoints'
import { useEnvironments } from '../secrets/nav'
import { usePipeline } from './usePipeline'
import { promotion } from './endpoints'
import { errorMessage } from '../lib/api'
import { envTone, envDotClass } from '../ui/env'
import { Button } from '../ui/Button'
import { Skeleton } from '../ui/Skeleton'
import { useToast } from '../ui/Toast'
import { useTitle } from '../lib/title'
import { cn } from '../ui/cn'

interface Row {
  env: Environment
  included: boolean
}

export function PipelineSettings() {
  useTitle('Release pipeline')
  const { projectId } = useParams()
  const pid = projectId!
  const toast = useToast()
  const envs = useEnvironments(pid)
  const pipeline = usePipeline(pid)

  const [rows, setRows] = useState<Row[] | null>(null)

  // Initialize once both queries have loaded: envs in the pipeline come first in
  // pipeline order (included), then the remaining envs in API order. When there
  // is no pipeline yet, default every env to included so the first save is a
  // sensible full ordering.
  useEffect(() => {
    if (rows !== null) return
    if (!envs.data || pipeline.data === undefined) return
    const order = pipeline.data.environment_ids
    const byId = new Map(envs.data.map((e) => [e.id, e]))
    const inOrder = order
      .map((id) => byId.get(id))
      .filter((e): e is Environment => !!e)
      .map((env) => ({ env, included: true }))
    const rest = envs.data
      .filter((e) => !order.includes(e.id))
      .map((env) => ({ env, included: order.length === 0 }))
    setRows([...inOrder, ...rest])
  }, [rows, envs.data, pipeline.data])

  const save = useMutation({
    mutationFn: (ids: string[]) => promotion.pipeline.set(pid, ids),
    onSuccess: () => toast({ title: 'Pipeline saved' }),
    onError: (e) => toast({ title: errorMessage(e), tone: 'danger' }),
  })

  function move(index: number, dir: -1 | 1) {
    setRows((prev) => {
      if (!prev) return prev
      const j = index + dir
      if (j < 0 || j >= prev.length) return prev
      const next = [...prev]
      ;[next[index], next[j]] = [next[j], next[index]]
      return next
    })
  }

  function toggle(index: number) {
    setRows((prev) => prev && prev.map((r, i) => (i === index ? { ...r, included: !r.included } : r)))
  }

  return (
    <div>
      <div className="mb-1 flex items-center gap-2 text-[13px]">
        <Link to={`/projects/${pid}`} className="text-ink-mute hover:text-ink">Project</Link>
        <span className="text-ink-faint">/</span>
        <span className="inline-flex items-center gap-1.5 font-semibold text-ink">
          <GitBranch size={13} strokeWidth={1.8} aria-hidden /> Release pipeline
        </span>
      </div>

      <div className="mt-4 max-w-xl rounded-card border border-line bg-card p-5 shadow-elev-1">
        <h1 className="text-[15px] font-semibold text-ink">Release pipeline</h1>
        <p className="mt-1 text-[12.5px] text-ink-faint">
          Promote secrets forward along this order — dev → staging → prod. Include the
          environments that participate and arrange them in release order.
        </p>

        {rows === null ? (
          <div className="mt-5 flex flex-col gap-2" aria-hidden>
            {[0, 1, 2].map((i) => <Skeleton key={i} className="h-11 w-full" />)}
          </div>
        ) : rows.length === 0 ? (
          <p className="mt-5 text-[12.5px] text-ink-faint">No environments yet.</p>
        ) : (
          <ul className="mt-5 flex flex-col gap-2">
            {rows.map((r, i) => {
              const tone = envTone(r.env.slug || r.env.name)
              return (
                <li
                  key={r.env.id}
                  className={cn(
                    'flex items-center gap-3 rounded border border-line bg-surface-1 px-3 py-2.5 motion-safe:transition-nocturne',
                    !r.included && 'opacity-55',
                  )}
                >
                  <input
                    type="checkbox"
                    aria-label={`include ${r.env.name}`}
                    className="h-3.5 w-3.5 accent-brand"
                    checked={r.included}
                    onChange={() => toggle(i)}
                  />
                  <span className={cn('h-2 w-2 shrink-0 rounded-full', envDotClass[tone])} aria-hidden />
                  <span className="flex-1 text-[13px] font-medium text-ink">{r.env.name}</span>
                  <span className="flex items-center gap-1">
                    <button
                      type="button"
                      aria-label={`move ${r.env.name} up`}
                      disabled={i === 0}
                      onClick={() => move(i, -1)}
                      className="inline-flex h-7 w-7 items-center justify-center rounded text-ink-faint hover:bg-surface-3 hover:text-brand-text disabled:opacity-30 disabled:hover:bg-transparent disabled:hover:text-ink-faint"
                    >
                      <ChevronUp size={15} strokeWidth={1.8} />
                    </button>
                    <button
                      type="button"
                      aria-label={`move ${r.env.name} down`}
                      disabled={i === rows.length - 1}
                      onClick={() => move(i, 1)}
                      className="inline-flex h-7 w-7 items-center justify-center rounded text-ink-faint hover:bg-surface-3 hover:text-brand-text disabled:opacity-30 disabled:hover:bg-transparent disabled:hover:text-ink-faint"
                    >
                      <ChevronDown size={15} strokeWidth={1.8} />
                    </button>
                  </span>
                </li>
              )
            })}
          </ul>
        )}

        <div className="mt-5 flex justify-end">
          <Button
            size="sm"
            loading={save.isPending}
            disabled={rows === null}
            onClick={() => save.mutate((rows ?? []).filter((r) => r.included).map((r) => r.env.id))}
          >
            Save
          </Button>
        </div>
      </div>
    </div>
  )
}
