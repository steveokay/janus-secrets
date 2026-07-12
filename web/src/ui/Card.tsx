import type { ReactNode } from 'react'
import { cn } from './cn'

export function Card({ className, children }: { className?: string; children: ReactNode }) {
  return <div className={cn('rounded-card border border-line bg-card shadow-elev-1', className)}>{children}</div>
}
