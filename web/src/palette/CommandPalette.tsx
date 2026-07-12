import { useMemo, useState, useEffect, useRef } from 'react'
import * as Dialog from '@radix-ui/react-dialog'
import { fuzzyScore } from './fuzzy'
import type { PaletteItem, PaletteGroup } from './usePaletteItems'

const GROUP_ORDER: PaletteGroup[] = ['Projects', 'Configs', 'Secrets', 'Actions']

export function CommandPalette({
  open, items, onClose, onSelect,
}: {
  open: boolean
  items: PaletteItem[]
  onClose: () => void
  onSelect: (item: PaletteItem) => void
}) {
  const [query, setQuery] = useState('')
  const [active, setActive] = useState(0)
  const listRef = useRef<HTMLDivElement>(null)

  // Filter + rank, then order by group. `filtered` is the flat nav order.
  const filtered = useMemo(() => {
    const scored = items
      .map((it) => ({ it, score: fuzzyScore(query, `${it.label} ${it.keywords}`) }))
      .filter((s): s is { it: PaletteItem; score: number } => s.score !== null)
    scored.sort((a, b) => b.score - a.score)
    return GROUP_ORDER.flatMap((g) => scored.filter((s) => s.it.group === g).map((s) => s.it))
  }, [items, query])

  useEffect(() => { setActive(0) }, [query, open])
  // A stale search must not carry across opens.
  useEffect(() => { if (open) setQuery('') }, [open])

  function commit(item: PaletteItem | undefined) {
    if (item) onSelect(item)
  }

  function onKeyDown(e: React.KeyboardEvent) {
    if (e.key === 'ArrowDown') { e.preventDefault(); setActive((a) => Math.min(a + 1, filtered.length - 1)) }
    else if (e.key === 'ArrowUp') { e.preventDefault(); setActive((a) => Math.max(a - 1, 0)) }
    // `isComposing` guards against committing mid-IME-composition (e.g. an
    // Enter that confirms a candidate rather than selecting a result).
    else if (e.key === 'Enter' && !e.nativeEvent.isComposing) { e.preventDefault(); commit(filtered[active]) }
  }

  // Running flat index across groups; equals the item's index in `filtered`
  // because rows render in the SAME group order used to build `filtered`.
  let flatIndex = -1

  return (
    <Dialog.Root open={open} onOpenChange={(o) => { if (!o) onClose() }}>
      <Dialog.Portal>
        <Dialog.Overlay className="fixed inset-0 z-40 bg-ink/40 backdrop-blur-[8px]" />
        <Dialog.Content
          aria-label="Command palette"
          onOpenAutoFocus={(e) => e.preventDefault()}
          className="fixed left-1/2 top-[15vh] z-50 w-[min(560px,92vw)] -translate-x-1/2 overflow-hidden rounded-card bg-elevated shadow-pop"
        >
          <Dialog.Title className="sr-only">Command palette</Dialog.Title>
          <input
            role="combobox"
            aria-label="Search projects, configs, secrets"
            aria-expanded="true"
            aria-controls="palette-list"
            aria-activedescendant={filtered.length ? `palette-opt-${active}` : undefined}
            autoFocus
            value={query}
            onChange={(e) => setQuery(e.target.value)}
            onKeyDown={onKeyDown}
            placeholder="Search projects, configs, secrets…"
            className="w-full bg-surface-3 px-4 py-3 text-[14px] text-ink outline-none placeholder:text-ink-faint"
          />
          <div id="palette-list" ref={listRef} role="listbox" className="max-h-[50vh] overflow-y-auto p-1.5">
            {filtered.length === 0 && (
              <p className="px-3 py-6 text-center text-[12.5px] text-ink-faint">No matches</p>
            )}
            {GROUP_ORDER.map((group) => {
              const rows = filtered.filter((it) => it.group === group)
              if (rows.length === 0) return null
              return (
                <div key={group} role="group" aria-label={group} className="mb-1">
                  <div className="px-3 pb-0.5 pt-2 text-[10px] font-bold uppercase tracking-[.12em] text-ink-faint">
                    {group}
                  </div>
                  {rows.map((it) => {
                    flatIndex += 1
                    const isActive = flatIndex === active
                    const idx = flatIndex
                    return (
                      <button
                        key={it.id}
                        id={`palette-opt-${idx}`}
                        type="button"
                        role="option"
                        aria-selected={isActive}
                        onMouseEnter={() => setActive(idx)}
                        onClick={() => commit(it)}
                        className={
                          'flex w-full items-center justify-between rounded px-3 py-2 text-left text-[13px] ' +
                          (isActive ? 'bg-nav-active text-ink' : 'text-ink-body hover:bg-surface-3')
                        }
                      >
                        <span className={it.group === 'Secrets' || it.group === 'Configs' ? 'font-mono text-[12.5px]' : ''}>
                          {it.label}
                        </span>
                        {it.sublabel && <span className="ml-3 shrink-0 text-[11.5px] text-ink-faint">{it.sublabel}</span>}
                      </button>
                    )
                  })}
                </div>
              )
            })}
          </div>
          <div className="flex gap-3 border-t border-line px-3 py-1.5 text-[10.5px] text-ink-faint">
            <span>↑↓ navigate</span><span>↵ open</span><span>esc close</span>
          </div>
        </Dialog.Content>
      </Dialog.Portal>
    </Dialog.Root>
  )
}
