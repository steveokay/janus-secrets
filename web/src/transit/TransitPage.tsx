import { FormEvent, ReactNode, useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Shield, Lock, Unlock, Plus } from 'lucide-react'
import { endpoints, TransitKey, TransitKeyType } from '../lib/endpoints'
import { apiErrorTitle } from '../lib/api'
import { useToast } from '../ui/Toast'
import { Pill } from '../ui/Pill'
import { KeyActions } from './KeyActions'
import { Playground } from './Playground'
import { EmptyState } from '../ui/EmptyState'
import { useTitle } from '../lib/title'
import { cn } from '../ui/cn'

const NAME_RE = /^[A-Za-z0-9_-]{1,64}$/

// Centered create-key modal. Mirrors the CreateForms.tsx Dialog + useSubmit
// pattern (local, presentational shell) but surfaces failures via
// apiErrorTitle so the server's curated 409 conflict message reaches the user.
function Dialog({ title, children }: { title: string; children: ReactNode }) {
  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-ink/30">
      <div className="w-80 rounded-card border border-line bg-card p-5 shadow-pop">
        <h2 className="mb-3 text-[15px] font-semibold tracking-tight text-ink">{title}</h2>
        {children}
      </div>
    </div>
  )
}

function CreateKeyForm({ onClose }: { onClose: () => void }) {
  const qc = useQueryClient()
  const toast = useToast()
  const [name, setName] = useState('')
  const [type, setType] = useState<TransitKeyType>('aes256-gcm')
  const [error, setError] = useState('')
  const valid = NAME_RE.test(name)

  const m = useMutation({
    mutationFn: () => endpoints.createTransitKey(name, type),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ['transit', 'keys'] })
      // Success confirmed by toast; failures stay inline (curated 409 conflict).
      toast({ title: `Created ${name}` })
      onClose()
    },
    onError: (e) => setError(apiErrorTitle(e)),
  })

  function submit(e: FormEvent) {
    e.preventDefault()
    setError('')
    if (!valid) return
    m.mutate()
  }

  return (
    <Dialog title="Create transit key">
      <form onSubmit={submit} className="flex flex-col gap-2.5">
        <label className="flex flex-col gap-1 text-[12px] font-semibold text-ink">
          Name
          <input
            aria-label="name"
            value={name}
            onChange={(e) => setName(e.target.value)}
            placeholder="e.g. app"
            className="rounded border border-line bg-card px-3 py-2 font-mono text-[12.5px] font-normal text-ink placeholder:text-faint"
          />
          <span className="text-[11px] font-normal text-faint">Letters, digits, dash and underscore; up to 64 characters.</span>
          {name !== '' && !valid && (
            <span className="text-[11px] font-normal text-danger">Invalid key name.</span>
          )}
        </label>
        <fieldset className="flex flex-col gap-1.5">
          <legend className="text-[12px] font-semibold text-ink">Type</legend>
          <label className="flex items-center gap-2 text-[12.5px] font-normal text-muted">
            <input
              type="radio"
              name="key-type"
              value="aes256-gcm"
              checked={type === 'aes256-gcm'}
              onChange={() => setType('aes256-gcm')}
              className="accent-brand"
            />
            <span className="font-mono">aes256-gcm</span>
            <span className="text-faint">encrypt / decrypt</span>
          </label>
          <label className="flex items-center gap-2 text-[12.5px] font-normal text-muted">
            <input
              type="radio"
              name="key-type"
              value="ed25519"
              checked={type === 'ed25519'}
              onChange={() => setType('ed25519')}
              className="accent-brand"
            />
            <span className="font-mono">ed25519</span>
            <span className="text-faint">sign / verify</span>
          </label>
        </fieldset>
        {error && <p role="alert" className="text-[12.5px] text-danger">{error}</p>}
        <div className="mt-1 flex justify-end gap-2">
          <button
            type="button"
            onClick={onClose}
            className="rounded border border-line bg-card px-3 py-1.5 text-[13px] font-semibold text-ink"
          >
            Cancel
          </button>
          <button
            type="submit"
            disabled={!valid || m.isPending}
            className="rounded bg-brand px-4 py-1.5 text-[13px] font-semibold text-white shadow-card disabled:opacity-50"
          >
            Create key
          </button>
        </div>
      </form>
    </Dialog>
  )
}

