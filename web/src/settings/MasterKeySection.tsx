import { useQuery } from '@tanstack/react-query'
import { endpoints } from '../lib/endpoints'
import { ApiError } from '../lib/api'
import { Card } from '../ui/Card'
import { Button } from '../ui/Button'
import { Pill } from '../ui/Pill'

// Master-key rotation status card (owner-only). This task renders STATUS +
// affordance only: the "Rotate master key" button and (for Shamir) the
// share-collection modal are wired in a later task. The status carries no key
// material — just the unseal method, current version, and last-rotated time.
export function MasterKeySection() {
  const q = useQuery({ queryKey: ['master-key'], queryFn: endpoints.masterKeyStatus, retry: false })

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
        {/* Wired in the next task (rotate flow + Shamir share modal). */}
        <Button variant="primary" onClick={() => { /* TODO(T11): rotate flow */ }}>
          Rotate master key
        </Button>
      </div>
    </Card>
  )
}
