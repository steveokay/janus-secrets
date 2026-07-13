import { api } from '../lib/api'

// --- Wire types (mirror the masked Go view shapes; NO secrets) ---

export interface RotationView {
  id: string
  project_id: string
  config_id: string
  secret_key: string
  type: 'postgres' | 'webhook'
  interval_seconds: number
  status: 'active' | 'paused' | 'failed'
  failure_count: number
  last_error?: string | null
  next_rotation_at: string
  last_rotated_at?: string | null
  last_config_version?: number | null
  created_at: string
}

export interface SyncAddr {
  owner?: string
  repo?: string
  environment?: string
  namespace?: string
  secret_name?: string
}

export interface SyncView {
  id: string
  project_id: string
  config_id: string
  provider: 'github' | 'k8s'
  prune: boolean
  interval_seconds: number
  addr: SyncAddr
  status: 'active' | 'paused' | 'failed'
  failure_count: number
  last_error?: string | null
  next_sync_at: string
  last_synced_at?: string | null
  managed_keys: string[]
  created_at: string
}

export interface DynamicRoleView {
  id: string
  project_id: string
  config_id: string
  name: string
  default_ttl_seconds: number
  max_ttl_seconds: number
  created_at: string
}

export interface DynamicLeaseView {
  id: string
  role_id: string
  status: 'creating' | 'active' | 'expired' | 'revoked' | 'revoke_failed'
  db_username: string
  expires_at: string
  max_expires_at: string
  renewed_at?: string | null
  created_at: string
}

// The ONLY response that carries a plaintext secret (shown once, never cached).
export interface IssuedCreds {
  lease_id: string
  username: string
  password: string
  expires_at: string
}

// --- Create request shapes (write-only; secrets nested + omitempty) ---

export interface RotationCreateInput {
  config_id: string
  secret_key: string
  type: 'postgres' | 'webhook'
  interval_seconds: number
  config: {
    admin_dsn?: string; role?: string; password_len?: number
    url?: string; hmac_key?: string
    notify_url?: string; notify_hmac_key?: string
  }
}

export const opsEndpoints = {
  rotation: {
    list: (pid: string) =>
      api.get<{ policies: RotationView[] }>(`/v1/rotation/policies?project_id=${encodeURIComponent(pid)}`).then((r) => r.policies ?? []),
    create: (body: RotationCreateInput) => api.post<RotationView>('/v1/rotation/policies', body),
    rotateNow: (id: string) => api.post<{ rotated: boolean; config_version: number }>(`/v1/rotation/policies/${id}/rotate`),
    setStatus: (id: string, status: 'active' | 'paused') => api.patch<RotationView>(`/v1/rotation/policies/${id}`, { status }),
    setInterval: (id: string, interval_seconds: number) => api.patch<RotationView>(`/v1/rotation/policies/${id}`, { interval_seconds }),
    remove: (id: string) => api.del<void>(`/v1/rotation/policies/${id}`),
  },
  sync: {
    list: (pid: string) =>
      api.get<{ targets: SyncView[] }>(`/v1/sync/targets?project_id=${encodeURIComponent(pid)}`).then((r) => r.targets ?? []),
    syncNow: (id: string) => api.post<{ synced: boolean }>(`/v1/sync/targets/${id}/sync`),
    setStatus: (id: string, status: 'active' | 'paused') => api.patch<SyncView>(`/v1/sync/targets/${id}`, { status }),
    setInterval: (id: string, interval_seconds: number) => api.patch<SyncView>(`/v1/sync/targets/${id}`, { interval_seconds }),
    remove: (id: string) => api.del<void>(`/v1/sync/targets/${id}`),
  },
  dynamic: {
    listRoles: (cid: string) =>
      api.get<{ roles: DynamicRoleView[] }>(`/v1/dynamic/roles?config_id=${encodeURIComponent(cid)}`).then((r) => r.roles ?? []),
    deleteRole: (id: string) => api.del<void>(`/v1/dynamic/roles/${id}`),
    issue: (roleId: string) => api.post<IssuedCreds>(`/v1/dynamic/roles/${roleId}/creds`),
    listLeases: (roleId: string) =>
      api.get<{ leases: DynamicLeaseView[] }>(`/v1/dynamic/leases?role_id=${encodeURIComponent(roleId)}`).then((r) => r.leases ?? []),
    renew: (leaseId: string) => api.post<DynamicLeaseView>(`/v1/dynamic/leases/${leaseId}/renew`),
    revoke: (leaseId: string) => api.post<{ revoked: boolean }>(`/v1/dynamic/leases/${leaseId}/revoke`),
  },
}
