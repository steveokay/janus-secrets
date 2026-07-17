import { useEffect, useMemo, useState } from 'react'
import { Link, useSearchParams } from 'react-router-dom'
import { useQueries, useMutation, useQueryClient } from '@tanstack/react-query'
import * as Menu from '@radix-ui/react-dropdown-menu'
import { LayoutGrid, List, Plus, FolderGit2, MoreHorizontal } from 'lucide-react'
import { endpoints, Project } from '../lib/endpoints'
import { useProjects, useEnvironments } from '../secrets/nav'
import { envTone, envDotClass } from '../ui/env'
import { EmptyState } from '../ui/EmptyState'
import { Pill } from '../ui/Pill'
import { cn } from '../ui/cn'
import { useTitle } from '../lib/title'
import { CreateProjectForm } from '../structure/CreateForms'
import { RenameDialog } from '../structure/RenameDialog'
import { ConfirmDialog } from '../ui/ConfirmDialog'
import { useToast } from '../ui/Toast'
import { InstanceReadsStrip } from '../metrics/ReadsStrip'
import { glyphClass } from './glyph'
import { recencyLabel } from './recency'

const menuItem =
  'flex w-full cursor-default select-none items-center rounded px-2.5 py-1.5 text-[13px] text-ink outline-none data-[highlighted]:bg-brand-soft data-[highlighted]:text-brand-text'
const menuItemDanger =
  'flex w-full cursor-default select-none items-center rounded px-2.5 py-1.5 text-[13px] text-danger outline-none data-[highlighted]:bg-danger-soft'

type Sort = 'name-asc' | 'name-desc' | 'created-desc' | 'created-asc' | 'activity-desc'

function ProjectCard({ project, view }: { project: Project; view: 'grid' | 'list' }) {
  const envs = useEnvironments(project.id)
  const qc = useQueryClient()
  const toast = useToast()
  const [confirming, setConfirming] = useState(false)
  const [renaming, setRenaming] = useState(false)
  const label = recencyLabel(project)
  const configQueries = useQueries({
    queries: (envs.data ?? []).map((e) => ({
      queryKey: ['configs', project.id, e.id],
      queryFn: () => endpoints.listConfigs(project.id, e.id),
    })),
  })
  const anyConfigError = envs.isError || configQueries.some((q) => q.isError)
  const totalConfigs =
    configQueries.length > 0 && configQueries.every((q) => q.data)
      ? configQueries.reduce((n, q) => n + (q.data?.length ?? 0), 0)
      : undefined
  const countLabel = totalConfigs !== undefined ? `${totalConfigs} configs` : anyConfigError ? '— configs' : '… configs'

  const del = useMutation({
    mutationFn: () => endpoints.deleteProject(project.id),
    onSuccess: () => { toast({ title: `Moved ${project.name} to Trash` }); void qc.invalidateQueries({ queryKey: ['projects'] }) },
    onError: () => toast({ title: 'Delete failed', tone: 'danger' }),
  })

  const rename = useMutation({
    mutationFn: (name: string) => endpoints.renameProject(project.id, name),
    onSuccess: () => {
      toast({ title: `Renamed to ${project.name}` })
      void qc.invalidateQueries({ queryKey: ['projects'] })
      setRenaming(false)
    },
    onError: () => toast({ title: 'Rename failed', tone: 'danger' }),
  })

  return (
    <div className="group relative">
      <Link
        to={`/projects/${project.id}`}
        className={cn(
          'block rounded-card border border-line bg-card p-4 shadow-elev-1 hover:border-brand-line',
          view === 'list' && 'flex items-center gap-4',
        )}
      >
        <div className="flex min-w-0 flex-1 items-center gap-3">
          <span
            aria-hidden
            data-testid="project-glyph"
            className={cn(
              'flex h-8 w-8 shrink-0 items-center justify-center rounded-logo text-[13px] font-bold text-on-brand',
              glyphClass(project.slug),
            )}
          >
            {project.name.charAt(0).toUpperCase()}
          </span>
          <div className="min-w-0 flex-1">
            <div className="truncate text-[14px] font-semibold text-ink">{project.name}</div>
            {project.slug !== project.name && (
              <div className="truncate font-mono text-[11.5px] text-ink-faint">{project.slug}</div>
            )}
            {label && (
              <div className="truncate text-[11.5px] text-ink-faint">{label}</div>
            )}
          </div>
        </div>
        <div className={cn('flex items-center gap-2', view === 'grid' && 'mt-3')}>
          {envs.data && envs.data.length > 0 && (
            <span className="flex items-center gap-1">
              {envs.data.map((e) => (
                <span key={e.id} className={cn('h-[7px] w-[7px] rounded-[2px]', envDotClass[envTone(e.name)])} />
              ))}
            </span>
          )}
          <Pill tone="muted">{countLabel}</Pill>
        </div>
      </Link>
      <Menu.Root>
        <Menu.Trigger
          aria-label={`actions for ${project.name}`}
          onClick={(e) => { e.preventDefault(); e.stopPropagation() }}
          className="absolute right-2 top-2 hidden h-6 w-6 items-center justify-center rounded text-ink-faint outline-none hover:bg-row-hover hover:text-ink group-hover:flex group-focus-within:flex data-[state=open]:flex data-[state=open]:bg-row-hover"
        >
          <MoreHorizontal size={14} strokeWidth={1.8} />
        </Menu.Trigger>
        <Menu.Portal>
          <Menu.Content
            align="end"
            sideOffset={6}
            className="min-w-[160px] rounded-card border border-line bg-card p-1.5 shadow-pop"
          >
            <Menu.Item className={menuItem} onSelect={() => setRenaming(true)}>
              Rename
            </Menu.Item>
            <Menu.Separator className="my-1 h-px bg-line-soft" />
            <Menu.Item className={menuItemDanger} onSelect={() => setConfirming(true)}>
              Delete
            </Menu.Item>
          </Menu.Content>
        </Menu.Portal>
      </Menu.Root>
      {renaming && (
        <RenameDialog
          title={`Rename ${project.name}`}
          initial={project.name}
          onSubmit={(name) => rename.mutate(name)}
          onClose={() => setRenaming(false)}
        />
      )}
      <ConfirmDialog
        open={confirming}
        onOpenChange={setConfirming}
        title={`Delete ${project.name}?`}
        body="This moves the project to Trash. You can restore it, or permanently destroy it from Trash."
        confirmLabel="Move to Trash"
        tone="danger"
        onConfirm={() => { setConfirming(false); del.mutate() }}
      />
    </div>
  )
}

