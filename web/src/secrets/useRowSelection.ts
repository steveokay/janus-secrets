import { useCallback, useMemo, useState } from 'react'

export function useRowSelection() {
  const [sel, setSel] = useState<Set<string>>(() => new Set())

  const toggle = useCallback((key: string) => {
    setSel((s) => {
      const next = new Set(s)
      if (next.has(key)) next.delete(key)
      else next.add(key)
      return next
    })
  }, [])

  const clear = useCallback(() => setSel(new Set()), [])

  // Header toggle: if every given key is already selected, clear; else select all.
  const setAll = useCallback((keys: string[]) => {
    setSel((s) => {
      const allOn = keys.length > 0 && keys.every((k) => s.has(k))
      return allOn ? new Set() : new Set(keys)
    })
  }, [])

  // Drop any selected key that is no longer allowed (e.g. filtered out / saved).
  const prune = useCallback((allowed: string[]) => {
    const allow = new Set(allowed)
    setSel((s) => {
      let changed = false
      const next = new Set<string>()
      for (const k of s) {
        if (allow.has(k)) next.add(k)
        else changed = true
      }
      return changed ? next : s
    })
  }, [])

  const isSelected = useCallback((key: string) => sel.has(key), [sel])

  return useMemo(
    () => ({ selected: sel, count: sel.size, toggle, clear, setAll, prune, isSelected }),
    [sel, toggle, clear, setAll, prune, isSelected],
  )
}
