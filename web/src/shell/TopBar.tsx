import { Brand } from '../ui/Brand'
import { Pill } from '../ui/Pill'
import { Breadcrumb } from './Breadcrumb'
import { UserMenu } from './UserMenu'

export function TopBar({ sealed }: { sealed: boolean }) {
  return (
    <header className="flex items-center gap-5 border-b border-line bg-card px-4 py-2">
      <Brand />
      <Breadcrumb />
      <div className="ml-auto flex items-center gap-3.5">
        {sealed ? (
          <Pill tone="danger" dot>Sealed</Pill>
        ) : (
          <Pill tone="success" dot>Unsealed</Pill>
        )}
        <UserMenu />
      </div>
    </header>
  )
}
