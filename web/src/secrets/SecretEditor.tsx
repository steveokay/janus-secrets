import { useEffect, useMemo, useState } from 'react'
import { useParams } from 'react-router-dom'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { endpoints, MaskedSecret } from '../lib/endpoints'
import { Buffer, emptyBuffer, setValue, removeKey, revert, addKey, summarize, toChanges, isDirty } from './dirty'
import { useTitle } from '../lib/title'
import { EmptyState } from '../ui/EmptyState'

const badge: Record<MaskedSecret['origin'], string> = {
  own: 'bg-success-soft text-success',
  inherited: 'bg-line-soft text-muted',
  overridden: 'bg-brand-soft text-brand-deep',
}

export function SecretEditor() {
  useTitle('Secrets')
  const { configId } = useParams()
  const cid = configId!
  const qc = useQueryClient()
  const masked = useQuery({ queryKey: ['config', cid, 'masked'], queryFn: () => endpoints.maskedSecrets(cid) })
  const raw = useQuery({ queryKey: ['config', cid, 'raw'], queryFn: () => endpoints.rawConfig(cid) })
  const [buffer, setBuffer] = useState<Buffer>(emptyBuffer())
  const [editing, setEditing] = useState<Record<string, boolean>>({})
  const [revealed, setRevealed] = useState<Record<string, string>>({})

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
      void qc.invalidateQueries({ queryKey: ['config', cid] })
    },
  })

  async function reveal(key: string) {
    const r = await endpoints.revealKey(cid, key)
    setRevealed((m) => ({ ...m, [key]: r.value }))
  }
  function valueOf(key: string): string {
    return key in buffer ? (buffer[key].value ?? '') : (original[key] ?? '')
  }

  if (masked.isLoading || raw.isLoading) return <p>Loading…</p>
  if (masked.isError) return <p role="alert">Could not load secrets.</p>
  const maskedRows = masked.data ?? {}
  const rows = Object.entries(maskedRows)
  // Keys added in the buffer that don't exist yet in the config — rendered as
  // pending rows so a new key is visible and cancellable before save.
  const addedKeys = Object.keys(buffer).filter((k) => !(k in maskedRows) && buffer[k].value !== null)

  return (
    <div>
      <div className="mb-3 flex items-center justify-between">
        <span className="text-sm text-muted">
          {dirty ? `Pending: +${summary.added} added · ${summary.changed} changed · ${summary.removed} removed` : `${rows.length} keys`}
        </span>
        <button
          onClick={() => save.mutate()}
          disabled={!dirty || save.isPending}
          className="rounded bg-brand px-3 py-1.5 text-[13px] font-semibold text-white shadow-card disabled:opacity-40"
        >
          {save.isPending ? 'Saving…' : `Save as v${version + 1}`}
        </button>
      </div>
      {save.isError && <p role="alert" className="mb-2 text-sm text-danger">Save failed.</p>}
      {rows.length === 0 && addedKeys.length === 0 ? (
        <EmptyState
          className="mt-10"
          title="No secrets yet"
          hint="Add your first key below — it's encrypted before it ever touches the database."
        />
      ) : (
      <table className="w-full overflow-hidden rounded-card border border-line bg-card text-sm shadow-card">
        <thead><tr className="text-left text-[10.5px] uppercase tracking-[.1em] text-faint"><th>KEY</th><th>VALUE</th><th>ORIGIN</th><th>v</th></tr></thead>
        <tbody>
          {rows.map(([key, meta]) => {
            const removedRow = key in buffer && buffer[key].value === null
            return (
              <tr key={key} className={`border-t border-line-soft ${removedRow ? 'line-through opacity-50' : ''}`}>
                <td className="py-1 font-mono">{key}</td>
                <td className="py-1 font-mono">
                  {editing[key] ? (
                    <input
                      aria-label={`value for ${key}`}
                      value={valueOf(key)}
                      onChange={(e) => setBuffer((b) => setValue(b, key, e.target.value))}
                      className="w-full rounded border border-line px-2.5 py-1 font-mono text-[12.5px]"
                    />
                  ) : (
                    <>
                      {key in revealed ? revealed[key] : '•••••••'}
                      {meta.origin !== 'inherited' && (
                        <button aria-label={`edit ${key}`} onClick={() => setEditing((s) => ({ ...s, [key]: true }))} className="ml-2 text-faint hover:text-brand-deep">✎</button>
                      )}
                      {!(key in revealed) && (
                        <button aria-label={`reveal ${key}`} onClick={() => void reveal(key)} className="ml-1 text-faint hover:text-brand-deep">👁</button>
                      )}
                      {meta.origin !== 'inherited' && !removedRow && (
                        <button aria-label={`remove ${key}`} onClick={() => setBuffer((b) => removeKey(b, key))} className="ml-1 text-danger/70 hover:text-danger">✕</button>
                      )}
                    </>
                  )}
                </td>
                <td className="py-1"><span className={`rounded px-1.5 ${badge[meta.origin]}`}>{meta.origin}</span></td>
                <td className="py-1 text-faint">{meta.value_version}</td>
              </tr>
            )
          })}
          {addedKeys.map((key) => (
            <tr key={key} className="border-t border-line-soft bg-success-soft/50">
              <td className="py-1 font-mono">{key} <span className="text-xs text-success">(new)</span></td>
              <td className="py-1 font-mono">
                <input
                  aria-label={`value for ${key}`}
                  value={buffer[key].value ?? ''}
                  onChange={(e) => setBuffer((b) => setValue(b, key, e.target.value))}
                  className="w-full rounded border border-line px-2.5 py-1 font-mono text-[12.5px]"
                />
              </td>
              <td className="py-1"><span className={`rounded px-1.5 ${badge.own}`}>own</span></td>
              <td className="py-1">
                <button aria-label={`remove ${key}`} onClick={() => setBuffer((b) => revert(b, key))} className="text-danger/70 hover:text-danger">✕</button>
              </td>
            </tr>
          ))}
        </tbody>
      </table>
      )}
      <AddKeyRow onAdd={(k, v) => setBuffer((b) => addKey(b, k, v))} />
    </div>
  )
}

function AddKeyRow({ onAdd }: { onAdd: (key: string, value: string) => void }) {
  const [key, setKey] = useState('')
  const [value, setValue] = useState('')
  return (
    <div className="mt-3 flex gap-2">
      <input aria-label="new key" placeholder="NEW_KEY" value={key} onChange={(e) => setKey(e.target.value)} className="rounded border border-line px-2.5 py-1.5 font-mono text-[12.5px]" />
      <input aria-label="new value" placeholder="value" value={value} onChange={(e) => setValue(e.target.value)} className="rounded border border-line px-2.5 py-1.5 font-mono text-[12.5px]" />
      <button
        disabled={!key}
        onClick={() => { onAdd(key, value); setKey(''); setValue('') }}
        className="rounded border border-line bg-card px-3 text-[13px] font-semibold disabled:opacity-40"
      >
        ＋ Add key
      </button>
    </div>
  )
}
