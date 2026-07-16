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

// --- Promotion requests (approval workflow) ---

export type PromotionRequestStatus = 'pending' | 'applied' | 'rejected' | 'cancelled'

export interface CreateRequestBody {
  from_config: string
  to_config?: string
  to_env?: string
  to_name?: string
  create?: boolean
  source_version: number
  note: string
  selections: Selection[]
}

// Value-free diff entries for a request: key + status + locked only, never a value.
export interface RequestDiffEntry {
  key: string
  status: PromoteStatus
  locked: boolean
}

export interface RequestDiff {
  source_version: number
  target_exists: boolean
  entries: RequestDiffEntry[]
}

export interface PromotionRequest {
  id: string
  project_id: string
  source_config_id: string
  source_version: number
  target_env_id: string
  target_config_id?: string
  target_name: string
  create_target: boolean
  keys: string[]
  selections: Selection[]
  note: string
  status: PromotionRequestStatus
  requested_by: string
  decided_by?: string
  applied_target_version?: number
  created_at: string
}

export interface PromotionRequestDetail extends PromotionRequest {
  diff?: RequestDiff
}

export interface ApproveRequestResult {
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
  requests: {
    create: (body: CreateRequestBody) => api.post<{ id: string; status: 'pending' }>('/v1/promote/requests', body),
    list: (params: { project: string; status?: string; mine?: boolean }) => {
      const q = new URLSearchParams({ project: params.project })
      if (params.status) q.set('status', params.status)
      if (params.mine) q.set('mine', 'true')
      return api
        .get<{ requests: PromotionRequest[] }>(`/v1/promote/requests?${q.toString()}`)
        .then((r) => r.requests ?? [])
    },
    get: (id: string) => api.get<PromotionRequestDetail>(`/v1/promote/requests/${id}`),
    approve: (id: string) => api.post<ApproveRequestResult>(`/v1/promote/requests/${id}/approve`),
    reject: (id: string, note: string) =>
      api.post<{ status: 'rejected' }>(`/v1/promote/requests/${id}/reject`, { note }),
    cancel: (id: string) => api.post<{ status: 'cancelled' }>(`/v1/promote/requests/${id}/cancel`),
  },
}
