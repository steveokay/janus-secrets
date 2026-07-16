import { useState } from 'react'
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { Sheet } from '../ui/Sheet'
import { Button } from '../ui/Button'
import { Pill, type Tone } from '../ui/Pill'
import { Textarea } from '../ui/Textarea'
import { useToast } from '../ui/Toast'
import { apiErrorTitle } from '../lib/api'
import { promotion, type PromoteStatus } from './endpoints'
import { usePromotionRequest } from './useRequests'

const CHIP: Record<PromoteStatus, { tone: Tone; label: string }> = {
  add: { tone: 'success', label: 'Add' },
  change: { tone: 'warning', label: 'Change' },
  remove: { tone: 'danger', label: 'Remove' },
  same: { tone: 'muted', label: 'Unchanged' },
}

// Value-free diff + note review for a single promotion request, with
// approve/reject actions. Reused both from the RequestsPanel rows and from the
// direct-promote fallback ("request approval instead").
export function RequestReview({
  requestId,
  open,
  onOpenChange,
  onDecided,
}: {
  requestId: string
  open: boolean
  onOpenChange: (open: boolean) => void
  onDecided?: () => void
}) {
  const qc = useQueryClient()
  const toast = useToast()
  const req = usePromotionRequest(requestId)
  const [rejecting, setRejecting] = useState(false)
  const [note, setNote] = useState('')

  const invalidate = () => {
    void qc.invalidateQueries({ queryKey: ['promote-requests'] })
    void qc.invalidateQueries({ queryKey: ['promote-request', requestId] })
  }

  const approve = useMutation({
    mutationFn: () => promotion.requests.approve(requestId),
    onSuccess: () => { invalidate(); onDecided?.() },
    onError: (e) => toast({ title: apiErrorTitle(e), tone: 'danger' }),
  })

  const reject = useMutation({
    mutationFn: () => promotion.requests.reject(requestId, note),
    onSuccess: () => { invalidate(); onDecided?.() },
    onError: (e) => toast({ title: apiErrorTitle(e), tone: 'danger' }),
  })

  const data = req.data

  return (
    <Sheet open={open} onOpenChange={onOpenChange} title="Review promotion request">
      {req.isLoading && <p className="text-[12.5px] text-ink-mute">Loading…</p>}
      {req.isError && <p role="alert" className="text-[12.5px] text-danger">Couldn't load the request.</p>}

      {data && (
        <div className="flex flex-col gap-4">
          <div className="text-[12.5px] text-ink-mute">
            Target <span className="font-mono text-ink-body">{data.target_name}</span>
            {' · '}requested by <span className="text-ink-body">{data.requested_by}</span>
          </div>

          {data.note && (
            <div className="rounded border border-line-soft bg-surface-2 p-2.5 text-[12.5px] text-ink-body">
              {data.note}
            </div>
          )}

          <div className="flex flex-col gap-1.5">
            {(data.diff?.entries ?? []).map((e) => (
              <div key={e.key} className="flex items-center justify-between gap-2 rounded px-2 py-1.5 hover:bg-row-hover">
                <span className="font-mono text-[12.5px] text-ink-hi">{e.key}</span>
                <Pill tone={CHIP[e.status].tone}>{CHIP[e.status].label}</Pill>
              </div>
            ))}
          </div>

          {approve.isSuccess && approve.data && (
            <div className="rounded border border-success-line bg-success-soft p-2.5 text-[12.5px] text-success">
              Applied as v{approve.data.target_version} · applied {approve.data.applied.length}
              {approve.data.skipped.length > 0 && (
                <div className="mt-1 text-ink-mute">
                  Skipped: {approve.data.skipped.join(', ')}
                </div>
              )}
            </div>
          )}

          {reject.isSuccess && (
            <div className="rounded border border-line-soft bg-surface-2 p-2.5 text-[12.5px] text-ink-mute">
              Request rejected.
            </div>
          )}

          {data.status === 'pending' && !approve.isSuccess && !reject.isSuccess && (
            <>
              {!rejecting ? (
                <div className="flex justify-end gap-2">
                  <Button variant="danger" size="sm" onClick={() => setRejecting(true)}>Reject</Button>
                  <Button size="sm" loading={approve.isPending} onClick={() => approve.mutate()}>Approve</Button>
                </div>
              ) : (
                <div className="flex flex-col gap-2">
                  <Textarea
                    label="Reason"
                    aria-label="reason"
                    value={note}
                    onChange={(e) => setNote(e.target.value)}
                    placeholder="Why is this request being rejected?"
                  />
                  <div className="flex justify-end gap-2">
                    <Button variant="secondary" size="sm" onClick={() => setRejecting(false)}>Cancel</Button>
                    <Button
                      variant="danger"
                      size="sm"
                      loading={reject.isPending}
                      onClick={() => reject.mutate()}
                    >
                      Confirm reject
                    </Button>
                  </div>
                </div>
              )}
            </>
          )}
        </div>
      )}
    </Sheet>
  )
}
