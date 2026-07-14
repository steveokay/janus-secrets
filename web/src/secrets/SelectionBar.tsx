import { Eye, Copy, Download, Trash2, X } from 'lucide-react'
import { Button } from '../ui/Button'

export function SelectionBar({ count, onReveal, onCopy, onDownload, onDelete, onClear }: {
  count: number
  onReveal: () => void
  onCopy: () => void
  onDownload: () => void
  onDelete: () => void
  onClear: () => void
}) {
  return (
    <div className="mb-3 flex flex-wrap items-center gap-2 rounded-bar border border-line bg-surface-2 px-3 py-2">
      <span className="text-[12.5px] font-semibold text-ink">{count} selected</span>
      <div className="ml-auto flex flex-wrap gap-2">
        <Button variant="secondary" size="sm" onClick={onReveal}><Eye size={14} strokeWidth={1.7} /> Reveal</Button>
        <Button variant="secondary" size="sm" onClick={onCopy}><Copy size={14} strokeWidth={1.7} /> Copy .env</Button>
        <Button variant="secondary" size="sm" onClick={onDownload}><Download size={14} strokeWidth={1.7} /> Download .env</Button>
        <Button variant="danger" size="sm" onClick={onDelete}><Trash2 size={14} strokeWidth={1.7} /> Delete</Button>
        <Button variant="secondary" size="sm" onClick={onClear}><X size={14} strokeWidth={1.7} /> Clear</Button>
      </div>
    </div>
  )
}
