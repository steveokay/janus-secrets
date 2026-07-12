import { useQuery } from '@tanstack/react-query'
import { endpoints, VerifyResult } from '../lib/endpoints'
import { useAuth } from '../auth/AuthProvider'
import { Pill } from '../ui/Pill'

// For kind==='user' the principal `name` is the email address; greet with the
// local part, first letter uppercased. Never render the full email here.
function displayName(email: string): string {
  const local = email.split('@')[0]
  return local ? local.charAt(0).toUpperCase() + local.slice(1) : email
}

function ChainBadge({ result }: { result: VerifyResult }) {
  return result.valid ? (
    <Pill tone="success" dot>chain verified</Pill>
  ) : (
    <Pill tone="danger" dot>chain FAILED</Pill>
  )
}

export function HomeHeader({ projectCount }: { projectCount: number }) {
  const { user } = useAuth()
  // Audit verify is admin-gated; on 403 the query errors and the badge simply
  // does not render (section hides rather than erroring).
  const verify = useQuery({ queryKey: ['audit', 'verify'], queryFn: endpoints.verifyAudit, retry: false })
  const hour = new Date().getHours()
  const daypart = hour < 12 ? 'morning' : hour < 18 ? 'afternoon' : 'evening'
  const who = user && user.kind === 'user' ? displayName(user.name) : null
  return (
    <div className="mb-6 flex flex-wrap items-baseline justify-between gap-3">
      <div>
        <h2 className="text-[20px] font-bold text-ink-hi">{who ? `Good ${daypart}, ${who}` : 'Welcome'}</h2>
        <p className="mt-0.5 text-[12px] text-ink-faint">
          {projectCount} project{projectCount === 1 ? '' : 's'}
        </p>
      </div>
      {verify.data && <ChainBadge result={verify.data} />}
    </div>
  )
}
