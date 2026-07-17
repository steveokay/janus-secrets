import type { ReactNode } from 'react'
import { Eye, Copy, Pencil, X, Undo2, RotateCcw, ChevronUp, ChevronDown, Lock, LockOpen, Wand2, AlertTriangle } from 'lucide-react'
import type { MaskedSecret } from '../lib/endpoints'
import type { Buffer } from './dirty'
import { Pill } from '../ui/Pill'
import type { Tone } from '../ui/Pill'
import { rowState } from './rowState'
import { relativeTime } from '../lib/relativeTime'
import type { SortKey, SortState } from './sortRows'
import { cn } from '../ui/cn'
import { SECRET_TYPES, SECRET_TYPE_ORDER, normalizeType } from './secretTypes'
import type { SecretType } from './secretTypes'
import { generatePassword } from './generatePassword'

const GRID = 'grid grid-cols-[24px_1.2fr_1.4fr_104px_92px_56px_96px] items-center gap-3 px-4'

const railTone: Record<'added' | 'edited' | 'removed', string> = {
  added: 'bg-success',
  edited: 'bg-warning',
  removed: 'bg-danger',
}
// Row washes (background-images — they compose with the hover background-color).
const washTone: Record<'added' | 'edited' | 'removed', string> = {
  added: 'bg-added-wash',
  edited: 'bg-dirty-wash',
  removed: 'bg-removed-wash',
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
      className="inline-flex h-6 w-6 items-center justify-center rounded text-ink-faint hover:text-brand-text hover:bg-surface-3"
    >
      {children}
    </button>
  )
}

const ICON = { size: 14, strokeWidth: 1.7 } as const

function HeaderCell({ label, sortKey, sort, onSort }: {
  label: string; sortKey: SortKey; sort: SortState; onSort: (k: SortKey) => void
}) {
  const on = sort?.key === sortKey
  return (
    <button
      type="button"
      aria-label={`sort by ${label.toLowerCase()}`}
      onClick={() => onSort(sortKey)}
      className={cn('flex items-center gap-1 text-left text-[10.5px] font-bold uppercase tracking-[.1em] transition-nocturne',
        on ? 'text-brand-text' : 'text-ink-faint hover:text-ink-mute')}
    >
      {label}
      {on && (sort!.dir === 'asc' ? <ChevronUp size={12} strokeWidth={2} /> : <ChevronDown size={12} strokeWidth={2} />)}
    </button>
  )
}

