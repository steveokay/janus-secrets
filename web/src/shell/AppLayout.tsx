import { ReactNode } from 'react'
import { TopBar } from './TopBar'

export function AppLayout({ sidebar, children }: { sidebar: ReactNode; children: ReactNode }) {
  return (
    <div className="flex h-screen flex-col">
      <TopBar sealed={false} />
      <div className="flex min-h-0 flex-1">
        <aside className="w-64 shrink-0 overflow-y-auto border-r p-3">{sidebar}</aside>
        <main className="min-w-0 flex-1 overflow-y-auto p-4">{children}</main>
      </div>
    </div>
  )
}
