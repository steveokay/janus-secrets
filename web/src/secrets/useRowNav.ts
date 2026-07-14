import { useEffect, useState } from 'react'

type Cbs = {
  visible: string[]
  onEdit: (key: string) => void
  onReveal: (key: string) => void
  onRemove: (key: string) => void
  onToggleSelect: (key: string) => void
  onFocusFilter: () => void
}

function isTypingTarget(t: EventTarget | null): boolean {
  const el = t as HTMLElement | null
  if (!el || !el.tagName) return false
  const tag = el.tagName
  return tag === 'INPUT' || tag === 'TEXTAREA' || (el as HTMLElement).isContentEditable === true
}

// Active-row keyboard navigation. Installs a window keydown listener that is
// inert while a text field is focused (so value editing / filter typing is
// normal) and coexists with the global Cmd/Ctrl-S save handler.
export function useRowNav({ visible, onEdit, onReveal, onRemove, onToggleSelect, onFocusFilter }: Cbs) {
  const [active, setActive] = useState<string | null>(null)

  // Reset active if it drops out of the visible set (e.g. filter change).
  useEffect(() => {
    if (active !== null && !visible.includes(active)) setActive(null)
  }, [visible, active])

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (isTypingTarget(e.target)) return
      if (e.metaKey || e.ctrlKey || e.altKey) return // leave Cmd/Ctrl-S etc alone
      const idx = active === null ? -1 : visible.indexOf(active)
      const move = (delta: number) => {
        if (visible.length === 0) return
        const next = idx < 0 ? (delta > 0 ? 0 : visible.length - 1)
                             : Math.min(visible.length - 1, Math.max(0, idx + delta))
        setActive(visible[next])
      }
      switch (e.key) {
        case 'ArrowDown': case 'j': e.preventDefault(); move(1); break
        case 'ArrowUp':   case 'k': e.preventDefault(); move(-1); break
        case '/': e.preventDefault(); onFocusFilter(); break
        case 'Escape': setActive(null); break
        case 'x': if (active) { e.preventDefault(); onToggleSelect(active) } break
        case 'e': if (active) { e.preventDefault(); onEdit(active) } break
        case 'Enter': if (active) { e.preventDefault(); onReveal(active) } break
        case 'Delete': case 'Backspace': if (active) { e.preventDefault(); onRemove(active) } break
      }
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [visible, active, onEdit, onReveal, onRemove, onToggleSelect, onFocusFilter])

  return { active, setActive }
}
