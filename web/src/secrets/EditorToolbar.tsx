import { Search, Upload, History, Eye, EyeOff } from 'lucide-react'

// Editor toolbar (mockup §06): key filter + Import .env + History + reveal-all.
export function EditorToolbar({ filter, onFilter, onImport, onHistory, anyRevealed, onToggleRevealAll }: {
  filter: string
  onFilter: (v: string) => void
  onImport: () => void
  onHistory: () => void
  anyRevealed: boolean
  onToggleRevealAll: () => void
}) {
  return (
    <div className="mb-3.5 flex flex-wrap items-center gap-2.5">
      <div className="flex max-w-[260px] flex-1 items-center gap-2 rounded border border-line bg-card px-2.5 py-1.5">
        <Search size={14} strokeWidth={1.7} className="shrink-0 text-faint" />
        <input
          type="search"
          role="searchbox"
          aria-label="filter keys"
          value={filter}
          onChange={(e) => onFilter(e.target.value)}
          placeholder="Filter keys…"
          className="min-w-0 flex-1 bg-transparent text-[12.5px] text-ink outline-none placeholder:text-faint"
        />
      </div>
      <button
        type="button"
        onClick={onImport}
        className="flex items-center gap-1.5 rounded border border-line bg-card px-3 py-1.5 text-[13px] font-semibold text-ink"
      >
        <Upload size={14} strokeWidth={1.7} /> Import .env
      </button>
      <button
        type="button"
        onClick={onHistory}
        className="flex items-center gap-1.5 rounded border border-line bg-card px-3 py-1.5 text-[13px] font-semibold text-ink"
      >
        <History size={14} strokeWidth={1.7} /> History
      </button>
      <button
        type="button"
        onClick={onToggleRevealAll}
        className="flex items-center gap-1.5 rounded border border-line bg-card px-3 py-1.5 text-[13px] font-semibold text-ink"
      >
        {anyRevealed ? <EyeOff size={14} strokeWidth={1.7} /> : <Eye size={14} strokeWidth={1.7} />}
        {anyRevealed ? 'Hide all' : 'Reveal all'}
      </button>
    </div>
  )
}
