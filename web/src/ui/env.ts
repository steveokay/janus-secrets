import type { Tone } from './Pill'

// Doppler-signature env coding (spec §Environment colors):
// dev=info blue · staging/test/qa=warning amber · prod=danger red · other=info.
export function envTone(slugOrName: string): Extract<Tone, 'info' | 'warning' | 'danger'> {
  const s = slugOrName.toLowerCase()
  if (/^prod/.test(s)) return 'danger'
  if (/^(stag|test|qa)/.test(s)) return 'warning'
  return 'info'
}

// For the sidebar's 7px square env dots.
export const envDotClass: Record<ReturnType<typeof envTone>, string> = {
  info: 'bg-info',
  warning: 'bg-warning',
  danger: 'bg-danger',
}
