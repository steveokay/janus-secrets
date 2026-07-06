import { ReactNode } from 'react'
import { TopBar } from './TopBar'

export function AppLayout({ sealed, sidebar, children }: { sealed: boolean; sidebar: ReactNode; children: ReactNode }) {
  return (
    <div className="flex h-screen flex-col bg-page">
      <TopBar sealed={sealed} />
      <div className="flex min-h-0 flex-1">
        <aside className="w-60 shrink-0 overflow-y-auto border-r border-line bg-card px-2.5 py-3.5">{sidebar}</aside>
        <main className="min-w-0 flex-1 overflow-y-auto p-6">{children}</main>
      </div>
    </div>
  )
}
