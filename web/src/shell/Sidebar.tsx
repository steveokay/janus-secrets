import { useState } from 'react'
import { Link, useLocation, useNavigate, matchPath } from 'react-router-dom'
import { useProjects, useEnvironments, useConfigs } from '../secrets/nav'
import { CreateProjectForm, CreateEnvironmentForm, CreateConfigForm } from '../structure/CreateForms'
import { Config } from '../lib/endpoints'

// Sidebar is rendered as a sibling of <Routes> (inside AppLayout), not nested
// within a matched <Route>, so useParams() would always be empty here.
// Derive the active projectId from the URL directly via matchPath instead.
function useActiveProjectId() {
  const location = useLocation()
  return matchPath('/projects/:projectId/*', location.pathname)?.params.projectId
}

type OpenForm = null | 'project' | 'env' | { config: { eid: string; bases: Config[] } }

function EnvConfigs({
  pid,
  eid,
  name,
  onAddConfig,
}: {
  pid: string
  eid: string
  name: string
  onAddConfig: (eid: string, bases: Config[]) => void
}) {
  const configs = useConfigs(pid, eid)
  return (
    <li>
      <div className="mt-1 flex items-center justify-between text-xs uppercase tracking-wide text-gray-400">
        <span>{name}</span>
        <button
          type="button"
          onClick={() => onAddConfig(eid, configs.data ?? [])}
          aria-label={`add config to ${name}`}
          className="normal-case text-blue-500"
        >
          ＋
        </button>
      </div>
      <ul className="ml-2">
        {configs.data?.map((c) => (
          <li key={c.id}>
            <Link to={`/projects/${pid}/configs/${c.id}`} className="block rounded px-1 py-0.5 hover:bg-gray-100">
              {c.name}
              {c.inherits_from && <span className="ml-1 text-xs text-blue-500">↳</span>}
            </Link>
          </li>
        ))}
      </ul>
    </li>
  )
}

export function Sidebar() {
  const projectId = useActiveProjectId()
  const navigate = useNavigate()
  const projects = useProjects()
  const envs = useEnvironments(projectId)
  const [open, setOpen] = useState<OpenForm>(null)

  return (
    <nav className="text-sm">
      <div className="mb-3 flex items-center gap-1">
        <select
          value={projectId ?? ''}
          onChange={(e) => navigate(`/projects/${e.target.value}`)}
          className="w-full rounded border p-1"
          aria-label="project"
        >
          <option value="" disabled>Select a project…</option>
          {projects.data?.map((p) => <option key={p.id} value={p.id}>{p.name}</option>)}
        </select>
        <button type="button" onClick={() => setOpen('project')} aria-label="add project" className="rounded border px-2 py-1 text-blue-500">＋</button>
      </div>
      {projectId && (
        <>
          <ul>
            {envs.data?.map((e) => (
              <EnvConfigs
                key={e.id}
                pid={projectId}
                eid={e.id}
                name={e.name}
                onAddConfig={(eid, bases) => setOpen({ config: { eid, bases } })}
              />
            ))}
          </ul>
          <button type="button" onClick={() => setOpen('env')} className="mt-1 text-blue-500">＋ Env</button>
        </>
      )}
      <div className="mt-4 border-t pt-2 text-gray-400">
        <Link to={`/projects/${projectId ?? ''}/audit`} className="block">Audit</Link>
        <Link to="/tokens" className="block">Tokens</Link>
        <Link to="/members" className="block">Members</Link>
        <div className="mt-2 border-t pt-2">
          <Link to="/transit" className="block">Transit</Link>
          <Link to="/settings" className="block">Settings</Link>
        </div>
      </div>

      {open === 'project' && (
        <CreateProjectForm
          onCreated={(p) => { setOpen(null); navigate('/projects/' + p.id) }}
          onClose={() => setOpen(null)}
        />
      )}
      {open === 'env' && projectId && (
        <CreateEnvironmentForm pid={projectId} onCreated={() => setOpen(null)} onClose={() => setOpen(null)} />
      )}
      {open && typeof open === 'object' && projectId && (
        <CreateConfigForm
          pid={projectId}
          eid={open.config.eid}
          bases={open.config.bases}
          onCreated={(c) => { setOpen(null); navigate('/projects/' + projectId + '/configs/' + c.id) }}
          onClose={() => setOpen(null)}
        />
      )}
    </nav>
  )
}