export function SecretTable({
  rows, masked, buffer, original, editing, revealed,
  sort, onSort, selected, onToggleSelect, onSelectAll, active,
  onReveal, onCopy, onEdit, onChangeValue, onRemove, onRevert,
  lockedKeys, onToggleLock, onOpenHistory, onChangeType,
}: {
  rows: string[]
  masked: Record<string, MaskedSecret>
  buffer: Buffer
  original: Record<string, string>
  editing: Record<string, boolean>
  revealed: Record<string, string>
  sort: SortState
  onSort: (key: SortKey) => void
  selected: Set<string>
  onToggleSelect: (key: string) => void
  onSelectAll: (visibleKeys: string[]) => void
  active: string | null
  onReveal: (key: string) => void
  onCopy: (key: string) => void
  onEdit: (key: string) => void
  onChangeValue: (key: string, value: string) => void
  onRemove: (key: string) => void
  onRevert: (key: string) => void
  lockedKeys: Set<string>
  onToggleLock: (key: string) => void
  onOpenHistory: (key: string) => void
  onChangeType: (key: string, type: SecretType) => void
}) {
  return (
    <div className="overflow-x-auto">
      <div className="min-w-[844px] rounded-card border border-line bg-card shadow-elev-1 overflow-hidden">
      <div className={cn(GRID, 'bg-surface-1 py-2.5')}>
        <span className="flex items-center">
          <input
            type="checkbox"
            aria-label="select all"
            className="h-3.5 w-3.5 accent-brand"
            ref={(el) => { if (el) el.indeterminate = selected.size > 0 && !rows.every((k) => selected.has(k)) }}
            checked={rows.length > 0 && rows.every((k) => selected.has(k))}
            onChange={() => onSelectAll(rows)}
          />
        </span>
        <HeaderCell label="Key" sortKey="key" sort={sort} onSort={onSort} />
        <span className="text-[10.5px] font-bold uppercase tracking-[.1em] text-ink-faint">Value</span>
        <HeaderCell label="Origin" sortKey="origin" sort={sort} onSort={onSort} />
        <HeaderCell label="Updated" sortKey="updated" sort={sort} onSort={onSort} />
        <HeaderCell label="Version" sortKey="version" sort={sort} onSort={onSort} />
        <span className="text-right text-[10.5px] font-bold uppercase tracking-[.1em] text-ink-faint">Actions</span>
      </div>
      {rows.map((key) => {
        const st = rowState(key, masked, buffer, original)
        const isEditing = !!editing[key]
        const isRemoved = st.change === 'removed'
        const strike = isRemoved ? 'line-through opacity-45' : ''
        const serverType = normalizeType(masked[key]?.type)
        const bufferType = buffer[key]?.type
        const effectiveType = bufferType !== undefined ? normalizeType(bufferType) : serverType
        const spec = SECRET_TYPES[effectiveType]
        const TypeIcon = spec.icon
        // Current value for warn-only validation: the live buffer/original value
        // while editing, or the on-demand revealed value otherwise. A value that
        // hasn't been fetched yet (masked, unrevealed) is never validated.
        const currentValue = isEditing
          ? (key in buffer ? (buffer[key].value ?? '') : (original[key] ?? ''))
          : (key in revealed ? revealed[key] : undefined)
        const validationError = spec.validate && currentValue !== undefined ? spec.validate(currentValue) : null
        return (
          <div key={key} className={cn('group relative border-t border-line-soft hover:bg-row-hover transition-nocturne', GRID, 'py-2.5', st.change && washTone[st.change], active === key && 'ring-1 ring-inset ring-brand-line')}>
            {st.change && <span className={cn('absolute left-0 top-0 bottom-0 w-[3px]', railTone[st.change])} />}

            {/* Select */}
            <span className="flex items-center">
              <input
                type="checkbox"
                aria-label={`select ${key}`}
                className="h-3.5 w-3.5 accent-brand"
                checked={selected.has(key)}
                onChange={() => onToggleSelect(key)}
              />
            </span>

            {/* Key */}
            <span className={cn('font-mono text-[12.5px] font-semibold text-ink truncate flex items-center gap-1', strike)}>
              <TypeIcon size={11} strokeWidth={1.9} className="shrink-0 text-ink-faint" aria-hidden />
              <span className="truncate">{key}</span>
              {lockedKeys.has(key) && (
                <Lock size={11} strokeWidth={1.9} className="shrink-0 text-warning" aria-label={`${key} is promotion-locked`} />
              )}
              {st.change && (
                <span className={cn('ml-0.5 text-[10px] font-bold uppercase tracking-[.06em]', chipTone[st.change])}>
                  {st.change}
                </span>
              )}
            </span>

            {/* Value */}
            <span className={cn('font-mono text-[12.5px] text-ink-mute flex items-center gap-2 min-w-0', strike)}>
              {isEditing ? (
                spec.multiline ? (
                  <textarea
                    aria-label={`value for ${key}`}
                    data-testid={`value-${key}`}
                    value={key in buffer ? (buffer[key].value ?? '') : (original[key] ?? '')}
                    onChange={(e) => onChangeValue(key, e.target.value)}
                    onKeyDown={(e) => { if (e.key === 'Escape') onRevert(key) }}
                    rows={3}
                    className="w-full resize-y rounded border border-line bg-surface-3 px-2.5 py-1 font-mono text-[12.5px] text-ink focus:border-brand-line focus:shadow-glow-soft"
                  />
                ) : (
                  <input
                    aria-label={`value for ${key}`}
                    data-testid={`value-${key}`}
                    value={key in buffer ? (buffer[key].value ?? '') : (original[key] ?? '')}
                    onChange={(e) => onChangeValue(key, e.target.value)}
                    onKeyDown={(e) => { if (e.key === 'Escape') onRevert(key) }}
                    className="w-full rounded border border-line bg-surface-3 px-2.5 py-1 font-mono text-[12.5px] text-ink focus:border-brand-line focus:shadow-glow-soft"
                  />
                )
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
              {isEditing && spec.generate && (
                <IconButton label={`generate ${key}`} onClick={() => onChangeValue(key, generatePassword())}>
                  <Wand2 {...ICON} />
                </IconButton>
              )}
              {validationError && (
                <span
                  className="inline-flex shrink-0 items-center gap-1 rounded bg-warning-soft px-1.5 py-0.5 text-[10.5px] font-semibold text-warning"
                  title={validationError}
                >
                  <AlertTriangle size={11} strokeWidth={1.9} aria-hidden />
                  {validationError}
                </span>
              )}
            </span>

            {/* Origin */}
            <span className="flex flex-col items-start gap-1">
              <Pill tone={originTone[st.origin]}>{st.origin}</Pill>
              <select
                aria-label={`type for ${key}`}
                value={effectiveType}
                onChange={(e) => onChangeType(key, e.target.value as SecretType)}
                className="w-full rounded border border-line bg-surface-3 px-1 py-0.5 text-[10.5px] text-ink-mute focus:border-brand-line focus:shadow-glow-soft"
              >
                {SECRET_TYPE_ORDER.map((t) => (
                  <option key={t} value={t}>{SECRET_TYPES[t].label}</option>
                ))}
              </select>
            </span>

            {/* Updated */}
            <span className="text-ink-faint text-[12px] tabular-nums truncate">
              {st.existing ? relativeTime(masked[key].created_at) : '—'}
            </span>

            {/* Ver */}
            <span className="text-ink-faint text-[12px] tabular-nums">
              {st.existing ? (
                <button
                  type="button"
                  aria-label={`history for ${key}`}
                  onClick={() => onOpenHistory(key)}
                  className="rounded px-1 tabular-nums text-ink-faint hover:bg-surface-3 hover:text-brand-text"
                >
                  v{masked[key].value_version}
                </button>
              ) : '—'}
            </span>

            {/* Actions */}
            <span className="flex justify-end gap-1">
              {st.existing && (
                lockedKeys.has(key) ? (
                  <IconButton label={`unlock ${key}`} onClick={() => onToggleLock(key)}><Lock {...ICON} /></IconButton>
                ) : (
                  <IconButton label={`lock ${key}`} onClick={() => onToggleLock(key)}><LockOpen {...ICON} /></IconButton>
                )
              )}
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
    </div>
  )
}
