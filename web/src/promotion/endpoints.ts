import { api } from '../lib/api'

// Wire types mirror the Go promotion handler JSON EXACTLY (snake_case).
// Mock-drift (types diverging from the server shapes) is a known past bug class,
// so these must not be camelCased or reshaped.

export type PromoteStatus = 'add' | 'change' | 'remove' | 'same'

export interface DiffEntry {
  key: string
  status: PromoteStatus
  source_value: string
  target_value: string
  locked: boolean
}

export interface PromoteDiff {
  source_version: number
  target_exists: boolean
  entries: DiffEntry[]
}

export interface Selection {
  key: string
  action: 'set' | 'remove'
}

export interface ApplyBody {
  from_config: string
  to_config?: string
  to_env?: string
  to_name?: string
  create?: boolean
  source_version: number
  selections: Selection[]
}

export interface ApplyResult {
  target_version: number
  applied: string[]
  skipped: string[]
}

export const promotion = {
  pipeline: {
    get: (pid: string) => api.get<{ environment_ids: string[] }>(`/v1/projects/${pid}/pipeline`),
    set: (pid: string, ids: string[]) =>
      api.put<{ environment_ids: string[] }>(`/v1/projects/${pid}/pipeline`, { environment_ids: ids }),
  },
  locked: {
    list: (cid: string) => api.get<{ keys: string[] }>(`/v1/configs/${cid}/locked-keys`),
    lock: (cid: string, key: string) =>
      api.post<{ key: string; locked: boolean }>(`/v1/configs/${cid}/locked-keys`, { key }),
    unlock: (cid: string, key: string) =>
      api.del<{ key: string; locked: boolean }>(`/v1/configs/${cid}/locked-keys/${encodeURIComponent(key)}`),
  },
  preview: (from: string, to: string) =>
    api.get<PromoteDiff>(`/v1/promote/preview?from=${encodeURIComponent(from)}&to=${encodeURIComponent(to)}`),
  previewCreate: (from: string, toEnv: string) =>
    api.get<PromoteDiff>(`/v1/promote/preview?from=${encodeURIComponent(from)}&to_env=${encodeURIComponent(toEnv)}`),
  apply: (body: ApplyBody) => api.post<ApplyResult>(`/v1/promote`, body),
}
