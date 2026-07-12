import { useState } from 'react'
import { Link, useLocation, useNavigate, matchPath } from 'react-router-dom'
import { Home, LayoutGrid, ScrollText, KeyRound, Users, Shield, Settings, Plus, RefreshCw } from 'lucide-react'
import { useProjects, useEnvironments, useConfigs } from '../secrets/nav'
import { CreateEnvironmentForm, CreateConfigForm } from '../structure/CreateForms'
import { Config } from '../lib/endpoints'
import { envTone, envDotClass } from '../ui/env'
import { cn } from '../ui/cn'
import { Tooltip } from '../ui/Tooltip'

// Sidebar is a sibling of <Routes>, so useParams() is empty here — derive the
// active ids from the URL via matchPath.
function useActiveIds() {
  const location = useLocation()
  return {
    projectId: matchPath('/projects/:projectId/*', location.pathname)?.params.projectId,
    configId: matchPath('/projects/:projectId/configs/:configId', location.pathname)?.params.configId,
  }
}

type OpenForm = null | 'env' | { config: { eid: string; bases: Config[] } }

function SectionLabel({ children, action }: { children: React.ReactNode; action?: React.ReactNode }) {
  return (
    <div className="mb-1 mt-4 flex items-center justify-between px-2 text-[10.5px] font-bold uppercase tracking-[.12em] text-ink-faint">
      <span className="truncate">{children}</span>
      {action}
    </div>
  )
}

function IconAdd({ label, onClick }: { label: string; onClick: () => void }) {
  return (
    <Tooltip content={label}>
      <button
        type="button"
        onClick={onClick}
        aria-label={label}
        className="flex h-5 w-5 items-center justify-center rounded text-faint hover:bg-brand-soft hover:text-brand-text"
      >
        <Plus size={13} strokeWidth={1.7} />
      </button>
    </Tooltip>
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
              <Link
                to={`/projects/${pid}/configs/${c.id}`}
                aria-current={active ? 'page' : undefined}
                className={cn(
                  'block rounded px-2 py-1 text-[12.5px] text-ink-mute hover:bg-surface-3',
                  active && primaryActive,
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

const PRIMARY = [
  { to: '/', label: 'Home', Icon: Home, match: (p: string) => p === '/' },
  { to: '/projects', label: 'Projects', Icon: LayoutGrid, match: (p: string) => p.startsWith('/projects') },
  { to: '/audit', label: 'Activity', Icon: ScrollText, match: (p: string) => p === '/audit' },
  { to: '/members', label: 'Members', Icon: Users, match: (p: string) => p === '/members' },
  { to: '/tokens', label: 'Tokens', Icon: KeyRound, match: (p: string) => p === '/tokens' },
  { to: '/transit', label: 'Transit', Icon: Shield, match: (p: string) => p === '/transit' },
  { to: '/operations', label: 'Operations', Icon: RefreshCw, match: (p: string) => p === '/operations' },
  { to: '/settings', label: 'Settings', Icon: Settings, match: (p: string) => p === '/settings' },
]

const primaryItem =
  'mx-1 flex items-center gap-2.5 rounded px-2 py-1.5 text-[12.5px] font-medium text-ink-mute transition-nocturne hover:bg-surface-3 hover:text-ink'
const primaryActive =
  'bg-nav-active font-semibold text-ink shadow-[inset_2px_0_0_var(--nav-rail)] hover:bg-nav-active'

export function Sidebar() {
  const { projectId, configId } = useActiveIds()
  const location = useLocation()
  const navigate = useNavigate()
  const projects = useProjects()
  const envs = useEnvironments(projectId)
  const [open, setOpen] = useState<OpenForm>(null)
  const projectName = projects.data?.find((p) => p.id === projectId)?.name

  return (
    <nav className="text-sm">
      <div className="mb-2 flex flex-col gap-0.5">
        {PRIMARY.map(({ to, label, Icon, match }) => {
          const active = match(location.pathname)
          return (
            <Link
              key={to}
              to={to}
              aria-current={active ? 'page' : undefined}
              className={cn(primaryItem, active && primaryActive)}
            >
              <Icon size={15} strokeWidth={1.7} /> {label}
            </Link>
          )
        })}
      </div>

      {projectId && (
        <>
          <SectionLabel action={<IconAdd label="add environment" onClick={() => setOpen('env')} />}>
            {projectName ?? 'Project'}
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
