import { useMemo, useState } from 'react'
import { Link } from 'react-router-dom'
import { useQueries } from '@tanstack/react-query'
import { LayoutGrid, List, Plus, FolderGit2 } from 'lucide-react'
import { endpoints, Project } from '../lib/endpoints'
import { useProjects, useEnvironments } from '../secrets/nav'
import { envTone, envDotClass } from '../ui/env'
import { EmptyState } from '../ui/EmptyState'
import { Pill } from '../ui/Pill'
import { cn } from '../ui/cn'
import { useTitle } from '../lib/title'
import { CreateProjectForm } from '../structure/CreateForms'

type Sort = 'name-asc' | 'name-desc'

function ProjectCard({ project, view }: { project: Project; view: 'grid' | 'list' }) {
  const envs = useEnvironments(project.id)
  const configQueries = useQueries({
    queries: (envs.data ?? []).map((e) => ({
      queryKey: ['configs', project.id, e.id],
      queryFn: () => endpoints.listConfigs(project.id, e.id),
    })),
  })
  const totalConfigs =
    configQueries.length > 0 && configQueries.every((q) => q.data)
      ? configQueries.reduce((n, q) => n + (q.data?.length ?? 0), 0)
      : undefined

  return (
    <Link
      to={`/projects/${project.id}`}
      className={cn(
        'group rounded-card border border-line bg-card p-4 shadow-card hover:border-brand-line',
        view === 'list' && 'flex items-center gap-4',
      )}
    >
      <div className="min-w-0 flex-1">
        <div className="truncate text-[14px] font-semibold text-ink">{project.name}</div>
        {project.slug !== project.name && (
          <div className="truncate font-mono text-[11.5px] text-faint">{project.slug}</div>
        )}
      </div>
      <div className={cn('flex items-center gap-2', view === 'grid' && 'mt-3')}>
        {envs.data && envs.data.length > 0 && (
          <span className="flex items-center gap-1">
            {envs.data.map((e) => (
              <span key={e.id} className={cn('h-[7px] w-[7px] rounded-[2px]', envDotClass[envTone(e.name)])} />
            ))}
          </span>
        )}
        <Pill tone="muted">{totalConfigs === undefined ? '… configs' : `${totalConfigs} configs`}</Pill>
      </div>
    </Link>
  )
}

export function ProjectsList() {
  useTitle('Projects')
  const projects = useProjects()
  const [q, setQ] = useState('')
  const [sort, setSort] = useState<Sort>('name-asc')
  const [view, setView] = useState<'grid' | 'list'>('grid')
  const [creating, setCreating] = useState(false)

  const shown = useMemo(() => {
    const list = (projects.data ?? []).filter(
      (p) => p.name.toLowerCase().includes(q.toLowerCase()) || p.slug.toLowerCase().includes(q.toLowerCase()),
    )
    list.sort((a, b) => (sort === 'name-asc' ? a.name.localeCompare(b.name) : b.name.localeCompare(a.name)))
    return list
  }, [projects.data, q, sort])

  const createDialog = creating && (
    <CreateProjectForm onCreated={() => setCreating(false)} onClose={() => setCreating(false)} />
  )

  if (projects.isLoading) {
    return (
      <div aria-hidden className="grid gap-3 sm:grid-cols-2 lg:grid-cols-3">
        {[0, 1, 2].map((i) => <div key={i} className="h-24 rounded-card bg-line-soft" />)}
      </div>
    )
  }
  if (projects.isError) {
    return <p role="alert" className="mt-16 text-center text-danger">Could not load projects.</p>
  }
  if (!projects.data?.length) {
    return (
      <>
        <EmptyState
          icon={<FolderGit2 size={22} strokeWidth={1.7} />}
          title="No projects yet"
          hint="A project groups your dev, staging and prod secrets."
          action={
            <button
              onClick={() => setCreating(true)}
              className="rounded bg-brand px-4 py-2 text-[13px] font-semibold text-white shadow-card"
            >
              Create your first project
            </button>
          }
        />
        {createDialog}
      </>
    )
  }

  return (
    <div>
      <div className="mb-4 flex items-center justify-between gap-3">
        <h2 className="text-[17px] font-semibold tracking-tight text-ink">Projects</h2>
        <button
          onClick={() => setCreating(true)}
          className="flex items-center gap-1.5 rounded bg-brand px-3 py-1.5 text-[13px] font-semibold text-white shadow-card"
        >
          <Plus size={14} strokeWidth={1.7} /> New project
        </button>
      </div>

      <div className="mb-4 flex flex-wrap items-center gap-2">
        <input
          type="search"
          role="searchbox"
          aria-label="search projects"
          value={q}
          onChange={(e) => setQ(e.target.value)}
          placeholder="Search projects…"
          className="min-w-[200px] flex-1 rounded border border-line bg-card px-3 py-1.5 text-[12.5px] text-ink placeholder:text-faint"
        />
        <select
          aria-label="sort"
          value={sort}
          onChange={(e) => setSort(e.target.value as Sort)}
          className="rounded border border-line bg-card px-2.5 py-1.5 text-[12.5px] text-ink"
        >
          <option value="name-asc">Name A–Z</option>
          <option value="name-desc">Name Z–A</option>
        </select>
        <div className="flex rounded border border-line">
          <button
            aria-label="grid view"
            aria-pressed={view === 'grid'}
            onClick={() => setView('grid')}
            className={cn('flex h-8 w-8 items-center justify-center rounded-l text-muted', view === 'grid' && 'bg-brand-soft text-brand-text')}
          >
            <LayoutGrid size={15} strokeWidth={1.7} />
          </button>
          <button
            aria-label="list view"
            aria-pressed={view === 'list'}
            onClick={() => setView('list')}
            className={cn('flex h-8 w-8 items-center justify-center rounded-r text-muted', view === 'list' && 'bg-brand-soft text-brand-text')}
          >
            <List size={15} strokeWidth={1.7} />
          </button>
        </div>
      </div>

      {shown.length === 0 ? (
        <EmptyState title="No projects match your search." />
      ) : (
        <div className={cn(view === 'grid' ? 'grid gap-3 sm:grid-cols-2 lg:grid-cols-3' : 'flex flex-col gap-2')}>
          {shown.map((p) => <ProjectCard key={p.id} project={p} view={view} />)}
        </div>
      )}
      {createDialog}
    </div>
  )
}
