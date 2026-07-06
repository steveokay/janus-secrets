import { useState } from 'react'
import { Link, useLocation, useNavigate, matchPath } from 'react-router-dom'
import { Plus, ScrollText, KeyRound, Users, Shield, Settings } from 'lucide-react'
import { useProjects, useEnvironments, useConfigs } from '../secrets/nav'
import { CreateProjectForm, CreateEnvironmentForm, CreateConfigForm } from '../structure/CreateForms'
import { Config } from '../lib/endpoints'
import { envTone, envDotClass } from '../ui/env'
import { cn } from '../ui/cn'

// Sidebar is rendered as a sibling of <Routes> (inside AppLayout), not nested
// within a matched <Route>, so useParams() would always be empty here.
// Derive the active ids from the URL directly via matchPath instead.
function useActiveIds() {
  const location = useLocation()
  return {
    projectId: matchPath('/projects/:projectId/*', location.pathname)?.params.projectId,
    configId: matchPath('/projects/:projectId/configs/:configId', location.pathname)?.params.configId,
  }
}

type OpenForm = null | 'project' | 'env' | { config: { eid: string; bases: Config[] } }

function SectionLabel({ children, action }: { children: React.ReactNode; action?: React.ReactNode }) {
  return (
    <div className="mb-1 mt-4 flex items-center justify-between px-2 text-[10.5px] font-bold uppercase tracking-[.12em] text-faint">
      <span>{children}</span>
      {action}
    </div>
  )
}

function IconAdd({ label, onClick }: { label: string; onClick: () => void }) {
  return (
    <button
      type="button"
      onClick={onClick}
      aria-label={label}
      className="flex h-5 w-5 items-center justify-center rounded text-faint hover:bg-brand-soft hover:text-brand-deep"
    >
      <Plus size={13} strokeWidth={1.7} />
    </button>
  )
}

function EnvConfigs({ pid, eid, name, activeConfigId, onAddConfig }: {
  pid: string
  eid: string
  name: string
  activeConfigId?: string
  onAddConfig: (eid: string, bases: Config[]) => void
}) {
  const configs = useConfigs(pid, eid)
  return (
    <li className="mx-1 mt-2">
      <div className="flex items-center justify-between px-2 text-[12px] font-semibold text-muted">
        <span className="flex items-center gap-2">
          <span className={cn('h-[7px] w-[7px] rounded-[2px]', envDotClass[envTone(name)])} />
          {name}
        </span>
        <IconAdd label={`add config to ${name}`} onClick={() => onAddConfig(eid, configs.data ?? [])} />
      </div>
      <ul className="mt-0.5">
        {configs.data?.map((c) => {
          const active = c.id === activeConfigId
          return (
            <li key={c.id} className="relative ml-3.5">
              {active && <span className="absolute -left-3.5 bottom-[5px] top-[5px] w-[3px] rounded-full bg-brand" />}
              <Link
                to={`/projects/${pid}/configs/${c.id}`}
                aria-current={active ? 'page' : undefined}
                className={cn(
                  'block rounded px-2 py-1 text-[12.5px] text-muted hover:bg-line-soft',
                  active && 'bg-brand-soft font-semibold text-brand-deep hover:bg-brand-soft',
                )}
              >
                {c.name}
                {c.inherits_from && <span className="ml-1 text-[11px] text-info">↳</span>}
              </Link>
            </li>
          )
        })}
      </ul>
    </li>
  )
}

const navItem =
  'mx-1 flex items-center gap-2.5 rounded px-2 py-1.5 text-[12.5px] text-muted hover:bg-line-soft hover:text-ink'

export function Sidebar() {
  const { projectId, configId } = useActiveIds()
  const navigate = useNavigate()
  const projects = useProjects()
  const envs = useEnvironments(projectId)
  const [open, setOpen] = useState<OpenForm>(null)

  return (
    <nav className="text-sm">
      <SectionLabel action={<IconAdd label="add project" onClick={() => setOpen('project')} />}>Project</SectionLabel>
      <select
        value={projectId ?? ''}
        onChange={(e) => navigate(`/projects/${e.target.value}`)}
        aria-label="project"
        className="mx-1 w-[calc(100%-8px)] rounded border border-line bg-card px-2.5 py-1.5 text-[13px] font-semibold text-ink"
      >
        <option value="" disabled>Select a project…</option>
        {projects.data?.map((p) => <option key={p.id} value={p.id}>{p.name}</option>)}
      </select>

      {projectId && (
        <>
          <SectionLabel action={<IconAdd label="add environment" onClick={() => setOpen('env')} />}>
            Environments
          </SectionLabel>
          <ul>
            {envs.data?.map((e) => (
              <EnvConfigs
                key={e.id}
                pid={projectId}
                eid={e.id}
                name={e.name}
                activeConfigId={configId}
                onAddConfig={(eid, bases) => setOpen({ config: { eid, bases } })}
              />
            ))}
          </ul>
        </>
      )}

      <SectionLabel>Instance</SectionLabel>
      <Link to={`/projects/${projectId ?? ''}/audit`} className={navItem}><ScrollText size={15} strokeWidth={1.7} /> Audit</Link>
      <Link to="/tokens" className={navItem}><KeyRound size={15} strokeWidth={1.7} /> Tokens</Link>
      <Link to="/members" className={navItem}><Users size={15} strokeWidth={1.7} /> Members</Link>
      <Link to="/transit" className={navItem}><Shield size={15} strokeWidth={1.7} /> Transit</Link>
      <Link to="/settings" className={navItem}><Settings size={15} strokeWidth={1.7} /> Settings</Link>

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
