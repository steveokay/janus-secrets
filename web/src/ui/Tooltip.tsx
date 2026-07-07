import type { ReactNode } from 'react'
import * as RT from '@radix-ui/react-tooltip'

// Token-styled tooltip for icon-buttons. `content` is a short label — never a
// secret value. Wraps a single focusable trigger child.
export function Tooltip({ content, children, delay = 300 }: {
  content: string
  children: ReactNode
  delay?: number
}) {
  return (
    <RT.Provider delayDuration={delay}>
      <RT.Root>
        <RT.Trigger asChild>{children}</RT.Trigger>
        <RT.Portal>
          <RT.Content
            sideOffset={6}
            className="z-50 select-none rounded bg-ink px-2 py-1 text-[11.5px] text-card shadow-pop"
          >
            {content}
            <RT.Arrow className="fill-ink" />
          </RT.Content>
        </RT.Portal>
      </RT.Root>
    </RT.Provider>
  )
}
