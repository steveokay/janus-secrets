/* Small display helpers. */

const min = 60_000
const hr = 3_600_000
const day = 86_400_000

export function relTime(isoStr: string | null | undefined): string {
  if (!isoStr) return '—'
  const d = Date.now() - new Date(isoStr).getTime()
  if (d < 0) {
    const f = -d
    if (f < hr) return `in ${Math.max(1, Math.round(f / min))}m`
    if (f < day) return `in ${Math.round(f / hr)}h`
    return `in ${Math.round(f / day)}d`
  }
  if (d < min) return 'just now'
  if (d < hr) return `${Math.round(d / min)}m ago`
  if (d < day) return `${Math.round(d / hr)}h ago`
  if (d < 30 * day) return `${Math.round(d / day)}d ago`
  return new Date(isoStr).toLocaleDateString('en-GB', { day: 'numeric', month: 'short', year: 'numeric' })
}

export function stampDate(isoStr: string | null | undefined): string {
  if (!isoStr) return '—'
  return new Date(isoStr)
    .toLocaleDateString('en-GB', { day: '2-digit', month: 'short', year: 'numeric' })
    .toUpperCase()
}

export function clockTime(isoStr: string): string {
  return new Date(isoStr).toLocaleTimeString('en-GB', { hour: '2-digit', minute: '2-digit' })
}

export function shortDate(isoStr: string): string {
  return new Date(isoStr).toLocaleDateString('en-GB', { day: '2-digit', month: 'short' }).toUpperCase()
}

/* Secret-key rules — mirror internal/secrets validateKey exactly. */
const VALID_KEY_RE = /^[A-Za-z0-9._-]+$/

/** Filename-safe key: letters/digits/._-, not '.'/'..', no slashes, <=255. */
export function isValidKey(k: string): boolean {
  return k.length > 0 && k.length <= 255 && k !== '.' && k !== '..' &&
    !k.includes('/') && !k.includes('\\') && VALID_KEY_RE.test(k)
}

/** True if `janus run` can inject the key as an environment variable. */
export function isEnvVarKey(k: string): boolean {
  return /^[A-Za-z_][A-Za-z0-9_]*$/.test(k)
}
