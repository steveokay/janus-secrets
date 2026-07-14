import type { Ref } from 'react'
import { Search, Upload, History, Eye, EyeOff } from 'lucide-react'
import { Button } from '../ui/Button'

// Editor toolbar (mockup §06): key filter + Import .env + History + reveal-all.
export function EditorToolbar({ filter, onFilter, filterRef, onImport, onHistory, anyRevealed, onToggleRevealAll }: {
  filter: string
  onFilter: (v: string) => void
  filterRef?: Ref<HTMLInputElement>
  onImport: () => void
  onHistory: () => void
  anyRevealed: boolean
  onToggleRevealAll: () => void
}) {
  return (
    <div className="mb-3.5 flex flex-wrap items-center gap-2.5">
      <div className="flex max-w-[260px] flex-1 items-center gap-2 rounded border border-line bg-surface-3 px-2.5 py-1.5 focus-within:border-brand-line">
        <Search size={14} strokeWidth={1.7} className="shrink-0 text-ink-faint" />
        <input
          ref={filterRef}
          type="search"
          role="searchbox"
          aria-label="filter keys"
          value={filter}
          onChange={(e) => onFilter(e.target.value)}
          placeholder="Filter keys…"
          className="min-w-0 flex-1 bg-transparent text-[12.5px] text-ink outline-none placeholder:text-ink-faint"
        />
      </div>
      <Button variant="secondary" size="sm" onClick={onImport}>
        <Upload size={14} strokeWidth={1.7} /> Import .env
      </Button>
      <Button variant="secondary" size="sm" onClick={onHistory}>
        <History size={14} strokeWidth={1.7} /> History
      </Button>
      <Button variant="secondary" size="sm" onClick={onToggleRevealAll}>
        {anyRevealed ? <EyeOff size={14} strokeWidth={1.7} /> : <Eye size={14} strokeWidth={1.7} />}
        {anyRevealed ? 'Hide all' : 'Reveal all'}
      </Button>
    </div>
  )
}
