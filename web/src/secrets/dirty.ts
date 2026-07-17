import { SecretChange } from '../lib/endpoints'
import type { SecretType } from './secretTypes'

// A Buffer records intended edits keyed by secret name. `null` value = deletion.
export type Buffer = Record<string, { value: string | null; type?: SecretType }>
type Original = Record<string, string>

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

// A buffer entry is "effective" only if it actually differs from the original.
function effective(b: Buffer, original: Original): Array<{ key: string; value: string | null }> {
  const out: Array<{ key: string; value: string | null }> = []
  for (const [key, { value }] of Object.entries(b)) {
    const had = key in original
    if (value === null) { if (had) out.push({ key, value: null }) } // delete only a key that exists
    else if (!had || original[key] !== value) out.push({ key, value }) // add or real change
  }
  return out
}

export const isDirty = (b: Buffer, original: Original = {}): boolean => effective(b, original).length > 0

export function summarize(b: Buffer, original: Original) {
  let added = 0, changed = 0, removed = 0
  for (const e of effective(b, original)) {
    if (e.value === null) removed++
    else if (e.key in original) changed++
    else added++
  }
  return { added, changed, removed }
}

export function toChanges(b: Buffer, original: Original): SecretChange[] {
  return effective(b, original).map((e) => (e.value === null ? { key: e.key, delete: true } : { key: e.key, value: e.value }))
}
