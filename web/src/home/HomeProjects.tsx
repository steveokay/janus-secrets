import { Link } from 'react-router-dom'
import type { UseQueryResult } from '@tanstack/react-query'
import { FolderGit2 } from 'lucide-react'
import type { Project } from '../lib/endpoints'
import { useEnvironments } from '../secrets/nav'
import { envTone } from '../ui/env'
import { Card } from '../ui/Card'
import { Pill } from '../ui/Pill'
import { EmptyState } from '../ui/EmptyState'
import { Skeleton } from '../ui/Skeleton'
import { glyphClass } from './glyph'

function ProjectCard({ project }: { project: Project }) {
  const envs = useEnvironments(project.id)
  return (
    <Link to={`/projects/${project.id}`} className="block">
      <Card className="p-4 transition-nocturne hover:-translate-y-px hover:shadow-elev-2">
        <div className="flex items-center gap-3">
          <span
            aria-hidden
            className={`flex h-8 w-8 shrink-0 items-center justify-center rounded-logo text-[13px] font-bold text-on-brand ${glyphClass(project.slug)}`}
          >
            {project.name.charAt(0).toUpperCase()}
          </span>
          <div className="min-w-0">
            <div className="truncate text-[13.5px] font-semibold text-ink">{project.name}</div>
            <div className="truncate font-mono text-[11px] text-ink-faint">{project.slug}</div>
          </div>
        </div>
        {envs.data && envs.data.length > 0 && (
          <div className="mt-3 flex flex-wrap items-center gap-1.5">
            {envs.data.map((e) => (
              <Pill key={e.id} tone={envTone(e.name)}>{e.name}</Pill>
            ))}
          </div>
        )}
      </Card>
    </Link>
  )
}

export function HomeProjects({ projects }: { projects: UseQueryResult<Project[]> }) {
  if (projects.isLoading) {
    return (
      <div className="mb-6 grid gap-3 sm:grid-cols-2 lg:grid-cols-3">
        {[0, 1, 2].map((i) => (
          <Skeleton key={i} className="h-[92px] rounded-card" />
        ))}
      </div>
    )
  }
  // Section hides on error (e.g. 403) rather than erroring.
  if (projects.isError) return null
  if (!projects.data?.length) {
    return (
      <EmptyState
        className="my-10"
        icon={<FolderGit2 size={22} strokeWidth={1.7} />}
        title="No projects yet"
        hint="A project groups your dev, staging and prod secrets."
        action={
          <Link
            to="/projects"
            className="rounded bg-brand px-4 py-2 text-[13px] font-semibold text-on-brand shadow-card"
          >
            New project
          </Link>
        }
      />
    )
  }
  return (
    <div className="mb-6 grid gap-3 sm:grid-cols-2 lg:grid-cols-3">
      {projects.data.map((p) => (
        <ProjectCard key={p.id} project={p} />
      ))}
    </div>
  )
}