const typeTone = { 'aes256-gcm': 'info', ed25519: 'brand' } as const

function KeyRow({ k, selected, onSelect }: {
  k: TransitKey
  selected: boolean
  onSelect: () => void
}) {
  // Row is a plain container so the select affordance and the actions menu are
  // SIBLINGS — nesting the menu trigger inside the select <button> would be
  // invalid HTML and swallow clicks.
  return (
    <div
      data-key-row
      data-key-name={k.name}
      aria-current={selected ? 'true' : undefined}
      className={cn(
        'flex items-center gap-3 border-t border-line-soft px-3 py-2 first:border-t-0 hover:bg-line-soft',
        selected && 'bg-brand-soft hover:bg-brand-soft',
      )}
    >
      <button
        type="button"
        onClick={onSelect}
        className="flex min-w-0 flex-1 items-center gap-3 text-left"
      >
        <span className="min-w-0 flex-1 truncate font-mono text-[13px] text-ink">{k.name}</span>
        <Pill tone={typeTone[k.type]}>{k.type}</Pill>
        <Pill tone="muted">v{k.latest_version}</Pill>
        {k.min_decryption_version > 1 && (
          <span className="text-[11.5px] text-faint">min v{k.min_decryption_version}</span>
        )}
        {k.deletion_allowed ? (
          <Unlock size={14} strokeWidth={1.7} className="text-faint" aria-label="deletion allowed" />
        ) : (
          <Lock size={14} strokeWidth={1.7} className="text-faint" aria-label="deletion protected" />
        )}
      </button>
      <KeyActions keyMeta={k} />
    </div>
  )
}

export function TransitPage() {
  useTitle('Transit')
  const keys = useQuery({ queryKey: ['transit', 'keys'], queryFn: endpoints.listTransitKeys })
  const [creating, setCreating] = useState(false)
  const [selected, setSelected] = useState<string | null>(null)

  const rows = keys.data ?? []

  const newKeyButton = (
    <button
      type="button"
      onClick={() => setCreating(true)}
      className="flex items-center gap-1.5 rounded bg-brand px-3 py-1.5 text-[13px] font-semibold text-white shadow-card"
    >
      <Plus size={14} strokeWidth={1.7} /> New key
    </button>
  )

  return (
    <div>
      <div className="mb-4 flex items-start justify-between gap-3">
        <div>
          <h2 className="text-[17px] font-semibold tracking-tight text-ink">Transit</h2>
          <p className="text-[12.5px] text-faint">Named encryption &amp; signing keys for encrypt-as-a-service.</p>
        </div>
        {rows.length > 0 && newKeyButton}
      </div>

      {keys.isError ? (
        <p role="alert" className="text-[12.5px] text-danger">Couldn't load transit keys.</p>
      ) : keys.isLoading ? (
        <div className="flex flex-col gap-1.5" aria-hidden="true">
          {[0, 1, 2].map((i) => <div key={i} className="h-9 animate-pulse rounded bg-line-soft" />)}
        </div>
      ) : rows.length === 0 ? (
        <EmptyState
          icon={<Shield size={22} strokeWidth={1.7} />}
          title="No transit keys yet"
          hint="Create a named key to encrypt, decrypt, sign and verify without exposing key material."
          action={newKeyButton}
        />
      ) : (
        <div className="overflow-hidden rounded-card border border-line bg-card shadow-card">
          {rows.map((k) => (
            <KeyRow
              key={k.name}
              k={k}
              selected={selected === k.name}
              onSelect={() => setSelected(k.name)}
            />
          ))}
        </div>
      )}

      {/* Playground for the selected key. `key={selected}` remounts on switch,
          clearing all input/result state so no crypto output leaks across keys. */}
      {selected && (() => {
        const k = rows.find((x) => x.name === selected)
        return k ? <Playground key={selected} keyMeta={k} /> : null
      })()}

      {creating && <CreateKeyForm onClose={() => setCreating(false)} />}
    </div>
  )
}
