import { useMemo, useState } from 'react'
import { parseDotenv } from './rowState'

// Bulk-paste a .env blob into the dirty buffer. Parsed values live only in the
// buffer (component state) until Save — nothing is persisted here.
export function ImportEnvDialog({ open, onClose, onApply }: {
  open: boolean
  onClose: () => void
  onApply: (pairs: Record<string, string>) => void
}) {
  const [text, setText] = useState('')
  const parsed = useMemo(() => parseDotenv(text), [text])
  if (!open) return null
  const count = Object.keys(parsed.pairs).length

  function close() {
    setText('')
    onClose()
  }

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-ink/30" onClick={close}>
      <div
        role="dialog"
        aria-label="Import .env"
        onClick={(e) => e.stopPropagation()}
        className="w-[440px] max-w-[92vw] rounded-card border border-line bg-card p-5 shadow-pop"
      >
        <h2 className="mb-1 text-[15px] font-semibold text-ink">Import .env</h2>
        <p className="mb-3 text-[12.5px] text-muted">
          Paste <span className="font-mono">KEY=VALUE</span> lines. They're staged as pending edits — nothing is saved until you Save.
        </p>
        <textarea
          aria-label="paste .env contents"
          value={text}
          onChange={(e) => setText(e.target.value)}
          rows={8}
          placeholder={'DATABASE_URL=postgres://…\n# comments and blank lines are ignored\nSTRIPE_KEY="sk_live_…"'}
          className="w-full resize-y rounded border border-line bg-card px-3 py-2 font-mono text-[12px] text-ink placeholder:text-faint"
        />
        <div className="mt-3 flex items-center justify-between">
          <span className="text-[12px] text-faint">
            {count} key{count === 1 ? '' : 's'}{parsed.skipped > 0 && ` · ${parsed.skipped} skipped`}
          </span>
          <div className="flex gap-2">
            <button
              type="button"
              onClick={close}
              className="rounded border border-line bg-card px-3 py-1.5 text-[13px] font-semibold text-ink"
            >
              Cancel
            </button>
            <button
              type="button"
              onClick={() => { onApply(parsed.pairs); close() }}
              disabled={count === 0}
              className="rounded bg-brand px-4 py-1.5 text-[13px] font-semibold text-white shadow-card disabled:opacity-40"
            >
              Import {count > 0 ? count : ''}
            </button>
          </div>
        </div>
      </div>
    </div>
  )
}
