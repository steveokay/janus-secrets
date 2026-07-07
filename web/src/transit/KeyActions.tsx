import { FormEvent, ReactNode, useState } from 'react'
import { useMutation, useQueryClient } from '@tanstack/react-query'
import * as Menu from '@radix-ui/react-dropdown-menu'
import { MoreHorizontal } from 'lucide-react'
import { endpoints, TransitKey } from '../lib/endpoints'
import { apiErrorTitle } from '../lib/api'
import { ConfirmDialog } from '../ui/ConfirmDialog'
import { useToast } from '../ui/Toast'

const item =
  'flex w-full cursor-default select-none items-center rounded px-2.5 py-1.5 text-[13px] text-ink outline-none data-[highlighted]:bg-brand-soft data-[highlighted]:text-brand-text'

// Local centered modal shell — mirrors the TransitPage create-key Dialog so the
// management dialogs share the same overlay/card chrome (token classes only).
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

function ConfigureDialog({ keyMeta, onClose, onError }: {
  keyMeta: TransitKey
  onClose: () => void
  onError: (msg: string) => void
}) {
  const qc = useQueryClient()
  const toast = useToast()
  const [minVer, setMinVer] = useState(String(keyMeta.min_decryption_version))
  const [deletionAllowed, setDeletionAllowed] = useState(keyMeta.deletion_allowed)

  const m = useMutation({
    mutationFn: () => endpoints.configTransitKey(keyMeta.name, {
      min_decryption_version: Number(minVer),
      deletion_allowed: deletionAllowed,
    }),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ['transit', 'keys'] })
      toast({ title: `Configured ${keyMeta.name}` })
      onClose()
    },
    onError: (e) => onError(apiErrorTitle(e)),
  })

  function submit(e: FormEvent) {
    e.preventDefault()
    m.mutate()
  }

  return (
    <Dialog title={`Configure ${keyMeta.name}`}>
      <form onSubmit={submit} className="flex flex-col gap-2.5">
        <label className="flex flex-col gap-1 text-[12px] font-semibold text-ink">
          Min decryption version
          <input
            aria-label="min decryption version"
            type="number"
            min={1}
            max={keyMeta.latest_version}
            value={minVer}
            onChange={(e) => setMinVer(e.target.value)}
            className="rounded border border-line bg-card px-3 py-2 font-mono text-[12.5px] font-normal text-ink"
          />
          <span className="text-[11px] font-normal text-faint">
            Between 1 and {keyMeta.latest_version} (the latest version).
          </span>
        </label>
        <label className="flex items-center gap-2 text-[12.5px] font-normal text-muted">
          <input
            type="checkbox"
            checked={deletionAllowed}
            onChange={(e) => setDeletionAllowed(e.target.checked)}
            className="accent-brand"
          />
          Allow deletion
        </label>
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
            disabled={m.isPending}
            className="rounded bg-brand px-4 py-1.5 text-[13px] font-semibold text-white shadow-card disabled:opacity-50"
          >
            Save
          </button>
        </div>
      </form>
    </Dialog>
  )
}

function TrimDialog({ keyMeta, onClose, onError }: {
  keyMeta: TransitKey
  onClose: () => void
  onError: (msg: string) => void
}) {
  const qc = useQueryClient()
  const toast = useToast()
  const [minAvail, setMinAvail] = useState(String(keyMeta.min_decryption_version))

  const m = useMutation({
    mutationFn: () => endpoints.trimTransitKey(keyMeta.name, Number(minAvail)),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ['transit', 'keys'] })
      toast({ title: `Trimmed ${keyMeta.name}` })
      onClose()
    },
    onError: (e) => onError(apiErrorTitle(e)),
  })

  function submit(e: FormEvent) {
    e.preventDefault()
    m.mutate()
  }

  return (
    <Dialog title={`Trim ${keyMeta.name}`}>
      <form onSubmit={submit} className="flex flex-col gap-2.5">
        <label className="flex flex-col gap-1 text-[12px] font-semibold text-ink">
          Min available version
          <input
            aria-label="min available version"
            type="number"
            min={1}
            max={keyMeta.min_decryption_version}
            value={minAvail}
            onChange={(e) => setMinAvail(e.target.value)}
            className="rounded border border-line bg-card px-3 py-2 font-mono text-[12.5px] font-normal text-ink"
          />
          <span className="text-[11px] font-normal text-faint">
            Older versions are permanently removed; must be &le; min decryption version (v{keyMeta.min_decryption_version}).
          </span>
        </label>
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
            disabled={m.isPending}
            className="rounded bg-brand px-4 py-1.5 text-[13px] font-semibold text-white shadow-card disabled:opacity-50"
          >
            Apply
          </button>
        </div>
      </form>
    </Dialog>
  )
}

