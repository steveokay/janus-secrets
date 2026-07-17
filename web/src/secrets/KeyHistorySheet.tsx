import { useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { Eye, Copy, Check } from 'lucide-react'
import { endpoints } from '../lib/endpoints'
import { Sheet } from '../ui/Sheet'
import { Pill } from '../ui/Pill'
import { Skeleton } from '../ui/Skeleton'
import { useToast } from '../ui/Toast'
import { useCopyFeedback } from '../ui/useCopyFeedback'
import { relativeTime } from '../lib/relativeTime'

// Inner body: keyed by `secretKey` from the parent so switching keys remounts
// it, dropping any revealed plaintext. Revealed values live ONLY in local state
// (never the TanStack Query cache) — the reveal call is imperative.
function KeyHistoryBody({ cid, secretKey }: { cid: string; secretKey: string }) {
  const toast = useToast()
  const history = useQuery({
    queryKey: ['key-history', cid, secretKey],
    queryFn: () => endpoints.keyHistory(cid, secretKey),
  })
  const [revealed, setRevealed] = useState<Record<number, string>>({})
  const copyFeedback = useCopyFeedback()

  async function reveal(version: number) {
    if (version in revealed) return
    try {
      const r = await endpoints.revealKeyVersion(cid, secretKey, version) // audited
      setRevealed((m) => ({ ...m, [version]: r.value }))
    } catch {
      toast({ title: 'Reveal failed', tone: 'danger' })
    }
  }
  async function copy(version: number) {
    const cached = revealed[version]
    const value = cached ?? (await endpoints.revealKeyVersion(cid, secretKey, version)).value
    if (cached === undefined) setRevealed((m) => ({ ...m, [version]: value }))
    try {
      await navigator.clipboard?.writeText(value)
      toast({ title: `Copied v${version}` })
      copyFeedback.markCopied(String(version))
    } catch {
      toast({ title: 'Copy failed', tone: 'danger' })
    }
  }

  if (history.isLoading) {
    return (
      <div aria-hidden className="flex flex-col gap-1.5">
        {[0, 1, 2].map((i) => <Skeleton key={i} className="h-12 w-full rounded-card" />)}
      </div>
    )
  }
  if (history.isError) return <p role="alert" className="text-[12.5px] text-danger">Couldn't load history.</p>
  const list = [...(history.data?.history ?? [])].sort((a, b) => b.value_version - a.value_version)
  if (list.length === 0) return <p className="text-[12.5px] text-ink-faint">No version history for this key.</p>

  return (
    <ul className="flex flex-col gap-1.5">
      {list.map((v) => (
        <li key={v.value_version} className="rounded-card border border-line-soft px-3 py-2">
          <div className="flex items-center gap-2">
            <Pill tone="brand">v{v.value_version}</Pill>
            <span className="flex-1 text-[11.5px] text-ink-faint">{relativeTime(v.created_at)}</span>
            {v.value_version in revealed ? (
              <button
                type="button"
                aria-label={copyFeedback.isCopied(String(v.value_version)) ? `v${v.value_version} copied` : `copy v${v.value_version}`}
                onClick={() => void copy(v.value_version)}
                className="inline-flex h-6 w-6 items-center justify-center rounded text-ink-faint hover:bg-surface-3 hover:text-brand-text"
              >
                {copyFeedback.isCopied(String(v.value_version))
                  ? <Check size={13} strokeWidth={1.8} className="text-success" />
                  : <Copy size={13} strokeWidth={1.8} />}
              </button>
            ) : (
              <button
                type="button"
                aria-label={`reveal v${v.value_version}`}
                onClick={() => void reveal(v.value_version)}
                className="inline-flex h-6 w-6 items-center justify-center rounded text-ink-faint hover:bg-surface-3 hover:text-brand-text"
              >
                <Eye size={13} strokeWidth={1.8} />
              </button>
            )}
          </div>
          {v.value_version in revealed && (
            <div className="mt-1.5 break-all rounded bg-surface-3 px-2.5 py-1.5 font-mono text-[12px] text-ink">
              {revealed[v.value_version]}
            </div>
          )}
        </li>
      ))}
    </ul>
  )
}

export function KeyHistorySheet({ cid, secretKey, open, onOpenChange }: {
  cid: string
  secretKey: string | null
  open: boolean
  onOpenChange: (open: boolean) => void
}) {
  return (
    <Sheet open={open} onOpenChange={onOpenChange} title={secretKey ? `History · ${secretKey}` : 'History'}>
      {open && secretKey ? <KeyHistoryBody key={secretKey} cid={cid} secretKey={secretKey} /> : null}
    </Sheet>
  )
}
