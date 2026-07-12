import type { AuditEvent } from '../lib/endpoints'
import type { Tone } from '../ui/Pill'

// THE audit result → pill tone mapping. Shared by AuditPage and the home
// ActivityFeed so the two surfaces can never disagree on severity:
// denied = danger (someone was blocked), error = warning (op faulted).
export const resultTone: Record<AuditEvent['result'], Tone> = {
  success: 'success',
  denied: 'danger',
  error: 'warning',
}
