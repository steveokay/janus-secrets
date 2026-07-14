import { useEffect, useMemo, useRef, useState } from 'react'
import { useParams } from 'react-router-dom'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { endpoints } from '../lib/endpoints'
import type { VersionMeta } from '../lib/endpoints'
import { errorMessage } from '../lib/api'
import { relativeTime } from '../lib/relativeTime'
import { Buffer, emptyBuffer, setValue, removeKey, revert, addKey, summarize, toChanges, isDirty } from './dirty'
import { useTitle } from '../lib/title'
import { useToast } from '../ui/Toast'
import { Button } from '../ui/Button'
import { EmptyState } from '../ui/EmptyState'
import { Sheet } from '../ui/Sheet'
import { SecretTable } from './SecretTable'
import { SelectionBar } from './SelectionBar'
import { useRowSelection } from '../lib/useRowSelection'
import { sortRows } from './sortRows'
import type { SortKey, SortState } from './sortRows'
import { rowState } from './rowState'
import { EditorToolbar } from './EditorToolbar'
import { DirtyBar } from './DirtyBar'
import { ReviewDiffDialog } from './ReviewDiffDialog'
import { ImportEnvDialog } from './ImportEnvDialog'
import { VersionHistory } from './VersionHistory'
import { Skeleton } from '../ui/Skeleton'
import { toEnvText } from './exportEnv'
import { useRowNav } from './useRowNav'
import { ConfirmDialog } from '../ui/ConfirmDialog'

