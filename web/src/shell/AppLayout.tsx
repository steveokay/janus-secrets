import { ReactNode, useEffect, useState } from 'react'
import { useLocation } from 'react-router-dom'
import { TopBar } from './TopBar'
import { cn } from '../ui/cn'

export function AppLayout({ sealed, sidebar, children }: { sealed: boolean; sidebar: ReactNode; children: ReactNode }) {
  const [mobileOpen, setMobileOpen] = useState(false)
  const location = useLocation()

  // Navigating anywhere closes the off-canvas sidebar (narrow viewports only —
  // harmless no-op at sm: and above, where the sidebar is never overlaid).
  useEffect(() => { setMobileOpen(false) }, [location.pathname])

  return (
    <div className="flex h-screen flex-col bg-transparent">
      <TopBar sealed={sealed} onMenuClick={() => setMobileOpen((o) => !o)} />
      <div className="flex min-h-0 flex-1">
        <aside className="hidden w-60 shrink-0 overflow-y-auto border-r border-line bg-card px-2.5 py-3.5 sm:block">
          {sidebar}
        </aside>
        {mobileOpen && (
          <div className="fixed inset-0 z-40 sm:hidden">
            <div
              aria-hidden="true"
              onClick={() => setMobileOpen(false)}
              className="absolute inset-0 bg-ink/30 backdrop-blur-[8px]"
            />
            <aside
              aria-label="sidebar"
              className={cn(
                'absolute inset-y-0 left-0 w-64 max-w-[80vw] overflow-y-auto border-r border-line bg-card px-2.5 py-3.5 shadow-pop',
              )}
            >
              {sidebar}
            </aside>
          </div>
        )}
        <main className="min-w-0 flex-1 overflow-y-auto p-6">{children}</main>
      </div>
    </div>
  )
}