type OpenDialog = 'none' | 'configure' | 'trim' | 'delete'

export function KeyActions({ keyMeta }: { keyMeta: TransitKey }) {
  const qc = useQueryClient()
  const toast = useToast()
  const [dialog, setDialog] = useState<OpenDialog>('none')
  // Inline surface for management errors. The app wraps TransitPage in a
  // ToastProvider, but errors are ALSO mirrored here so the server's curated
  // 409 message (e.g. "deletion not allowed for this key") stays on screen.
  const [error, setError] = useState('')

  const rotate = useMutation({
    mutationFn: () => endpoints.rotateTransitKey(keyMeta.name),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ['transit', 'keys'] })
      toast({ title: `Rotated ${keyMeta.name}` })
    },
    onError: (e) => setError(apiErrorTitle(e)),
  })

  const del = useMutation({
    mutationFn: () => endpoints.deleteTransitKey(keyMeta.name),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ['transit', 'keys'] })
      toast({ title: `Deleted ${keyMeta.name}` })
      setDialog('none')
    },
    onError: (e) => {
      setError(apiErrorTitle(e))
      setDialog('none')
    },
  })

  return (
    <>
      <Menu.Root>
        <Menu.Trigger
          aria-label={`actions for ${keyMeta.name}`}
          className="flex h-7 w-7 items-center justify-center rounded border border-line bg-card text-muted outline-none hover:bg-line-soft data-[state=open]:bg-line-soft"
        >
          <MoreHorizontal size={16} strokeWidth={1.7} />
        </Menu.Trigger>
        <Menu.Portal>
          <Menu.Content
            align="end"
            sideOffset={6}
            className="min-w-[170px] rounded-card border border-line bg-card p-1.5 shadow-pop"
          >
            <Menu.Item className={item} onSelect={() => { setError(''); rotate.mutate() }}>
              Rotate
            </Menu.Item>
            <Menu.Item className={item} onSelect={() => { setError(''); setDialog('configure') }}>
              Configure…
            </Menu.Item>
            <Menu.Item className={item} onSelect={() => { setError(''); setDialog('trim') }}>
              Trim…
            </Menu.Item>
            <Menu.Separator className="my-1 h-px bg-line-soft" />
            <Menu.Item
              className="flex w-full cursor-default select-none items-center rounded px-2.5 py-1.5 text-[13px] text-danger outline-none data-[highlighted]:bg-danger-soft"
              onSelect={() => { setError(''); setDialog('delete') }}
            >
              Delete
            </Menu.Item>
          </Menu.Content>
        </Menu.Portal>
      </Menu.Root>

      {error && (
        <p role="alert" className="mt-1 text-[11.5px] text-danger">{error}</p>
      )}

      {dialog === 'configure' && (
        <ConfigureDialog keyMeta={keyMeta} onClose={() => setDialog('none')} onError={setError} />
      )}
      {dialog === 'trim' && (
        <TrimDialog keyMeta={keyMeta} onClose={() => setDialog('none')} onError={setError} />
      )}
      <ConfirmDialog
        open={dialog === 'delete'}
        onOpenChange={(o) => { if (!o) setDialog('none') }}
        title={`Delete ${keyMeta.name}?`}
        body="This permanently destroys the key and all its versions. Ciphertext encrypted with it can no longer be decrypted."
        confirmLabel="Delete"
        tone="danger"
        onConfirm={() => del.mutate()}
      />
    </>
  )
}
