import { Search } from 'lucide-react'
import { Brand } from '../ui/Brand'
import { Pill } from '../ui/Pill'
import { Breadcrumb } from './Breadcrumb'
import { UserMenu } from './UserMenu'
import { ThemeToggle } from './ThemeToggle'
import { usePalette } from '../palette/PaletteProvider'

export function TopBar({ sealed }: { sealed: boolean }) {
  const { open } = usePalette()
  return (
    <header className="flex items-center gap-4 border-b border-line bg-surface-1 px-4 py-2">
      <Brand />
      <Breadcrumb />
      <button
        type="button"
        onClick={open}
        aria-label="Open command palette"
        className="ml-4 flex min-w-[260px] items-center gap-2 rounded-pill bg-surface-3 px-3 py-1.5 text-[12.5px] text-ink-mute transition-nocturne hover:text-ink"
      >
        <Search size={14} strokeWidth={1.7} />
        <span>Search projects, configs, secrets…</span>
        <span className="ml-auto rounded-pill bg-surface-1 px-1.5 py-0.5 text-[10.5px] font-semibold text-ink-mute">⌘K</span>
      </button>
      <div className="ml-auto flex items-center gap-2.5">
        {sealed ? <Pill tone="danger" dot>Sealed</Pill> : <Pill tone="success" dot>Unsealed</Pill>}
        <ThemeToggle />
        <UserMenu />
      </div>
    </header>
  )
}
