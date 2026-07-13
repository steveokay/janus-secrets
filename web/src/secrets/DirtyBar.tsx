import { AlertTriangle } from 'lucide-react'
import { Button } from '../ui/Button'

// Bottom pending-changes bar (mockup §06). Rendered by the editor only when dirty.
export function DirtyBar({ summary, version, saving, onReview, onDiscard, onSave }: {
  summary: { added: number; changed: number; removed: number }
  version: number
  saving: boolean
  onReview: () => void
  onDiscard: () => void
  onSave: () => void
}) {
  return (
    <div className="mt-3.5 flex items-center gap-3.5 rounded-bar border border-brand-line bg-elevated p-3 px-4 shadow-bar">
      <span className="flex items-center gap-2 text-[12.5px] text-ink-mute">
        <AlertTriangle size={15} strokeWidth={1.7} className="text-brand-text" />
        <b className="font-semibold text-ink">+{summary.added} added</b> ·{' '}
        <b className="font-semibold text-ink">{summary.changed} changed</b> ·{' '}
        <b className="font-semibold text-ink">{summary.removed} removed</b>
      </span>
      <div className="ml-auto flex items-center gap-2">
        <Button variant="ghost" onClick={onReview}>
          Review diff
        </Button>
        <Button variant="secondary" onClick={onDiscard}>
          Discard
        </Button>
        <Button variant="primary" onClick={onSave} disabled={saving}>
          {saving ? 'Saving…' : `Save as v${version + 1}`}
        </Button>
      </div>
    </div>
  )
}
