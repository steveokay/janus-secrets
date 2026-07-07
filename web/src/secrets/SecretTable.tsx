import type { ReactNode } from 'react'
import { Eye, Copy, Pencil, X, Undo2, RotateCcw } from 'lucide-react'
import type { MaskedSecret } from '../lib/endpoints'
import type { Buffer } from './dirty'
import { Pill } from '../ui/Pill'
import type { Tone } from '../ui/Pill'
import { rowState } from './rowState'
import { cn } from '../ui/cn'

const GRID = 'grid grid-cols-[1.3fr_1.5fr_108px_56px_92px] items-center gap-3 px-4'

const railTone: Record<'added' | 'edited' | 'removed', string> = {
  added: 'bg-success',
  edited: 'bg-warning',
  removed: 'bg-danger',
}
const chipTone: Record<'added' | 'edited' | 'removed', string> = {
  added: 'text-success',
  edited: 'text-warning',
  removed: 'text-danger',
}
const originTone: Record<MaskedSecret['origin'], Tone> = {
  own: 'success',
  inherited: 'muted',
  overridden: 'brand',
}

function IconButton({ label, onClick, children }: { label: string; onClick: () => void; children: ReactNode }) {
  return (
    <button
      type="button"
      aria-label={label}
      onClick={onClick}
      className="inline-flex h-6 w-6 items-center justify-center rounded text-faint hover:text-brand-text"
    >
      {children}
    </button>
  )
}

const ICON = { size: 14, strokeWidth: 1.7 } as const

export function SecretTable({
  rows, masked, buffer, original, editing, revealed, filter,
  onReveal, onCopy, onEdit, onChangeValue, onRemove, onRevert,
}: {
  rows: string[]
  masked: Record<string, MaskedSecret>
  buffer: Buffer
  original: Record<string, string>
  editing: Record<string, boolean>
  revealed: Record<string, string>
  filter: string
  onReveal: (key: string) => void
  onCopy: (key: string) => void
  onEdit: (key: string) => void
  onChangeValue: (key: string, value: string) => void
  onRemove: (key: string) => void
  onRevert: (key: string) => void
}) {
  const q = filter.trim().toLowerCase()
  const visible = q ? rows.filter((k) => k.toLowerCase().includes(q)) : rows

  return (
    <div className="rounded-card border border-line bg-card overflow-hidden">
      <div className={cn(GRID, 'sticky top-0 z-10 bg-page py-2.5')}>
        {(['Key', 'Value', 'Origin', 'Ver', 'Actions'] as const).map((label) => (
          <span
            key={label}
            className={cn('text-[10.5px] font-bold uppercase tracking-[.1em] text-faint', label === 'Actions' && 'text-right')}
          >
            {label}
          </span>
        ))}
      </div>
      {visible.map((key) => {
        const st = rowState(key, masked, buffer, original)
        const isEditing = !!editing[key]
        const isRemoved = st.change === 'removed'
        const strike = isRemoved ? 'line-through opacity-45' : ''
        return (
          <div key={key} className={cn('group relative border-t border-line-soft', GRID, 'py-2.5')}>
            {st.change && <span className={cn('absolute left-0 top-0 bottom-0 w-[3px]', railTone[st.change])} />}

            {/* Key */}
            <span className={cn('font-mono text-[12.5px] font-semibold text-ink truncate', strike)}>
              {key}
              {st.change && (
                <span className={cn('ml-1.5 text-[10px] font-bold uppercase tracking-[.06em]', chipTone[st.change])}>
                  {st.change}
                </span>
              )}
            </span>

            {/* Value */}
            <span className={cn('font-mono text-[12.5px] text-muted flex items-center gap-2 min-w-0', strike)}>
              {isEditing ? (
                <input
                  aria-label={`value for ${key}`}
                  value={key in buffer ? (buffer[key].value ?? '') : (original[key] ?? '')}
                  onChange={(e) => onChangeValue(key, e.target.value)}
                  className="w-full rounded border border-line bg-card px-2.5 py-1 font-mono text-[12.5px] text-ink focus:border-brand"
                />
              ) : (
                <>
                  <span className="truncate">{key in revealed ? revealed[key] : '••••••••••••'}</span>
                  {st.existing && (
                    <span className="inline-flex gap-1 opacity-0 group-hover:opacity-100">
                      {!(key in revealed) && (
                        <IconButton label={`reveal ${key}`} onClick={() => onReveal(key)}><Eye {...ICON} /></IconButton>
                      )}
                      <IconButton label={`copy ${key}`} onClick={() => onCopy(key)}><Copy {...ICON} /></IconButton>
                    </span>
                  )}
                </>
              )}
            </span>

            {/* Origin */}
            <span>
              <Pill tone={originTone[st.origin]}>{st.origin}</Pill>
            </span>

            {/* Ver */}
            <span className="text-faint text-[12px] tabular-nums">
              {st.existing ? `v${masked[key].value_version}` : '—'}
            </span>

            {/* Actions */}
            <span className="flex justify-end gap-1">
              {isEditing ? (
                <IconButton label={`cancel edit ${key}`} onClick={() => onRevert(key)}><X {...ICON} /></IconButton>
              ) : st.change === 'added' ? (
                <>
                  <IconButton label={`edit ${key}`} onClick={() => onEdit(key)}><Pencil {...ICON} /></IconButton>
                  <IconButton label={`discard ${key}`} onClick={() => onRevert(key)}><X {...ICON} /></IconButton>
                </>
              ) : st.change === 'edited' ? (
                <>
                  <IconButton label={`revert ${key}`} onClick={() => onRevert(key)}><Undo2 {...ICON} /></IconButton>
                  <IconButton label={`remove ${key}`} onClick={() => onRemove(key)}><X {...ICON} /></IconButton>
                </>
              ) : st.change === 'removed' ? (
                <IconButton label={`restore ${key}`} onClick={() => onRevert(key)}><RotateCcw {...ICON} /></IconButton>
              ) : st.origin === 'inherited' ? (
                <IconButton label={`edit ${key}`} onClick={() => onEdit(key)}><Pencil {...ICON} /></IconButton>
              ) : (
                <>
                  <IconButton label={`edit ${key}`} onClick={() => onEdit(key)}><Pencil {...ICON} /></IconButton>
                  <IconButton label={`remove ${key}`} onClick={() => onRemove(key)}><X {...ICON} /></IconButton>
                </>
              )}
            </span>
          </div>
        )
      })}
    </div>
  )
}
