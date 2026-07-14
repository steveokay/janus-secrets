import { X } from 'lucide-react'
import { Button } from '../ui/Button'

export interface BulkAction { label: string; onClick: () => void; tone?: 'secondary' | 'danger'; loading?: boolean; disabled?: boolean }

export function OpsSelectionBar({ count, actions, onClear }: { count: number; actions: BulkAction[]; onClear: () => void }) {
  return (
    <div role="toolbar" aria-label="bulk actions" className="mb-3 flex flex-wrap items-center gap-2 rounded-bar border border-line bg-surface-2 px-3 py-2">
      <span className="text-[12.5px] font-semibold text-ink">{count} selected</span>
      <div className="ml-auto flex flex-wrap gap-2">
        {actions.map((a) => (
          <Button key={a.label} variant={a.tone === 'danger' ? 'danger' : 'secondary'} size="sm" loading={a.loading} disabled={a.disabled} onClick={a.onClick}>{a.label}</Button>
        ))}
        <Button variant="ghost" size="sm" onClick={onClear}><X size={14} strokeWidth={1.7} /> Clear</Button>
      </div>
    </div>
  )
}
