import { api } from './api'

export interface SealProgress { submitted: number; required: number }
export interface SealStatus {
  initialized: boolean
  sealed: boolean
  type: 'shamir' | 'awskms'
  threshold?: number
  shares?: number
  progress?: SealProgress
}
// /v1/auth/me principal: for users, `name` is the email address.
export interface User { kind: 'user' | 'service_token'; id: string; name: string }
export interface Project { id: string; slug: string; name: string }
export interface Environment { id: string; slug: string; name: string }
export interface Config { id: string; environment_id: string; name: string; inherits_from: string | null; created_at: string }
export interface MaskedSecret { value_version: number; created_at: string; origin: 'own' | 'inherited' | 'overridden' }
export interface SecretChange { key: string; value?: string; delete?: boolean }
export interface VersionResult { version: number; id: string; created_at: string }
export interface VersionMeta { version: number; message: string; created_by: string; created_at: string }
export interface VersionDiff { a: number; b: number; added: string[]; changed: string[]; removed: string[] }
export interface AuditEvent {
  seq: number
  occurred_at: string
  actor_kind: string
  actor_id: string | null
  actor_name: string
  action: string
  resource: string
  detail: string | null
  result: 'success' | 'denied' | 'error'
  result_code: string | null
  ip: string
  prev_hash: string
  hash: string
}
export interface VerifyResult {
  valid: boolean
  count: number
  head_seq: number
  head_hash?: string
  broken_at_seq?: number
  reason?: 'hash_mismatch' | 'chain_break'
}
export interface AuditEventFilters {
  from?: string; to?: string; actor?: string; action?: string; result?: string
}

function auditParams(f: AuditEventFilters & { cursor?: number; limit?: number; format?: string }): string {
  const q = new URLSearchParams()
  for (const [k, v] of Object.entries(f)) {
    if (v !== undefined && v !== '') q.set(k, String(v))
  }
  return q.toString()
}

export const endpoints = {
  // sys / auth
  sealStatus: () => api.get<SealStatus>('/v1/sys/seal-status'),
  unsealShare: (share: string) => api.post<SealStatus>('/v1/sys/unseal', { share }),
  unsealKms: () => api.post<SealStatus>('/v1/sys/unseal', {}),
  unsealReset: () => api.post<SealStatus>('/v1/sys/unseal/reset'),
  me: () => api.get<User>('/v1/auth/me'),
  login: (email: string, password: string) =>
    api.post<{ user: { id: string; email: string } }>('/v1/auth/login', { email, password }),
  logout: () => api.post<void>('/v1/auth/logout'),
  changePassword: (current_password: string, new_password: string) =>
    api.post<void>('/v1/auth/password', { current_password, new_password }),

  // structure
  listProjects: () => api.get<{ projects: Project[] }>('/v1/projects').then((r) => r.projects),
  createProject: (slug: string, name: string) => api.post<Project>('/v1/projects', { slug, name }),
  listEnvironments: (pid: string) =>
    api.get<{ environments: Environment[] }>(`/v1/projects/${pid}/environments`).then((r) => r.environments),
  createEnvironment: (pid: string, slug: string, name: string) =>
    api.post<Environment>(`/v1/projects/${pid}/environments`, { slug, name }),
  listConfigs: (pid: string, eid: string) =>
    api.get<{ configs: Config[] }>(`/v1/projects/${pid}/environments/${eid}/configs`).then((r) => r.configs),
  createConfig: (pid: string, eid: string, name: string, inherits_from?: string) =>
    api.post<Config>(`/v1/projects/${pid}/environments/${eid}/configs`, { name, inherits_from }),

  // secrets
  maskedSecrets: (cid: string) =>
    api.get<{ secrets: Record<string, MaskedSecret> }>(`/v1/configs/${cid}/secrets`).then((r) => r.secrets),
  revealKey: (cid: string, key: string) =>
    api.get<{ key: string; value: string }>(`/v1/configs/${cid}/secrets/${encodeURIComponent(key)}`),
  revealAll: (cid: string) =>
    api.get<{ version: number; secrets: Record<string, string> }>(`/v1/configs/${cid}/secrets?reveal=true`),
  // The config's own stored values verbatim (unresolved), plus the config
  // version — the editable truth the secret editor diffs against.
  rawConfig: (cid: string) =>
    api.get<{ version: number; secrets: Record<string, string> }>(`/v1/configs/${cid}/secrets?reveal=true&raw=true`),
  saveSecrets: (cid: string, changes: SecretChange[], message: string) =>
    api.put<VersionResult>(`/v1/configs/${cid}/secrets`, { message, changes }),

  // versions (B2): reads are config:read and NOT audited; diff is key NAMES only.
  listVersions: (cid: string) =>
    api.get<{ versions: VersionMeta[] }>(`/v1/configs/${cid}/versions`).then((r) => r.versions),
  diffVersions: (cid: string, a: number, b: number) =>
    api.get<VersionDiff>(`/v1/configs/${cid}/versions/diff?a=${a}&b=${b}`),
  rollback: (cid: string, target_version: number, message: string) =>
    api.post<VersionResult>(`/v1/configs/${cid}/rollback`, { target_version, message }),

  // audit (B3): events/verify reads are NOT audited server-side; export IS.
  verifyAudit: () => api.get<VerifyResult>('/v1/audit/verify'),
  listAuditEvents: (f: AuditEventFilters & { cursor?: number; limit?: number }) =>
    api.get<{ events: AuditEvent[]; next_cursor: number | null }>(`/v1/audit/events?${auditParams(f)}`),
  auditExportUrl: (f: AuditEventFilters, format: 'jsonl' | 'csv') =>
    `/v1/audit/export?${auditParams({ ...f, format })}`,
}
