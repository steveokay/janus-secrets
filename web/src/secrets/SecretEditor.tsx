import { useEffect, useMemo, useState } from 'react'
import { useParams } from 'react-router-dom'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { endpoints } from '../lib/endpoints'
import { Buffer, emptyBuffer, setValue, removeKey, revert, addKey, summarize, toChanges, isDirty } from './dirty'
import { useTitle } from '../lib/title'
import { useToast } from '../ui/Toast'
import { EmptyState } from '../ui/EmptyState'
import { Sheet } from '../ui/Sheet'
import { SecretTable } from './SecretTable'
import { EditorToolbar } from './EditorToolbar'
import { DirtyBar } from './DirtyBar'
import { ReviewDiffDialog } from './ReviewDiffDialog'
import { VersionHistory } from './VersionHistory'

export function SecretEditor() {
  useTitle('Secrets')
  const { configId } = useParams()
  const cid = configId!
  const qc = useQueryClient()
  const toast = useToast()
  const masked = useQuery({ queryKey: ['config', cid, 'masked'], queryFn: () => endpoints.maskedSecrets(cid) })
  const raw = useQuery({ queryKey: ['config', cid, 'raw'], queryFn: () => endpoints.rawConfig(cid) })
  const [buffer, setBuffer] = useState<Buffer>(emptyBuffer())
  const [editing, setEditing] = useState<Record<string, boolean>>({})
  const [revealed, setRevealed] = useState<Record<string, string>>({})
  const [filter, setFilter] = useState('')
  const [showHistory, setShowHistory] = useState(false)
  const [, setImportOpen] = useState(false)
  const [reviewOpen, setReviewOpen] = useState(false)

  const original = raw.data?.secrets ?? {}
  // The config version (from the raw reveal) — not the max per-secret value
  // version, which diverges from it under two-level versioning.
  const version = raw.data?.version ?? 0
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
    onSuccess: () => {
      setBuffer(emptyBuffer())
      setEditing({})
      setReviewOpen(false)
      void qc.invalidateQueries({ queryKey: ['config', cid] })
    },
  })

  function discard() {
    setBuffer(emptyBuffer())
    setEditing({})
    setReviewOpen(false)
  }

  async function reveal(key: string) {
    const r = await endpoints.revealKey(cid, key)
    setRevealed((m) => ({ ...m, [key]: r.value }))
    return r.value
  }
  // Copying a secret is a read — reveal (audited) first if needed, then copy.
  async function copy(key: string) {
    const value = key in revealed ? revealed[key] : await reveal(key)
    try {
      await navigator.clipboard?.writeText(value)
      toast({ title: `Copied ${key}` })
    } catch {
      /* clipboard unavailable / denied — no-op */
    }
  }
  function edit(key: string) {
    setEditing((s) => ({ ...s, [key]: true }))
  }
  function changeValue(key: string, value: string) {
    setBuffer((b) => setValue(b, key, value))
  }
  function remove(key: string) {
    setBuffer((b) => removeKey(b, key))
  }
  function undo(key: string) {
    setBuffer((b) => revert(b, key))
    setEditing((s) => { const { [key]: _drop, ...rest } = s; return rest })
  }

  if (masked.isLoading || raw.isLoading) return <p>Loading…</p>
  if (masked.isError) return <p role="alert">Could not load secrets.</p>
  const maskedRows = masked.data ?? {}
  // Ordered key list: existing masked keys, then keys added only in the buffer.
  const addedKeys = Object.keys(buffer).filter((k) => !(k in maskedRows) && buffer[k].value !== null)
  const rows = [...Object.keys(maskedRows), ...addedKeys]

  return (
    <div>
      {save.isError && <p role="alert" className="mb-2 text-sm text-danger">Save failed.</p>}
      <EditorToolbar
        filter={filter}
        onFilter={setFilter}
        onImport={() => setImportOpen(true)}
        onHistory={() => setShowHistory(true)}
      />
      {rows.length === 0 ? (
        <EmptyState
          className="mt-10"
          title="No secrets yet"
          hint="Add your first key below — it's encrypted before it ever touches the database."
        />
      ) : (
        <SecretTable
          rows={rows}
          masked={maskedRows}
          buffer={buffer}
          original={original}
          editing={editing}
          revealed={revealed}
          filter={filter}
          onReveal={(key) => void reveal(key)}
          onCopy={(key) => void copy(key)}
          onEdit={edit}
          onChangeValue={changeValue}
          onRemove={remove}
          onRevert={undo}
        />
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
    </div>
  )
}

function AddKeyRow({ onAdd }: { onAdd: (key: string, value: string) => void }) {
  const [key, setKey] = useState('')
  const [value, setValue] = useState('')
  return (
    <div className="mt-3 flex gap-2">
      <input aria-label="new key" placeholder="NEW_KEY" value={key} onChange={(e) => setKey(e.target.value)} className="rounded border border-line bg-card px-2.5 py-1.5 font-mono text-[12.5px] text-ink" />
      <input aria-label="new value" placeholder="value" value={value} onChange={(e) => setValue(e.target.value)} className="rounded border border-line bg-card px-2.5 py-1.5 font-mono text-[12.5px] text-ink" />
      <button
        disabled={!key}
        onClick={() => { onAdd(key, value); setKey(''); setValue('') }}
        className="rounded border border-line bg-card px-3 text-[13px] font-semibold text-ink disabled:opacity-40"
      >
        ＋ Add key
      </button>
    </div>
  )
}
