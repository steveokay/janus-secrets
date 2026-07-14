import { useMemo, useState } from 'react'
import { parseDotenv } from './rowState'
import { classifyImport } from './importClassify'
import type { MaskedSecret } from '../lib/endpoints'
import { Modal } from '../ui/Modal'
import { Button } from '../ui/Button'
import { Pill } from '../ui/Pill'

// Bulk-paste a .env blob into the dirty buffer. Parsed values live only in the
// buffer (component state) until Save — nothing is persisted here.
export function ImportEnvDialog({ open, onClose, onApply, masked }: {
  open: boolean
  onClose: () => void
  onApply: (pairs: Record<string, string>) => void
  masked: Record<string, MaskedSecret>
}) {
  const [text, setText] = useState('')
  const parsed = useMemo(() => parseDotenv(text), [text])
  const count = Object.keys(parsed.pairs).length
  // Value-free preview: key names + add/update badge only. NEVER the value.
  const rows = useMemo(() => classifyImport(parsed.pairs, masked), [parsed.pairs, masked])

  function close() {
    setText('')
    onClose()
  }

  return (
    <Modal open={open} onClose={close} label="Import .env" className="w-[440px]">
      <h2 className="mb-1 text-[15px] font-semibold text-ink-hi">Import .env</h2>
      <p className="mb-3 text-[12.5px] text-ink-mute">
        Paste <span className="font-mono">KEY=VALUE</span> lines. They're staged as pending edits — nothing is saved until you Save.
      </p>
      <textarea
        aria-label="paste .env contents"
        value={text}
        onChange={(e) => setText(e.target.value)}
        rows={8}
        placeholder={'DATABASE_URL=postgres://…\n# comments and blank lines are ignored\nSTRIPE_KEY="sk_live_…"'}
        className="w-full resize-y rounded border border-line bg-surface-3 px-3 py-2 font-mono text-[12px] text-ink placeholder:text-ink-faint focus:border-brand-line focus:shadow-glow-soft transition-nocturne"
      />
      {rows.length > 0 && (
        <ul className="mt-3 max-h-40 overflow-y-auto rounded border border-line bg-surface-2 p-2 text-[12px]">
          {rows.map((r) => (
            <li key={r.key} className="flex items-center justify-between py-0.5">
              <span className="font-mono text-ink truncate">{r.key}</span>
              <Pill tone={r.kind === 'add' ? 'success' : 'warning'}>{r.kind}</Pill>
            </li>
          ))}
        </ul>
      )}
      <div className="mt-3 flex items-center justify-between">
        <span className="text-[12px] text-ink-faint">
          {count} key{count === 1 ? '' : 's'}{parsed.skipped > 0 && ` · ${parsed.skipped} skipped`}
        </span>
        <div className="flex gap-2">
          <Button variant="secondary" onClick={close}>
            Cancel
          </Button>
          <Button
            variant="primary"
            onClick={() => { onApply(parsed.pairs); close() }}
            disabled={count === 0}
          >
            Import {count > 0 ? count : ''}
          </Button>
        </div>
      </div>
    </Modal>
  )
}
