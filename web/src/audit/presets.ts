import type { AuditEventFilters } from '../lib/endpoints'

const KEY = 'janus.audit.presets'
const hoursAgo = (h: number) => new Date(Date.now() - h * 3600_000).toISOString()

export interface Preset { name: string; filters: AuditEventFilters }
export interface BuiltinPreset { name: string; filters: () => AuditEventFilters }

export const BUILTIN_PRESETS: BuiltinPreset[] = [
  { name: 'Failures · 24h', filters: () => ({ result: 'error', from: hoursAgo(24) }) },
  { name: 'Denied · 24h', filters: () => ({ result: 'denied', from: hoursAgo(24) }) },
  { name: 'Last 7 days', filters: () => ({ from: hoursAgo(24 * 7) }) },
]

export function loadPresets(): Preset[] {
  try {
    const v = JSON.parse(localStorage.getItem(KEY) ?? '[]')
    return Array.isArray(v) ? (v as Preset[]) : []
  } catch {
    return []
  }
}
export function savePreset(name: string, filters: AuditEventFilters): void {
  const next = loadPresets().filter((p) => p.name !== name).concat({ name, filters })
  localStorage.setItem(KEY, JSON.stringify(next))
}
export function removePreset(name: string): void {
  localStorage.setItem(KEY, JSON.stringify(loadPresets().filter((p) => p.name !== name)))
}
