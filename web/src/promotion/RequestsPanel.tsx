import { useState } from 'react'
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { Button } from '../ui/Button'
import { Pill, type Tone } from '../ui/Pill'
import { Skeleton } from '../ui/Skeleton'
import { EmptyState } from '../ui/EmptyState'
import { useToast } from '../ui/Toast'
import { apiErrorTitle } from '../lib/api'
import { promotion, type PromotionRequest, type PromotionRequestStatus } from './endpoints'
import { usePendingRequests, useMyRequests } from './useRequests'
import { useAuth } from '../auth/AuthProvider'
import { RequestReview } from './RequestReview'

const STATUS_TONE: Record<PromotionRequestStatus, Tone> = {
  pending: 'warning',
  applied: 'success',
  rejected: 'danger',
  cancelled: 'muted',
}

function StatusPill({ status }: { status: PromotionRequestStatus }) {
  return <Pill tone={STATUS_TONE[status]} dot>{status}</Pill>
}

function RequestRow({
  req,
  canCancel,
  onReview,
}: {
  req: PromotionRequest
  canCancel: boolean
  onReview: (id: string) => void
}) {
  const qc = useQueryClient()
  const toast = useToast()
  const invalidate = () => void qc.invalidateQueries({ queryKey: ['promote-requests'] })

  const cancel = useMutation({
    mutationFn: () => promotion.requests.cancel(req.id),
    onSuccess: () => { invalidate(); toast({ title: 'Request cancelled', tone: 'success' }) },
    onError: (e) => toast({ title: apiErrorTitle(e), tone: 'danger' }),
  })

  return (
    <div className="flex flex-wrap items-center justify-between gap-3 rounded border border-line-soft bg-surface-1 px-3 py-2.5">
      <div className="min-w-0 flex-1">
        <div className="flex flex-wrap items-center gap-2">
          <span className="font-mono text-[12.5px] font-semibold text-ink-hi">{req.target_name}</span>
          <span className="text-[11.5px] text-ink-faint">{req.keys.length} key{req.keys.length === 1 ? '' : 's'}</span>
          <StatusPill status={req.status} />
        </div>
        <div className="mt-1 text-[11.5px] text-ink-mute">
          requested by <span className="text-ink-body">{req.requested_by}</span>
        </div>
        {req.note && <div className="mt-1 text-[12px] text-ink-body">{req.note}</div>}
      </div>
      <div className="flex items-center gap-2">
        {req.status === 'pending' && (
          <>
            <Button size="sm" variant="danger" onClick={() => onReview(req.id)}>Reject</Button>
            <Button size="sm" onClick={() => onReview(req.id)}>Approve</Button>
          </>
        )}
        {req.status === 'pending' && canCancel && (
          <Button size="sm" variant="secondary" loading={cancel.isPending} onClick={() => cancel.mutate()}>
            Cancel
          </Button>
        )}
      </div>
    </div>
  )
}

function RequestList({ requests, canCancel, onReview }: {
  requests: PromotionRequest[]
  canCancel: (req: PromotionRequest) => boolean
  onReview: (id: string) => void
}) {
  return (
    <div className="flex flex-col gap-2">
      {requests.map((r) => (
        <RequestRow key={r.id} req={r} canCancel={canCancel(r)} onReview={onReview} />
      ))}
    </div>
  )
}

export function RequestsPanel({ projectId }: { projectId: string }) {
  const { user } = useAuth()
  const pending = usePendingRequests(projectId)
  const mine = useMyRequests(projectId)
  const [reviewing, setReviewing] = useState<string | null>(null)

  const canCancel = (req: PromotionRequest) => !!user && req.requested_by === user.id

  return (
    <div className="flex flex-col gap-6">
      <section>
        <h2 className="mb-2 text-[13px] font-semibold text-ink">Pending approval</h2>
        {pending.isLoading && (
          <div className="flex flex-col gap-2">
            {[0, 1].map((i) => <Skeleton key={i} className="h-14 w-full" />)}
          </div>
        )}
        {pending.isError && <p role="alert" className="text-[12.5px] text-danger">Couldn't load requests.</p>}
        {pending.data && pending.data.length === 0 && (
          <EmptyState title="No pending requests" hint="Requests filed by teammates will show up here for review." className="mt-4" />
        )}
        {pending.data && pending.data.length > 0 && (
          <RequestList requests={pending.data} canCancel={canCancel} onReview={setReviewing} />
        )}
      </section>

      <section>
        <h2 className="mb-2 text-[13px] font-semibold text-ink">My requests</h2>
        {mine.isLoading && (
          <div className="flex flex-col gap-2">
            {[0].map((i) => <Skeleton key={i} className="h-14 w-full" />)}
          </div>
        )}
        {mine.isError && <p role="alert" className="text-[12.5px] text-danger">Couldn't load your requests.</p>}
        {mine.data && mine.data.length === 0 && (
          <EmptyState title="No requests yet" hint="Requests you file will show up here." className="mt-4" />
        )}
        {mine.data && mine.data.length > 0 && (
          <RequestList requests={mine.data} canCancel={canCancel} onReview={setReviewing} />
        )}
      </section>

      {reviewing && (
        <RequestReview
          requestId={reviewing}
          open={!!reviewing}
          onOpenChange={(o) => { if (!o) setReviewing(null) }}
        />
      )}
    </div>
  )
}
