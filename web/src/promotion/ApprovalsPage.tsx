import { useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { endpoints } from '../lib/endpoints'
import { Select } from '../ui/Select'
import { EmptyState } from '../ui/EmptyState'
import { useTitle } from '../lib/title'
import { RequestsPanel } from './RequestsPanel'

export function ApprovalsPage() {
  useTitle('Approvals')
  const projectsQ = useQuery({ queryKey: ['projects'], queryFn: endpoints.listProjects })
  const [projectId, setProjectId] = useState<string | null>(null)
  const projects = projectsQ.data ?? []
  const selected = projectId ?? projects[0]?.id ?? ''

  return (
    <div className="mx-auto max-w-4xl px-6 py-6">
      <header className="mb-4">
        <h1 className="text-lg font-semibold text-ink">Approvals</h1>
        <p className="text-[12.5px] text-ink-mute">Promotion requests awaiting review, and requests you've filed.</p>
      </header>

      {projects.length > 0 && (
        <div className="mb-4 w-64">
          <Select
            aria-label="project"
            label="Project"
            value={selected}
            onChange={(e) => setProjectId(e.target.value)}
          >
            {projects.map((p) => (
              <option key={p.id} value={p.id}>{p.name}</option>
            ))}
          </Select>
        </div>
      )}

      {projectsQ.isSuccess && projects.length === 0 && (
        <EmptyState title="No projects yet" hint="Create a project to start filing promotion requests." />
      )}

      {selected && <RequestsPanel projectId={selected} />}
    </div>
  )
}
