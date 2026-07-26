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

/* Humanize a max-age duration (whole seconds) for the advisory expiry chips.
   Whole days render as "90d"; otherwise falls back to hours/minutes. */
export function humanizeDuration(seconds: number): string {
  if (seconds % 86_400 === 0) return `${seconds / 86_400}d`
  if (seconds % 3_600 === 0) return `${seconds / 3_600}h`
  if (seconds % 60 === 0) return `${seconds / 60}m`
  return `${seconds}s`
}

/* Parse a human max-age into whole seconds, mirroring the CLI parseMaxAge:
   Go durations (e.g. "2160h", "30m") plus day "<n>d" / week "<n>w" suffixes.
   Returns null on an invalid or non-positive value. */
export function parseDurationToSeconds(input: string): number | null {
  const s = input.trim()
  if (!s) return null
  const suffix = s.slice(-1)
  const numPart = s.slice(0, -1)
  if (suffix === 'd' || suffix === 'w') {
    const n = Number(numPart)
    if (!Number.isFinite(n)) return null
    const secs = Math.round(n * (suffix === 'd' ? 86_400 : 604_800))
    return secs > 0 ? secs : null
  }
  // Go-style compound duration: sum of <num><unit> parts (h/m/s).
  const re = /(\d+(?:\.\d+)?)([hms])/g
  let total = 0
  let matched = false
  let m: RegExpExecArray | null
  while ((m = re.exec(s)) !== null) {
    matched = true
    const n = Number(m[1])
    total += n * (m[2] === 'h' ? 3_600 : m[2] === 'm' ? 60 : 1)
  }
  // Reject stray characters (anything not consumed by the unit grammar).
  if (!matched || s.replace(/(\d+(?:\.\d+)?)([hms])/g, '').trim() !== '') return null
  const secs = Math.round(total)
  return secs > 0 ? secs : null
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
  /** Stable identity within one parse: the 1-based source line for text
   *  formats, or a synthetic 1-based sequence number for JSON formats. */
  line: number
  error?: string
  /** Advisory remark shown next to the key (e.g. a deliberate JSON encoding). */
  note?: string
  /** Human location of the entry in the pasted document, when not a line. */
  where?: string
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

/* ── bulk import: source export formats ─────────────────────
 *
 * The web importer is deliberately PASTE-BASED. It never holds a Doppler /
 * Vault / AWS credential and makes no outbound call of its own — the operator
 * exports with the tool they already trust and pastes the result. Every parser
 * below is a pure function over that text; nothing here touches the network.
 *
 * Leaf semantics mirror cmd/janus/import_sources.go (`jsonLeafString` /
 * `mergeSMSecret`) so a web import and a `janus import` land the same values.
 */

export type ImportFormat = 'env' | 'doppler' | 'vault' | 'aws-sm'

export interface ImportSourceInfo {
  id: ImportFormat
  label: string
  /** Bare product name, for use mid-sentence. */
  short: string
  /** One line describing what to paste. */
  blurb: string
  /** The exact command to run against the source system, or null when the
   *  input is just a file the operator already has. */
  command: string | null
  /** A second command covering a common variant of the same source. */
  altLabel?: string
  altCommand?: string
}

/** Source catalogue driving the wizard's guidance step. */
export const IMPORT_SOURCES: ImportSourceInfo[] = [
  {
    id: 'env',
    label: '.env / .properties',
    short: 'dotenv',
    blurb: 'A dotenv or Java .properties file you already have on disk.',
    command: null,
  },
  {
    id: 'doppler',
    label: 'Doppler',
    short: 'Doppler',
    blurb: 'A flat JSON object of KEY → value, as the Doppler CLI downloads it.',
    command: 'doppler secrets download --no-file --format json --project acme --config prod',
  },
  {
    id: 'vault',
    label: 'Vault (KV v2)',
    short: 'Vault',
    blurb: 'The JSON envelope the Vault CLI prints for one KV v2 path.',
    command: 'vault kv get -format=json -mount=secret myapp/prod',
  },
  {
    id: 'aws-sm',
    label: 'AWS Secrets Manager',
    short: 'AWS',
    blurb: 'One secret, or a batch — the SecretString is read as JSON when it is an object.',
    command: 'aws secretsmanager get-secret-value --secret-id prod/myapp/db',
    altLabel: 'Several secrets at once',
    altCommand: 'aws secretsmanager batch-get-secret-value --secret-id-list prod/myapp/db prod/myapp/api',
  },
]

export function importSource(f: ImportFormat): ImportSourceInfo {
  return IMPORT_SOURCES.find(s => s.id === f) ?? IMPORT_SOURCES[0]
}

export interface ImportParse {
  /** Format actually used to parse — detected, or the caller's override. */
  format: ImportFormat
  entries: ImportedEntry[]
  /** True when `format` came from auto-detection rather than an override. */
  detected: boolean
  /** Set when the text could not be read as `format` at all. Never quotes
   *  the pasted text — it may contain secret values. */
  problem?: string
  /** Non-blocking remark about the document as a whole. */
  notice?: string
}

type JsonValue = string | number | boolean | null | JsonValue[] | { [k: string]: JsonValue }

function isPlainObject(v: unknown): v is Record<string, JsonValue> {
  return typeof v === 'object' && v !== null && !Array.isArray(v)
}

/**
 * Render one JSON leaf as a secret value. A JSON string is taken verbatim;
 * anything else keeps its JSON form so no data is lost — and says so, because
 * a silent coercion of `8080` to `"8080"` is exactly the kind of surprise a
 * secrets import must not spring on you.
 */
function jsonLeaf(v: JsonValue): { value: string; note?: string } {
  if (typeof v === 'string') return { value: v }
  if (v === null) return { value: 'null', note: 'null → JSON-encoded' }
  if (typeof v === 'number') return { value: JSON.stringify(v), note: 'number → JSON-encoded' }
  if (typeof v === 'boolean') return { value: JSON.stringify(v), note: 'boolean → JSON-encoded' }
  return { value: JSON.stringify(v), note: `${Array.isArray(v) ? 'array' : 'object'} → JSON-encoded` }
}

function leafEntry(key: string, v: JsonValue, line: number, where: string): ImportedEntry {
  if (!isValidKey(key)) {
    return { key, value: '', line, where, error: 'invalid key — letters, digits, . _ - only' }
  }
  const { value, note } = jsonLeaf(v)
  return note ? { key, value, line, where, note } : { key, value, line, where }
}

/** Flag repeat keys so the preview can say which one actually wins. */
function markDuplicates(entries: ImportedEntry[]): ImportedEntry[] {
  const seen = new Set<string>()
  for (const e of entries) {
    if (e.error) continue
    if (seen.has(e.key)) {
      e.note = e.note ? `${e.note} · duplicate key — the first selected wins` : 'duplicate key — the first selected wins'
    }
    seen.add(e.key)
  }
  return entries
}

/**
 * Guess which of the four supported shapes a paste is, from structure alone.
 * Anything that is not a JSON object falls back to the tolerant text parser.
 */
export function detectImportFormat(text: string): ImportFormat {
  const t = text.trim()
  if (!t.startsWith('{')) return 'env'
  let doc: unknown
  try {
    doc = JSON.parse(t)
  } catch {
    return 'env'
  }
  if (!isPlainObject(doc)) return 'env'
  if (Array.isArray(doc.SecretValues) || typeof doc.SecretString === 'string' || typeof doc.SecretBinary === 'string') {
    return 'aws-sm'
  }
  // Vault wraps its map in `data` (KV v2 nests it twice). A Doppler blob is
  // flat strings, so an object-valued `data` is a reliable tell.
  if (isPlainObject(doc.data)) return 'vault'
  return 'doppler'
}

/** Doppler: `{"KEY":"value", …}` — a flat object, one entry per field. */
function parseDoppler(doc: unknown): ImportParse {
  if (!isPlainObject(doc)) {
    return { format: 'doppler', entries: [], detected: false, problem: 'Expected a flat JSON object of KEY → value.' }
  }
  const keys = Object.keys(doc)
  const entries = keys.map((k, i) => leafEntry(k, doc[k], i + 1, k))
  return { format: 'doppler', entries: markDuplicates(entries), detected: false }
}

/** Vault KV v2: the full `{"data":{"data":{…},"metadata":{…}}}` envelope, a
 *  bare `{"data":{…}}`, or an already-unwrapped flat object. */
function parseVault(doc: unknown): ImportParse {
  if (!isPlainObject(doc)) {
    return { format: 'vault', entries: [], detected: false, problem: 'Expected a JSON object from `vault kv get -format=json`.' }
  }
  let data: Record<string, JsonValue> = doc
  let where = ''
  if (isPlainObject(doc.data)) {
    if (isPlainObject(doc.data.data)) {
      data = doc.data.data
      where = 'data.data'
    } else {
      data = doc.data
      where = 'data'
    }
  }
  const keys = Object.keys(data)
  if (!keys.length) {
    return { format: 'vault', entries: [], detected: false, problem: 'No keys found — the KV path holds an empty data map.' }
  }
  const entries = keys.map((k, i) => leafEntry(k, data[k], i + 1, where ? `${where}.${k}` : k))
  return { format: 'vault', entries: markDuplicates(entries), detected: false }
}

/** Strip a Secrets Manager name down to its trailing path segment. */
function smLeafKey(name: string): string {
  const trimmed = name.replace(/^\/+|\/+$/g, '')
  const i = trimmed.lastIndexOf('/')
  return i >= 0 ? trimmed.slice(i + 1) : trimmed
}

/**
 * One Secrets Manager secret → one or more Janus keys, mirroring the CLI:
 * a JSON *object* SecretString fans out to one key per field; anything else
 * becomes a single key named after the secret's trailing path segment.
 */
function mergeSMSecret(out: ImportedEntry[], secretString: string, name: string, where: string, next: () => number) {
  let parsed: unknown
  try {
    parsed = JSON.parse(secretString)
  } catch {
    parsed = undefined
  }
  if (isPlainObject(parsed)) {
    for (const k of Object.keys(parsed)) {
      out.push(leafEntry(k, parsed[k], next(), name ? `${where} · ${name}.${k}` : `${where}.${k}`))
    }
    return
  }
  const key = smLeafKey(name)
  if (!key) {
    out.push({
      key: '', value: '', line: next(), where,
      error: 'plain-string secret with no Name — cannot derive a key; add "Name" or paste it as KEY=value',
    })
    return
  }
  out.push(leafEntry(key, secretString, next(), `${where} · ${name}`))
}

/** AWS Secrets Manager: `get-secret-value` or `batch-get-secret-value` output. */
function parseAwsSM(doc: unknown): ImportParse {
  if (!isPlainObject(doc)) {
    return { format: 'aws-sm', entries: [], detected: false, problem: 'Expected the JSON object the AWS CLI prints.' }
  }
  const entries: ImportedEntry[] = []
  let seq = 0
  const next = () => ++seq

  if (Array.isArray(doc.SecretValues)) {
    doc.SecretValues.forEach((raw, idx) => {
      const where = `SecretValues[${idx}]`
      if (!isPlainObject(raw)) {
        entries.push({ key: '', value: '', line: next(), where, error: 'not a secret object' })
        return
      }
      const name = typeof raw.Name === 'string' ? raw.Name : ''
      if (typeof raw.SecretString === 'string') {
        mergeSMSecret(entries, raw.SecretString, name, where, next)
      } else if (typeof raw.SecretBinary === 'string') {
        entries.push({ key: smLeafKey(name), value: '', line: next(), where, error: 'binary secret — only string secrets can be imported' })
      } else {
        entries.push({ key: smLeafKey(name), value: '', line: next(), where, error: 'no SecretString on this secret' })
      }
    })
    const failed = Array.isArray(doc.Errors) ? doc.Errors.length : 0
    const parse: ImportParse = { format: 'aws-sm', entries: markDuplicates(entries), detected: false }
    if (failed) parse.notice = `${failed} secret${failed === 1 ? '' : 's'} in this batch reported an error from AWS and were not included.`
    if (!entries.length && !failed) parse.problem = 'The batch contained no secrets.'
    return parse
  }

  if (typeof doc.SecretString === 'string') {
    const name = typeof doc.Name === 'string' ? doc.Name : ''
    mergeSMSecret(entries, doc.SecretString, name, 'SecretString', next)
    return { format: 'aws-sm', entries: markDuplicates(entries), detected: false }
  }
  if (typeof doc.SecretBinary === 'string') {
    return { format: 'aws-sm', entries: [], detected: false, problem: 'Binary secret — only string secrets can be imported.' }
  }
  return { format: 'aws-sm', entries: [], detected: false, problem: 'No `SecretString` or `SecretValues` found in this output.' }
}

/**
 * Parse a paste in whichever supported format it is, or in `override` when the
 * operator has picked one by hand. Pure: no network, no storage, and error
 * text never echoes the pasted document (it may hold secret values).
 */
export function parseImport(text: string, override?: ImportFormat | null): ImportParse {
  const detected = override == null
  const format = override ?? detectImportFormat(text)
  if (!text.trim()) return { format, entries: [], detected }
  if (format === 'env') return { format, entries: markDuplicates(parseEnvOrProps(text)), detected }

  let doc: unknown
  try {
    doc = JSON.parse(text)
  } catch {
    return {
      format,
      entries: [],
      detected,
      problem: `Not valid JSON — ${importSource(format).label} export is a JSON document. Check you pasted the whole output.`,
    }
  }
  const parsed = format === 'doppler' ? parseDoppler(doc) : format === 'vault' ? parseVault(doc) : parseAwsSM(doc)
  parsed.detected = detected
  return parsed
}