export function SecretEditor() {
  useTitle('Secrets')
  const { configId } = useParams()
  const cid = configId!
  const qc = useQueryClient()
  const toast = useToast()
  const masked = useQuery({ queryKey: ['config', cid, 'masked'], queryFn: () => endpoints.maskedSecrets(cid) })
  const versions = useQuery({ queryKey: ['config', cid, 'versions'], queryFn: () => endpoints.listVersions(cid) })
  const [buffer, setBuffer] = useState<Buffer>(emptyBuffer())
  const [editing, setEditing] = useState<Record<string, boolean>>({})
  // Viewing reveal — re-maskable, RAW. Ephemeral component state only, never cached.
  const [revealed, setRevealed] = useState<Record<string, string>>({})
  // Edit originals — persist while a key stays dirty (across auto-re-mask), RAW.
  const [original, setOriginal] = useState<Record<string, string>>({})
  const [filter, setFilter] = useState('')
  const [changedOnly, setChangedOnly] = useState(false)
  const [showHistory, setShowHistory] = useState(false)
  const [importOpen, setImportOpen] = useState(false)
  const [reviewOpen, setReviewOpen] = useState(false)
  const [sort, setSort] = useState<SortState>(null)
  const [confirmDownload, setConfirmDownload] = useState(false)
  const filterRef = useRef<HTMLInputElement>(null)
  const selection = useRowSelection()
  function cycleSort(key: SortKey) {
    setSort((s) => {
      if (!s || s.key !== key) return { key, dir: 'asc' }
      if (s.dir === 'asc') return { key, dir: 'desc' }
      return null
    })
  }

  // The config version from the value-free versions list — no mount reveal.
  // Robust to ordering: take the max present, 0 for a config with no versions yet.
  const version = Math.max(0, ...(versions.data ?? []).map((v) => v.version))
  const summary = useMemo(() => summarize(buffer, original), [buffer, original])
  const dirty = isDirty(buffer, original)

  useEffect(() => {
    if (!dirty) return
    const h = (e: BeforeUnloadEvent) => { e.preventDefault(); e.returnValue = '' }
    window.addEventListener('beforeunload', h)
    return () => window.removeEventListener('beforeunload', h)
  }, [dirty])

  const save = useMutation({
    mutationFn: () => endpoints.saveSecrets(cid, toChanges(buffer, original), ''),
    onSuccess: (res) => {
      setBuffer(emptyBuffer())
      setEditing({})
      setRevealed({}) // saved values changed server-side — drop stale plaintext
      setOriginal({})
      setReviewOpen(false)
      void qc.invalidateQueries({ queryKey: ['config', cid] })
      toast({ title: `Saved as v${res.version}` })
    },
    // Danger toast surfaces the curated failure (e.g. 409 version conflict) —
    // never a secret value. Replaces the previous inline "Save failed." banner
    // so there is a single, transient failure surface.
    onError: (e) => toast({ title: errorMessage(e, 'Save failed.'), tone: 'danger' }),
  })

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === 's') {
        if (!dirty) return
        e.preventDefault()
        if (!save.isPending) save.mutate()
      }
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [dirty, save])

  function discard() {
    setBuffer(emptyBuffer())
    setEditing({})
    setOriginal({})
    setReviewOpen(false)
  }
  // Stage pasted .env pairs into the buffer (existing keys → edits, new → adds).
  function applyImport(pairs: Record<string, string>) {
    setBuffer((b) => Object.entries(pairs).reduce((acc, [k, v]) => setValue(acc, k, v), b))
    const n = Object.keys(pairs).length
    if (n > 0) toast({ title: `Imported ${n} key${n === 1 ? '' : 's'}` })
  }

  // Viewing reveal — RAW, imperative (NOT useQuery/useMutation) so plaintext
  // lands ONLY in `revealed` component state, never the TanStack Query cache.
  async function reveal(key: string): Promise<string> {
    if (key in revealed) return revealed[key]
    const r = await endpoints.revealKeyRaw(cid, key)
    setRevealed((m) => ({ ...m, [key]: r.value }))
    return r.value
  }
  const anyRevealed = Object.keys(revealed).length > 0
  // Bulk reveal — one audited RAW reveal, into ephemeral `revealed` only.
  async function revealAll() {
    const r = await endpoints.rawConfig(cid)
    setRevealed(r.secrets)
  }
  function hideAll() {
    setRevealed({})
  }

  // Security: auto re-mask revealed plaintext on window blur or after 60s of
  // inactivity while anything is revealed.
  useEffect(() => {
    if (!anyRevealed) return
    const remask = () => setRevealed({})
    let idle = window.setTimeout(remask, 60_000)
    const bump = () => { window.clearTimeout(idle); idle = window.setTimeout(remask, 60_000) }
    window.addEventListener('blur', remask)
    window.addEventListener('keydown', bump)
    window.addEventListener('mousemove', bump)
    return () => {
      window.clearTimeout(idle)
      window.removeEventListener('blur', remask)
      window.removeEventListener('keydown', bump)
      window.removeEventListener('mousemove', bump)
    }
  }, [anyRevealed])
  // Copying a secret is a read — reveal (audited) first if needed, then copy.
  async function copy(key: string) {
    const value = key in revealed ? revealed[key] : await reveal(key)
    try {
      await navigator.clipboard?.writeText(value)
      toast({ title: `Copied ${key}` })
    } catch {
      toast({ title: 'Copy failed', tone: 'danger' })
    }
  }
  // Editing a masked existing key needs its raw original (for prefill + diff)
  // — fetch on demand (audited), reusing an already-revealed value if present.
  // Added (new) keys have no server value, so they skip the fetch.
  async function edit(key: string) {
    if (!(key in original)) {
      const existing = key in (masked.data ?? {})
      if (existing) {
        const v = key in revealed ? revealed[key] : (await endpoints.revealKeyRaw(cid, key)).value
        setOriginal((o) => ({ ...o, [key]: v }))
      }
    }
    setEditing((s) => ({ ...s, [key]: true }))
  }
  function changeValue(key: string, value: string) {
    setBuffer((b) => setValue(b, key, value))
  }
  // Removing an existing key is a delete, not a reveal — no fetch needed. But
  // dirty.ts's `effective()` only counts a buffered delete when the key is
  // known in `original` (so a discarded pending-add never masquerades as a
  // real delete). Record a value-free existence marker so the delete is
  // diffed correctly without an unnecessary audited reveal. Don't clobber a
  // real fetched original (e.g. remove-after-edit).
  function remove(key: string) {
    setOriginal((o) => (key in o ? o : { ...o, [key]: '' }))
    setBuffer((b) => removeKey(b, key))
  }
  function undo(key: string) {
    setBuffer((b) => revert(b, key))
    setEditing((s) => { const { [key]: _drop, ...rest } = s; return rest })
    setOriginal((o) => { const { [key]: _drop, ...rest } = o; return rest })
  }

  const maskedRows = masked.data ?? {}
  // Ordered key list: existing masked keys, then keys added only in the buffer.
  const addedKeys = Object.keys(buffer).filter((k) => !(k in maskedRows) && buffer[k].value !== null)
  const rows = [...Object.keys(maskedRows), ...addedKeys]
  // Visible pipeline: sort → substring filter → changed-only filter.
  const ordered = sortRows(rows, maskedRows, sort)
  const q = filter.trim().toLowerCase()
  const filtered = q ? ordered.filter((k) => k.toLowerCase().includes(q)) : ordered
  const visible = changedOnly
    ? filtered.filter((k) => rowState(k, maskedRows, buffer, original).change !== null)
    : filtered

  // Prune selection to what's currently visible (filtered out / saved keys drop).
  // `prune` returns the same Set ref when nothing changes, so this won't loop.
  useEffect(() => { selection.prune(visible) }, [visible, selection])

  function bulkDelete() {
    const keys = [...selection.selected]
    let deleted = 0, skipped = 0
    keys.forEach((key) => {
      const st = rowState(key, maskedRows, buffer, original)
      if (st.change === 'added') { undo(key); deleted++ }
      else if (st.existing && st.origin !== 'inherited') { remove(key); deleted++ }
      else skipped++
    })
    selection.clear()
    toast({ title: `Deleted ${deleted}${skipped ? ` · skipped ${skipped} inherited` : ''}` })
  }

  // Bulk reveal — one audited per-key RAW reveal each (NOT rawConfig), into the
  // ephemeral `revealed` map. Skips added/new keys (no server value yet).
  async function bulkReveal(keys: string[]) {
    for (const key of keys) {
      const st = rowState(key, maskedRows, buffer, original)
      if (st.existing) await reveal(key)
    }
  }
  // Reveal selected existing keys (audited, per-key) into a LOCAL array — never
  // stored in state or the query cache. Reuses an already-revealed value.
  async function revealPairs(keys: string[]): Promise<Array<[string, string]>> {
    const out: Array<[string, string]> = []
    for (const key of keys) {
      const st = rowState(key, maskedRows, buffer, original)
      if (!st.existing) continue
      const value = key in revealed ? revealed[key] : await reveal(key)
      out.push([key, value])
    }
    return out
  }
  async function bulkCopy(keys: string[]) {
    try {
      const pairs = await revealPairs(keys) // local only
      if (pairs.length === 0) { toast({ title: 'Nothing to copy — selected keys have no stored value' }); return }
      await navigator.clipboard?.writeText(toEnvText(pairs))
      toast({ title: `Copied ${pairs.length} key${pairs.length === 1 ? '' : 's'} as .env` })
    } catch {
      toast({ title: 'Copy failed', tone: 'danger' })
    }
  }
  async function bulkDownload(keys: string[]) {
    try {
      const pairs = await revealPairs(keys) // local only
      if (pairs.length === 0) { toast({ title: 'Nothing to download — selected keys have no stored value' }); return }
      const text = toEnvText(pairs) // local only
      const blob = new Blob([text], { type: 'text/plain' })
      const url = URL.createObjectURL(blob)
      const a = document.createElement('a')
      a.href = url; a.download = 'secrets.env'
      document.body.appendChild(a); a.click(); a.remove()
      URL.revokeObjectURL(url) // do not cache the plaintext blob
      toast({ title: `Downloaded ${pairs.length} key${pairs.length === 1 ? '' : 's'}` })
    } catch {
      toast({ title: 'Download failed', tone: 'danger' })
    }
  }

  const nav = useRowNav({
    visible,
    onEdit: (k) => void edit(k),
    onReveal: (k) => { if (rowState(k, maskedRows, buffer, original).existing) void reveal(k) },
    onRemove: (k) => remove(k),
    onToggleSelect: (k) => selection.toggle(k),
    onFocusFilter: () => filterRef.current?.focus(),
  })

  if (masked.isLoading || versions.isLoading)
    return (
      <div aria-hidden className="flex flex-col gap-2">
        <Skeleton className="h-9 w-full" />
        {[0, 1, 2, 3].map((i) => <Skeleton key={i} className="h-11 w-full" />)}
      </div>
    )
  if (masked.isError) return <p role="alert">Could not load secrets.</p>
  // Latest config version metadata — the API returns versions oldest-first
  // (store ListVersions ORDER BY version ASC), so take the max, not [0].
  const latest = (versions.data ?? []).reduce<VersionMeta | null>(
    (a, b) => (!a || b.version > a.version ? b : a),
    null,
  )

  return (
    <div>
      {latest && (
        <p className="mb-2 text-[11px] text-ink-faint">
          v{latest.version} · {rows.length} key{rows.length === 1 ? '' : 's'} · updated {relativeTime(latest.created_at)} by {latest.created_by}
        </p>
      )}
      <EditorToolbar
        filter={filter}
        onFilter={setFilter}
        filterRef={filterRef}
        onImport={() => setImportOpen(true)}
        onHistory={() => setShowHistory(true)}
        anyRevealed={anyRevealed}
        onToggleRevealAll={() => (anyRevealed ? hideAll() : void revealAll())}
        changedOnly={changedOnly}
        onChangedOnly={setChangedOnly}
      />
      {rows.length === 0 ? (
        <EmptyState
          className="mt-10"
          title="No secrets yet"
          hint="Add your first key below — it's encrypted before it ever touches the database."
          action={
            <Button
              variant="secondary"
              size="sm"
              onClick={() => {
                const el = document.getElementById('add-key-input')
                el?.scrollIntoView?.({ block: 'center' })
                el?.focus()
              }}
            >
              Add secret
            </Button>
          }
        />
      ) : visible.length === 0 ? (
        <EmptyState
          className="mt-8"
          title={changedOnly && !filter ? 'No changed keys' : `No keys match “${filter}”`}
          hint={changedOnly ? 'Adjust the filter or clear ‘Changed only’.' : 'Adjust the filter to find keys.'}
        />
      ) : (
        <>
        {selection.count > 0 && (
          <SelectionBar
            count={selection.count}
            onReveal={() => void bulkReveal([...selection.selected])}
            onCopy={() => void bulkCopy([...selection.selected])}
            onDownload={() => setConfirmDownload(true)}
            onDelete={bulkDelete}
            onClear={selection.clear}
          />
        )}
        <SecretTable
          rows={visible}
          masked={maskedRows}
          buffer={buffer}
          original={original}
          editing={editing}
          revealed={revealed}
          sort={sort}
          onSort={cycleSort}
          selected={selection.selected}
          onToggleSelect={selection.toggle}
          onSelectAll={selection.setAll}
          active={nav.active}
          onReveal={(key) => void reveal(key)}
          onCopy={(key) => void copy(key)}
          onEdit={(key) => void edit(key)}
          onChangeValue={changeValue}
          onRemove={remove}
          onRevert={undo}
        />
        </>
      )}
      {dirty && (
        <DirtyBar
          summary={summary}
          version={version}
          saving={save.isPending}
          onReview={() => setReviewOpen(true)}
          onDiscard={discard}
          onSave={() => save.mutate()}
        />
      )}
      <AddKeyRow onAdd={(k, v) => setBuffer((b) => addKey(b, k, v))} />
      <Sheet open={showHistory} onOpenChange={setShowHistory} title="Version history">
        <VersionHistory cid={cid} dirty={dirty} />
      </Sheet>
      <ReviewDiffDialog
        open={reviewOpen}
        onClose={() => setReviewOpen(false)}
        buffer={buffer}
        masked={maskedRows}
        original={original}
        version={version}
        saving={save.isPending}
        onSave={() => save.mutate()}
      />
      <ImportEnvDialog open={importOpen} onClose={() => setImportOpen(false)} onApply={applyImport} masked={maskedRows} />
      <ConfirmDialog
        open={confirmDownload}
        onOpenChange={setConfirmDownload}
        title="Download secrets as .env?"
        body={`This writes ${selection.count} secret value${selection.count === 1 ? '' : 's'} in plaintext to a file on your device.`}
        confirmLabel="Download"
        tone="danger"
        onConfirm={() => { const keys = [...selection.selected]; setConfirmDownload(false); void bulkDownload(keys) }}
      />
    </div>
  )
}

function AddKeyRow({ onAdd }: { onAdd: (key: string, value: string) => void }) {
  const [key, setKey] = useState('')
  const [value, setValue] = useState('')
  return (
    <div className="mt-3 flex gap-2">
      <input id="add-key-input" aria-label="new key" placeholder="NEW_KEY" value={key} onChange={(e) => setKey(e.target.value)} className="rounded border border-line bg-surface-3 px-2.5 py-1.5 font-mono text-[12.5px] text-ink focus:border-brand-line focus:shadow-glow-soft" />
      <input aria-label="new value" placeholder="value" value={value} onChange={(e) => setValue(e.target.value)} className="rounded border border-line bg-surface-3 px-2.5 py-1.5 font-mono text-[12.5px] text-ink focus:border-brand-line focus:shadow-glow-soft" />
      <Button
        variant="secondary"
        size="sm"
        disabled={!key}
        onClick={() => { onAdd(key, value); setKey(''); setValue('') }}
      >
        ＋ Add key
      </Button>
    </div>
  )
}
