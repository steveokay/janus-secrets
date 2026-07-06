import { api } from './api'

export interface SealStatus {
  initialized: boolean
  sealed: boolean
  type: 'shamir' | 'awskms'
  threshold?: number
  shares?: number
  progress?: number
}
export interface User { email: string }
export interface Project { id: string; slug: string; name: string }
export interface Environment { id: string; slug: string; name: string }
export interface Config { id: string; environment_id: string; name: string; inherits_from: string | null; created_at: string }
export interface MaskedSecret { value_version: number; created_at: string; origin: 'own' | 'inherited' | 'overridden' }
export interface SecretChange { key: string; value?: string; delete?: boolean }
export interface VersionResult { version: number; id: string; created_at: string }

export const endpoints = {
  // sys / auth
  sealStatus: () => api.get<SealStatus>('/v1/sys/seal-status'),
  unsealShare: (share: string) => api.post<SealStatus>('/v1/sys/unseal', { share }),
  unsealKms: () => api.post<SealStatus>('/v1/sys/unseal', {}),
  unsealReset: () => api.post<SealStatus>('/v1/sys/unseal/reset'),
  me: () => api.get<User>('/v1/auth/me'),
  login: (email: string, password: string) => api.post<User>('/v1/auth/login', { email, password }),
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
}
