import { Link, useLocation, useNavigate, matchPath } from 'react-router-dom'
import { useProjects, useEnvironments, useConfigs } from '../secrets/nav'

// Sidebar is rendered as a sibling of <Routes> (inside AppLayout), not nested
// within a matched <Route>, so useParams() would always be empty here.
// Derive the active projectId from the URL directly via matchPath instead.
function useActiveProjectId() {
  const location = useLocation()
  return matchPath('/projects/:projectId/*', location.pathname)?.params.projectId
}

function EnvConfigs({ pid, eid, name }: { pid: string; eid: string; name: string }) {
  const configs = useConfigs(pid, eid)
  return (
    <li>
      <div className="mt-1 text-xs uppercase tracking-wide text-gray-400">{name}</div>
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

  return (
    <nav className="text-sm">
      <select
        value={projectId ?? ''}
        onChange={(e) => navigate(`/projects/${e.target.value}`)}
        className="mb-3 w-full rounded border p-1"
        aria-label="project"
      >
        <option value="" disabled>Select a project…</option>
        {projects.data?.map((p) => <option key={p.id} value={p.id}>{p.name}</option>)}
      </select>
      {projectId && (
        <ul>
          {envs.data?.map((e) => <EnvConfigs key={e.id} pid={projectId} eid={e.id} name={e.name} />)}
        </ul>
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
    </nav>
  )
}
