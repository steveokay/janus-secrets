import { AlertTriangle } from 'lucide-react'

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
    <div className="mt-3.5 flex items-center gap-3.5 rounded-card border border-brand-line bg-card p-3 px-4">
      <span className="flex items-center gap-2 text-[12.5px] text-muted">
        <AlertTriangle size={15} strokeWidth={1.7} className="text-brand-text" />
        <b className="font-semibold text-ink">+{summary.added} added</b> ·{' '}
        <b className="font-semibold text-ink">{summary.changed} changed</b> ·{' '}
        <b className="font-semibold text-ink">{summary.removed} removed</b>
      </span>
      <div className="ml-auto flex items-center gap-2">
        <button
          type="button"
          onClick={onReview}
          className="rounded px-3 py-1.5 text-[13px] font-semibold text-muted hover:text-ink"
        >
          Review diff
        </button>
        <button
          type="button"
          onClick={onDiscard}
          className="rounded border border-line bg-card px-3 py-1.5 text-[13px] font-semibold text-ink"
        >
          Discard
        </button>
        <button
          type="button"
          onClick={onSave}
          disabled={saving}
          className="rounded bg-brand px-4 py-1.5 text-[13px] font-semibold text-white shadow-card disabled:opacity-40"
        >
          {saving ? 'Saving…' : `Save as v${version + 1}`}
        </button>
      </div>
    </div>
  )
}
