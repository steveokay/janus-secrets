import { useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { endpoints } from '../lib/endpoints'
import { ApiError, errorMessage } from '../lib/api'
import { Card } from '../ui/Card'
import { Button } from '../ui/Button'
import { Input } from '../ui/Input'
import { Pill } from '../ui/Pill'
import { Modal } from '../ui/Modal'
import { ConfirmDialog } from '../ui/ConfirmDialog'
import { RevealOnce } from '../ui/RevealOnce'
import { useToast } from '../ui/Toast'
import { cn } from '../ui/cn'

// Master-key rotation card (owner-only). Two flows by unseal type:
//  - awskms: a single POST /rotate mints a new key version; success toasts the
//    version and refetches status.
//  - shamir: a rekey CEREMONY — init issues a nonce, the operator submits the
//    quorum of OLD shares one at a time, and the final submit returns the FRESH
//    shares ONCE. Those new shares are the only sensitive strings this component
//    ever renders; they live in ephemeral state, appear only inside RevealOnce,
//    and are cleared on close/unmount. Never logged, toasted, or persisted.
export function MasterKeySection() {
  const toast = useToast()
  const qc = useQueryClient()
  const q = useQuery({ queryKey: ['master-key'], queryFn: endpoints.masterKeyStatus, retry: false })

  // Rotate confirmation (both flows share the same danger gate).
  const [confirming, setConfirming] = useState(false)

  // Shamir ceremony state. `nonce` binds the submit calls to this init; `share`
  // is the operator's current input (held only long enough to submit). The fresh
  // shares from a completed rekey land in `newShares` and render via RevealOnce.
  const [rekeyOpen, setRekeyOpen] = useState(false)
  const [nonce, setNonce] = useState('')
  const [required, setRequired] = useState(0)
  const [submitted, setSubmitted] = useState(0)
  const [share, setShare] = useState('')
  const [rekeyError, setRekeyError] = useState('')
  const [newShares, setNewShares] = useState<string[] | null>(null)

  function resetRekey() {
    setNonce('')
    setRequired(0)
    setSubmitted(0)
    setShare('')
    setRekeyError('')
    // NOTE: newShares are cleared by RevealOnce's onClose, not here — closing the
    // ceremony modal must not yank the reveal out from under the operator.
  }

  // awskms: single-call rotate.
  const rotate = useMutation({
    mutationFn: endpoints.rotateMasterKey,
    onSuccess: (r) => {
      qc.invalidateQueries({ queryKey: ['master-key'] })
      toast({ title: `Master key rotated (v${r.master_key_version})` })
    },
    onError: (e) => toast({ title: errorMessage(e), tone: 'danger' }),
  })

  // shamir: begin the ceremony — open the modal and init a nonce.
  const init = useMutation({
    mutationFn: endpoints.rekeyInit,
    onSuccess: (r) => {
      setNonce(r.nonce)
      setRequired(r.required)
      setSubmitted(r.submitted)
    },
    onError: (e) => setRekeyError(errorMessage(e)),
  })

  // shamir: submit one old share against the ceremony nonce.
  const submit = useMutation({
    mutationFn: () => endpoints.rekeySubmit(nonce, share),
    onSuccess: (r) => {
      setShare('')
      setRekeyError('')
      if (r.complete) {
        setSubmitted(r.required ?? required)
        setNewShares(r.new_shares ?? [])
        setRekeyOpen(false)
        qc.invalidateQueries({ queryKey: ['master-key'] })
      } else {
        setSubmitted(r.submitted ?? submitted)
        if (r.required !== undefined) setRequired(r.required)
      }
    },
    onError: (e) => {
      // A rejected share is expected operator error — surface INLINE, keep the
      // ceremony open so they can retry. Never reveal shares on a failed submit.
      setShare('')
      setRekeyError(errorMessage(e, 'That share was rejected.'))
    },
  })

  function onConfirmRotate() {
    setConfirming(false)
    if (q.data?.unseal_type === 'shamir') {
      resetRekey()
      setRekeyOpen(true)
      init.mutate()
    } else {
      rotate.mutate()
    }
  }

  function closeRekey() {
    // Best-effort cancel of the in-flight ceremony; ignore failures.
    if (nonce) endpoints.rekeyCancel().catch(() => {})
    setRekeyOpen(false)
    resetRekey()
  }

  if (q.isLoading) return <p className="text-[12.5px] text-ink-mute">Loading…</p>

  // Owner-only: a non-owner (403) sees a muted note, never the rotate control.
  const err = q.error
  if (err instanceof ApiError && err.status === 403) {
    return (
      <Card className="p-4">
        <h3 className="text-[15px] font-semibold text-ink">Master key</h3>
        <p className="mt-1 text-[12.5px] text-ink-mute">Rotating the master key requires the instance owner role.</p>
      </Card>
    )
  }

  const s = q.data
  if (!s) return <p className="text-[12.5px] text-ink-mute">Unable to read master-key status.</p>

  const rotated = s.rotated_at
    ? new Date(s.rotated_at).toLocaleString()
    : 'Never'
  const isShamir = s.unseal_type === 'shamir'

  return (
    <Card className="p-4">
      <div className="mb-3 flex items-center gap-2">
        <h3 className="text-[15px] font-semibold text-ink">Master key</h3>
        {s.rekey_in_progress && <Pill tone="warning" dot>Rekey in progress</Pill>}
      </div>
      <p className="mb-4 text-[12.5px] text-ink-mute">
        The root key wrapping all project keys. Rotating it re-wraps every project key online.
      </p>

      <dl className="flex flex-col gap-2.5">
        <div className="flex items-center gap-2">
          <dt className="w-24 text-[12px] text-ink-mute">Method</dt>
          <dd className="text-[12.5px] font-medium text-ink">{s.unseal_type}</dd>
        </div>
        <div className="flex items-center gap-2">
          <dt className="w-24 text-[12px] text-ink-mute">Version</dt>
          <dd className="text-[12.5px] font-medium text-ink tabular-nums">version {s.master_key_version}</dd>
        </div>
        <div className="flex items-center gap-2">
          <dt className="w-24 text-[12px] text-ink-mute">Last rotated</dt>
          <dd className="text-[12.5px] font-medium text-ink">{rotated}</dd>
        </div>
      </dl>

      <div className="mt-4">
        <Button variant="primary" loading={rotate.isPending} onClick={() => setConfirming(true)}>
          Rotate master key
        </Button>
      </div>

      <ConfirmDialog
        open={confirming}
        onOpenChange={setConfirming}
        title="Rotate master key?"
        body={
          isShamir
            ? 'This mints fresh master-key material and re-wraps every project key. New key shares are issued and shown ONCE — you will submit the current quorum of shares to authorize it. Take an instance backup first.'
            : 'This mints fresh master-key material and re-wraps every project key online. Take an instance backup first.'
        }
        confirmLabel="Rotate"
        tone="danger"
        onConfirm={onConfirmRotate}
      />

      {/* Shamir rekey ceremony — solid Modal surface. */}
      <Modal open={rekeyOpen} onClose={closeRekey} label="Rekey master key" className="w-[420px]">
        <h3 className="mb-1 text-[15px] font-semibold tracking-tight text-ink">Rekey master key</h3>
        <p className="mb-3 text-[12.5px] text-ink-mute">
          Submit the current quorum of key shares to authorize a new master key. New shares are shown once when the ceremony completes.
        </p>

        {init.isPending && !nonce ? (
          <p className="text-[12.5px] text-ink-mute">Starting ceremony…</p>
        ) : (
          <>
            <p className="mb-2 text-[12.5px] text-ink-mute tabular-nums">
              {submitted} of {required} shares submitted
            </p>
            <div
              className="mb-4 flex gap-1.5"
              aria-label={`Share progress: ${submitted} of ${required}`}
            >
              {Array.from({ length: required }, (_, i) => (
                <span
                  key={i}
                  className={cn('h-1.5 flex-1 rounded-full', i < submitted ? 'bg-success' : 'bg-line')}
                />
              ))}
            </div>
            <form
              onSubmit={(e) => { e.preventDefault(); if (share) submit.mutate() }}
              className="flex flex-col gap-3"
            >
              <Input
                label="Key share"
                type="password"
                autoComplete="off"
                value={share}
                onChange={(e) => setShare(e.target.value)}
                className="font-mono"
              />
              {rekeyError && (
                <p role="alert" className="text-[12px] text-danger">{rekeyError}</p>
              )}
              <div className="flex justify-end gap-2">
                <Button type="button" variant="secondary" size="sm" onClick={closeRekey}>Cancel</Button>
                <Button type="submit" size="sm" loading={submit.isPending} disabled={!share || !nonce}>
                  Submit share
                </Button>
              </div>
            </form>
          </>
        )}
      </Modal>

      {/* Fresh shares shown ONCE. Cleared from state when the reveal closes. */}
      <RevealOnce
        open={newShares !== null}
        onClose={() => setNewShares(null)}
        title="New key shares"
        secret={(newShares ?? []).join('\n')}
        hint="Distribute these to your key holders now. They will never be shown again."
      />
    </Card>
  )
}
