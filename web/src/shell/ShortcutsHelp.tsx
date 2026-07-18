import { Fragment, useEffect, useState } from 'react'
import { Modal } from '../ui/Modal'
import { isTypingTarget } from '../lib/isTypingTarget'

type Shortcut = { keys: string[]; label: string }
type Group = { title: string; shortcuts: Shortcut[] }

// Only lists shortcuts that actually exist elsewhere in the app (PaletteProvider,
// SecretEditor's save handler, useRowNav, TableSearch's Escape-to-clear). Keep
// this in sync if a shortcut is added/removed at its source.
const GROUPS: Group[] = [
  {
    title: 'Global',
    shortcuts: [
      { keys: ['⌘', 'K'], label: 'Open command palette' },
      { keys: ['?'], label: 'Show this shortcuts overlay' },
      { keys: ['Esc'], label: 'Close a dialog, palette, or search' },
    ],
  },
  {
    title: 'Secret editor',
    shortcuts: [
      { keys: ['⌘', 'S'], label: 'Save pending changes' },
      { keys: ['/'], label: 'Focus the key filter' },
      { keys: ['↑', '↓'], label: 'Move the active row (also J / K)' },
      { keys: ['Enter'], label: 'Reveal the active row (audited)' },
      { keys: ['E'], label: 'Edit the active row' },
      { keys: ['X'], label: 'Toggle the active row selection' },
      { keys: ['Delete'], label: 'Remove the active row' },
      { keys: ['Esc'], label: 'Cancel an in-progress edit' },
    ],
  },
]

function Kbd({ children }: { children: string }) {
  return (
    <kbd className="rounded-pill bg-surface-1 px-1.5 py-0.5 text-[10.5px] font-semibold text-ink-mute">
      {children}
    </kbd>
  )
}

export function ShortcutsHelp({ open, onClose }: { open: boolean; onClose: () => void }) {
  return (
    <Modal open={open} onClose={onClose} label="Keyboard shortcuts" className="w-[420px]">
      <h2 className="mb-3 text-[15px] font-semibold tracking-tight text-ink-hi">Keyboard shortcuts</h2>
      <div className="flex flex-col gap-4">
        {GROUPS.map((group) => (
          <div key={group.title}>
            <div className="mb-1.5 text-[10.5px] font-bold uppercase tracking-[.1em] text-ink-faint">
              {group.title}
            </div>
            <ul className="flex flex-col gap-1.5">
              {group.shortcuts.map((s) => (
                <li key={s.label} className="flex items-center justify-between gap-3 text-[12.5px]">
                  <span className="text-ink-body">{s.label}</span>
                  <span className="flex shrink-0 items-center gap-1">
                    {s.keys.map((k, i) => (
                      <Fragment key={k}>
                        {i > 0 && <span className="text-ink-faint">/</span>}
                        <Kbd>{k}</Kbd>
                      </Fragment>
                    ))}
                  </span>
                </li>
              ))}
            </ul>
          </div>
        ))}
      </div>
    </Modal>
  )
}

// Global `?` handler: opens the overlay unless focus is in a text field or a
// modifier is held (so it never fights Shift+/ input, e.g. typing a literal
// "?" into a field).
export function useShortcutsHelp() {
  const [open, setOpen] = useState(false)

  useEffect(() => {
    function onKey(e: KeyboardEvent) {
      if (e.key !== '?') return
      if (e.metaKey || e.ctrlKey || e.altKey) return
      if (isTypingTarget(e.target)) return
      e.preventDefault()
      setOpen((o) => !o)
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [])

  return { open, close: () => setOpen(false) }
}