export function ProjectsList() {
  useTitle('Projects')
  const projects = useProjects()
  const [q, setQ] = useState('')
  const [sort, setSort] = useState<Sort>('name-asc')
  const [view, setView] = useState<'grid' | 'list'>('grid')
  const [creating, setCreating] = useState(false)
  const [params, setParams] = useSearchParams()

  // ⌘K "New project" deep-links here with ?new=1. Open the create modal and
  // clear the param so a refresh/back doesn't re-open it.
  useEffect(() => {
    if (params.get('new') === '1') {
      setCreating(true)
      const next = new URLSearchParams(params)
      next.delete('new')
      setParams(next, { replace: true })
    }
  }, [params, setParams])

  const shown = useMemo(() => {
    const list = (projects.data ?? []).filter(
      (p) => p.name.toLowerCase().includes(q.toLowerCase()) || p.slug.toLowerCase().includes(q.toLowerCase()),
    )
    list.sort((a, b) => {
      switch (sort) {
        case 'name-asc': return a.name.localeCompare(b.name)
        case 'name-desc': return b.name.localeCompare(a.name)
        case 'created-desc': return (b.created_at ?? '').localeCompare(a.created_at ?? '')
        case 'created-asc': return (a.created_at ?? '').localeCompare(b.created_at ?? '')
        case 'activity-desc': {
          const av = a.last_activity_at ?? '', bv = b.last_activity_at ?? ''
          if (av && bv) return bv.localeCompare(av)
          if (av) return -1
          if (bv) return 1
          return a.name.localeCompare(b.name)
        }
        default: return 0
      }
    })
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
              className="rounded bg-brand px-4 py-2 text-[13px] font-semibold text-white shadow-elev-1"
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
      <InstanceReadsStrip />
      <div className="mb-4 flex items-center justify-between gap-3">
        <h2 className="text-[17px] font-semibold tracking-tight text-ink">Projects</h2>
        <button
          onClick={() => setCreating(true)}
          className="flex items-center gap-1.5 rounded bg-brand px-3 py-1.5 text-[13px] font-semibold text-white shadow-elev-1"
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
          className="min-w-[200px] flex-1 rounded border border-line bg-card px-3 py-1.5 text-[12.5px] text-ink placeholder:text-ink-faint"
        />
        <select
          aria-label="sort"
          value={sort}
          onChange={(e) => setSort(e.target.value as Sort)}
          className="rounded border border-line bg-card px-2.5 py-1.5 text-[12.5px] text-ink"
        >
          <option value="name-asc">Name A–Z</option>
          <option value="name-desc">Name Z–A</option>
          <option value="created-desc">Newest</option>
          <option value="created-asc">Oldest</option>
          <option value="activity-desc">Recently active</option>
        </select>
        <div className="flex rounded border border-line">
          <button
            aria-label="grid view"
            aria-pressed={view === 'grid'}
            onClick={() => setView('grid')}
            className={cn('flex h-8 w-8 items-center justify-center rounded-l text-ink-mute', view === 'grid' && 'bg-brand-soft text-brand-text')}
          >
            <LayoutGrid size={15} strokeWidth={1.7} />
          </button>
          <button
            aria-label="list view"
            aria-pressed={view === 'list'}
            onClick={() => setView('list')}
            className={cn('flex h-8 w-8 items-center justify-center rounded-r text-ink-mute', view === 'list' && 'bg-brand-soft text-brand-text')}
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
