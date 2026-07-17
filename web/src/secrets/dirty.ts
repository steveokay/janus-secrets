import { SecretChange } from '../lib/endpoints'
import type { SecretType } from './secretTypes'
import { normalizeType } from './secretTypes'

// A Buffer records intended edits keyed by secret name. `null` value = deletion.
export type Buffer = Record<string, { value: string | null; type?: SecretType }>
type Original = Record<string, string>
// Server-known type per key, as reported by the masked list (`MaskedSecret.type`).
// Absent = unknown / defaults to 'string' via normalizeType.
export type ServerTypes = Record<string, string | undefined>

export const emptyBuffer = (): Buffer => ({})
export const setValue = (b: Buffer, key: string, value: string): Buffer => ({ ...b, [key]: { ...b[key], value } })
export const addKey = setValue // adding and editing both set a value; save() diffs vs original
// Removal is a value-level state; a deleted row has no meaningful type, so drop any staged type.
export const removeKey = (b: Buffer, key: string): Buffer => ({ ...b, [key]: { value: null } })
export const revert = (b: Buffer, key: string): Buffer => { const { [key]: _drop, ...rest } = b; return rest }
export const setType = (b: Buffer, key: string, type: SecretType): Buffer => ({
  ...b,
  [key]: { value: b[key] ? b[key].value : null, type },
})

// A buffer entry is "effective" only if it actually differs from the original
// value OR the server's known type (a type-only change on an existing key
// still counts). `type` is populated whenever the entry carries a value change
// and/or a real type change, so callers can forward it to the save payload.
// `existing` (key already on the server) is `original` OR `serverTypes` —
// `original` only holds keys whose raw value has been fetched (e.g. via an
// in-progress edit), so a key that's merely had its type toggled (no value
// touched) is still recognized as existing via `serverTypes`.
function effective(
  b: Buffer,
  original: Original,
  serverTypes: ServerTypes = {},
): Array<{ key: string; value: string | null; type?: string }> {
  const out: Array<{ key: string; value: string | null; type?: string }> = []
  for (const [key, entry] of Object.entries(b)) {
    const { value } = entry
    const had = key in original
    const existing = had || key in serverTypes
    if (value === null && entry.type === undefined) {
      if (had) out.push({ key, value: null }) // delete only a key that exists
      continue
    }
    const valueChanged = value !== null && (!had || original[key] !== value)
    const serverType = normalizeType(serverTypes[key])
    const bufferType = entry.type === undefined ? serverType : normalizeType(entry.type)
    const typeChanged = existing && bufferType !== serverType
    if (value === null) {
      // type-only entry (no value edit) — only meaningful once the key's raw
      // value has actually been fetched into `original` (the editor always
      // does this before staging a type change). If it hasn't landed yet,
      // there's nothing safe to send — skip rather than risk emitting a
      // value:null, which toChanges would misread as a delete.
      if (typeChanged && had) out.push({ key, value: original[key], type: bufferType })
      continue
    }
    if (!valueChanged && !typeChanged) continue
    out.push({ key, value, type: entry.type !== undefined ? bufferType : undefined })
  }
  return out
}

export const isDirty = (b: Buffer, original: Original = {}, serverTypes: ServerTypes = {}): boolean =>
  effective(b, original, serverTypes).length > 0

export function summarize(b: Buffer, original: Original, serverTypes: ServerTypes = {}) {
  let added = 0, changed = 0, removed = 0
  for (const e of effective(b, original, serverTypes)) {
    if (e.value === null) removed++
    else if (e.key in original) changed++
    else added++
  }
  return { added, changed, removed }
}

export function toChanges(b: Buffer, original: Original, serverTypes: ServerTypes = {}): SecretChange[] {
  return effective(b, original, serverTypes).map((e) =>
    e.value === null
      ? { key: e.key, delete: true }
      : e.type !== undefined
        ? { key: e.key, value: e.value, type: e.type }
        : { key: e.key, value: e.value },
  )
}
