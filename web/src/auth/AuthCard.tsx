import type { ReactNode } from 'react'
import { Brand } from '../ui/Brand'

// Centered, branded card on the page background — the shell for login + unseal
// (mockup §07). Presentation only; behavior lives in the composing screens.
export function AuthCard({ children }: { children: ReactNode }) {
  return (
    <div className="flex min-h-screen items-center justify-center bg-page px-4">
      <div className="w-[340px] max-w-full rounded-[14px] border border-line bg-card p-7 text-center shadow-elev-1">
        <div className="mx-auto mb-4 flex h-11 w-11 items-center justify-center rounded-xl border border-brand-line bg-brand-soft">
          <Brand markOnly size={24} />
        </div>
        {children}
      </div>
    </div>
  )
}
