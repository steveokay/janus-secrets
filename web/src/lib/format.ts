/* Client-side format awareness for secret values (JSON / PEM).
   Detection and validation run only on plaintext already revealed into
   component state — nothing here talks to the network. Validation is
   advisory: it never blocks a save. */

export type ValueFormat = 'json' | 'pem'

export interface FormatCheck {
  format: ValueFormat
  ok: boolean
  /** e.g. "CERTIFICATE", "RSA PRIVATE KEY" — first PEM block label */
  label?: string
  /** extra PEM blocks beyond the first (cert chains) */
  extraBlocks?: number
  error?: string
}

const PEM_BLOCK = /-----BEGIN ([A-Z0-9 ]+)-----([\s\S]*?)-----END ([A-Z0-9 ]+)-----/g

/** Best-effort format sniff. `type` is the secret's declared type when the
    API provides one; content wins so pasted values are recognized either way. */
export function detectFormat(value: string, type?: string): ValueFormat | null {
  const t = value.trim()
  if (!t) return null
  if (t.includes('-----BEGIN ')) return 'pem'
  if (type === 'certificate' || type === 'ssh_key') return 'pem'
  if (t.startsWith('{') || t.startsWith('[')) return 'json'
  if (type === 'json') return 'json'
  return null
}

export function checkJson(value: string): FormatCheck {
  try {
    JSON.parse(value)
    return { format: 'json', ok: true }
  } catch (err) {
    return { format: 'json', ok: false, error: jsonError(err) }
  }
}

function jsonError(err: unknown): string {
  const msg = err instanceof Error ? err.message : 'invalid JSON'
  // V8: "Unexpected token } in JSON at position 42" — keep it, it's useful.
  return msg.replace(/^JSON\.parse: /, '')
}

export function checkPem(value: string): FormatCheck {
  const blocks = [...value.matchAll(PEM_BLOCK)]
  if (!blocks.length) {
    return { format: 'pem', ok: false, error: 'no complete -----BEGIN/END----- block' }
  }
  for (const [, begin, body, end] of blocks) {
    if (begin !== end) {
      return { format: 'pem', ok: false, label: begin, error: `BEGIN ${begin} closed by END ${end}` }
    }
    const b64 = body!.replace(/\s+/g, '')
    if (!b64) {
      return { format: 'pem', ok: false, label: begin, error: `${begin} block is empty` }
    }
    if (!/^[A-Za-z0-9+/]+={0,2}$/.test(b64) || b64.length % 4 !== 0) {
      return { format: 'pem', ok: false, label: begin, error: `${begin} body is not valid base64` }
    }
  }
  // Text outside blocks is legal PEM ("explanatory text"), so no check for it.
  return {
    format: 'pem',
    ok: true,
    label: blocks[0]![1],
    extraBlocks: blocks.length - 1,
  }
}

export function checkFormat(value: string, type?: string): FormatCheck | null {
  const f = detectFormat(value, type)
  if (f === 'json') return checkJson(value)
  if (f === 'pem') return checkPem(value)
  return null
}

/** 2-space pretty-print; null when the value is invalid JSON. */
export function prettyJson(value: string): string | null {
  try {
    return JSON.stringify(JSON.parse(value), null, 2)
  } catch {
    return null
  }
}
