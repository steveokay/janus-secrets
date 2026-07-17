import { useEffect, useRef, useState } from 'react'

const FLASH_MS = 1200

// Transient "Copied!" state for copy-to-clipboard affordances. Callers keep
// their own copy logic (single value, audited reveal-then-copy, etc.) and
// just call `markCopied(id)` after a successful copy — this hook only tracks
// which id (default: a single implicit id) is in its brief "copied" flash
// window so the icon/label can swap back automatically. Never touches what
// gets copied or any reveal/audit behavior.
export function useCopyFeedback() {
  const [copiedId, setCopiedId] = useState<string | null>(null)
  const timer = useRef<ReturnType<typeof setTimeout> | undefined>(undefined)

  useEffect(() => () => { if (timer.current) clearTimeout(timer.current) }, [])

  function markCopied(id: string = 'default') {
    setCopiedId(id)
    if (timer.current) clearTimeout(timer.current)
    timer.current = setTimeout(() => setCopiedId(null), FLASH_MS)
  }

  function isCopied(id: string = 'default') {
    return copiedId === id
  }

  return { isCopied, markCopied }
}
