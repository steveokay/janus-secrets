import type { MaskedSecret } from '../lib/endpoints'
import type { Buffer } from './dirty'
import { rowState } from './rowState'
import { cn } from '../ui/cn'
import { Modal } from '../ui/Modal'

// Pre-save change review. Lists pending key names by change type — NEVER values
// (values stay in-row on reveal; this surface is value-free).
export function ReviewDiffDialog({ open, onClose, buffer, masked, original, version, saving, onSave }: {
  open: boolean
  onClose: () => void
  buffer: Buffer
  masked: Record<string, MaskedSecret>
  original: Record<string, string>
  version: number
  saving: boolean
  onSave: () => void
}) {
  const groups: { added: string[]; edited: string[]; removed: string[] } = { added: [], edited: [], removed: [] }
  for (const key of Object.keys(buffer)) {
    const { change } = rowState(key, masked, buffer, original)
    if (change) groups[change].push(key)
  }
  const sections: Array<{ title: string; keys: string[]; dot: string }> = [
    { title: 'Added', keys: groups.added, dot: 'bg-success' },
    { title: 'Changed', keys: groups.edited, dot: 'bg-warning' },
    { title: 'Removed', keys: groups.removed, dot: 'bg-danger' },
  ]
  const empty = sections.every((s) => s.keys.length === 0)

  return (
    <Modal open={open} onClose={onClose} label="Review changes" className="w-[380px]">
      <h2 className="mb-3 text-[15px] font-semibold text-ink">Review changes</h2>
      <div className="flex max-h-[50vh] flex-col gap-3 overflow-y-auto">
        {empty && <p className="text-[12.5px] text-faint">No pending changes.</p>}
        {sections.filter((s) => s.keys.length > 0).map((s) => (
          <div key={s.title}>
            <div className="mb-1 text-[10.5px] font-bold uppercase tracking-[.1em] text-faint">
              {s.title} ({s.keys.length})
            </div>
            <ul className="flex flex-col gap-1">
              {s.keys.map((k) => (
                <li key={k} className="flex items-center gap-2 font-mono text-[12.5px] text-ink">
                  <span className={cn('h-1.5 w-1.5 rounded-full', s.dot)} />
                  {k}
                </li>
              ))}
            </ul>
          </div>
        ))}
      </div>
      <div className="mt-4 flex justify-end gap-2">
        <button
          type="button"
          onClick={onClose}
          className="rounded border border-line bg-card px-3 py-1.5 text-[13px] font-semibold text-ink"
        >
          Cancel
        </button>
        <button
          type="button"
          onClick={onSave}
          disabled={saving || empty}
          className="rounded bg-brand px-4 py-1.5 text-[13px] font-semibold text-white shadow-card disabled:opacity-40"
        >
          {saving ? 'Saving…' : `Save as v${version + 1}`}
        </button>
      </div>
    </Modal>
  )
}
