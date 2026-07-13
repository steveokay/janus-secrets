import { useSearchParams, Link } from 'react-router-dom'
import { useTitle } from '../lib/title'
import { cn } from '../ui/cn'
import { InstanceSection } from './InstanceSection'
import { OIDCSection } from './OIDCSection'
import { FederationSection } from './FederationSection'
import { AppearanceSection } from './AppearanceSection'

const SECTIONS = [
  { key: 'instance', label: 'Instance', El: InstanceSection },
  { key: 'oidc', label: 'OIDC provider', El: OIDCSection },
  { key: 'federation', label: 'CI federation', El: FederationSection },
  { key: 'appearance', label: 'Appearance', El: AppearanceSection },
] as const

export function SettingsPage() {
  useTitle('Settings')
  const [params] = useSearchParams()
  const active = SECTIONS.find((s) => s.key === params.get('section')) ?? SECTIONS[0]
  const Active = active.El
  return (
    <div className="mx-auto max-w-5xl">
      <header className="mb-6">
        <h1 className="text-[19px] font-semibold tracking-tight text-ink">Settings</h1>
        <p className="text-[12.5px] text-ink-mute">Instance administration and preferences.</p>
      </header>
      <div className="flex flex-col gap-6 md:flex-row">
        <nav className="md:w-52 shrink-0" aria-label="Settings sections">
          <ul className="flex flex-col gap-0.5">
            {SECTIONS.map((s) => {
              const on = s.key === active.key
              return (
                <li key={s.key}>
                  <Link
                    to={`/settings?section=${s.key}`}
                    aria-current={on ? 'page' : undefined}
                    className={cn(
                      'flex rounded px-2.5 py-1.5 text-[12.5px] font-medium text-ink-mute transition-nocturne hover:bg-surface-3 hover:text-ink',
                      on && 'bg-nav-active font-semibold text-ink',
                    )}
                  >
                    {s.label}
                  </Link>
                </li>
              )
            })}
          </ul>
        </nav>
        <section className="min-w-0 flex-1">
          <Active />
        </section>
      </div>
    </div>
  )
}
