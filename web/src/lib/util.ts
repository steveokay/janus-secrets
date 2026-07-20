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

/* ── bulk import: dotenv + Java .properties ─────────────────── */

export interface ImportedEntry {
  key: string
  value: string
  line: number
  error?: string
}

function unquoteEnvValue(raw: string): string {
  const v = raw.trim()
  if (v.length >= 2 && v.startsWith('"') && v.endsWith('"')) {
    return v.slice(1, -1)
      .replace(/\\n/g, '\n')
      .replace(/\\t/g, '\t')
      .replace(/\\r/g, '\r')
      .replace(/\\"/g, '"')
      .replace(/\\\\/g, '\\')
  }
  if (v.length >= 2 && v.startsWith("'") && v.endsWith("'")) {
    return v.slice(1, -1)
  }
  // unquoted: strip a trailing inline comment (` # …`) the way dotenv tools do
  const hash = v.search(/\s#/)
  return (hash >= 0 ? v.slice(0, hash) : v).trim()
}

function unescapeProps(raw: string): string {
  return raw
    .replace(/\\u([0-9a-fA-F]{4})/g, (_, h) => String.fromCharCode(parseInt(h, 16)))
    .replace(/\\n/g, '\n')
    .replace(/\\t/g, '\t')
    .replace(/\\r/g, '\r')
    .replace(/\\([:=# !\\])/g, '$1')
}

/**
 * Parse dotenv or Java .properties text into key/value entries.
 * Handles: comments (# and !), `export ` prefixes, quoted dotenv values,
 * properties `=`/`:`/whitespace separators, and backslash line continuations.
 * Invalid keys come back with `error` set instead of being dropped silently.
 */
export function parseEnvOrProps(text: string): ImportedEntry[] {
  const out: ImportedEntry[] = []
  const lines = text.replace(/^﻿/, '').split(/\r\n|\r|\n/)
  for (let i = 0; i < lines.length; i++) {
    const startLine = i + 1
    let line = lines[i]
    // properties-style continuation: trailing single backslash joins the next line
    while (/(^|[^\\])(\\\\)*\\$/.test(line) && i + 1 < lines.length) {
      line = line.slice(0, -1) + lines[++i].replace(/^\s+/, '')
    }
    const t = line.trim()
    if (!t || t.startsWith('#') || t.startsWith('!')) continue

    const body = t.startsWith('export ') ? t.slice(7).trim() : t
    // find the first unescaped separator: '=' or ':' (properties also allows whitespace)
    let sep = -1
    let sepChar = ''
    for (let j = 0; j < body.length; j++) {
      const c = body[j]
      if (c === '\\') { j++; continue }
      if (c === '=' || c === ':') { sep = j; sepChar = c; break }
      if (/\s/.test(c) && sep === -1) { sep = j; sepChar = ' '; break }
    }
    if (sep <= 0) {
      out.push({ key: body, value: '', line: startLine, error: 'no key=value separator' })
      continue
    }
    const rawKey = body.slice(0, sep).trim()
    let valStart = sep + 1
    if (sepChar === ' ') {
      // properties `key = value` / `key : value`: skip whitespace, then one
      // optional '='/':' separator, then its trailing whitespace
      while (valStart < body.length && /\s/.test(body[valStart])) valStart++
      if (body[valStart] === '=' || body[valStart] === ':') {
        sepChar = body[valStart]
        valStart++
      }
    }
    const rawVal = body.slice(valStart)
    const key = unescapeProps(rawKey)
    // '=' with quotes → dotenv semantics; ':'/whitespace → properties semantics
    const value = sepChar === '=' && /^\s*["']/.test(rawVal) ? unquoteEnvValue(rawVal)
      : sepChar === '=' ? unquoteEnvValue(rawVal)
      : unescapeProps(rawVal.replace(/^\s+/, ''))
    out.push(isValidKey(key)
      ? { key, value, line: startLine }
      : { key, value, line: startLine, error: 'invalid key — letters, digits, . _ - only' })
  }
  return out
}
