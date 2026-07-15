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
export interface Config { id: string; environment_id: string; name: string; inherits_from: string | null; created_at: string; promoted_from_env?: string; promoted_from_version?: number }
// Trash (§1.10) — grouped soft-deleted entities. Value-free metadata only:
// names/slugs/ownership + when it was deleted; never any secret material.
export interface TrashProject { id: string; slug: string; name: string; deleted_at: string }
export interface TrashEnvironment {
  id: string; slug: string; name: string
  project_id: string; project_name: string; deleted_at: string
}
export interface TrashConfig {
  id: string; name: string
  environment_id: string; environment_name: string
  project_id: string; project_name: string; deleted_at: string
}
export interface Trash { projects: TrashProject[]; environments: TrashEnvironment[]; configs: TrashConfig[] }
export interface MaskedSecret { value_version: number; created_at: string; origin: 'own' | 'inherited' | 'overridden' }
export interface KeyVersionMeta { value_version: number; created_at: string }
export interface SecretChange { key: string; value?: string; delete?: boolean }
export interface VersionResult { version: number; id: string; created_at: string }
export interface VersionMeta { version: number; message: string; created_by: string; created_at: string; promoted_from_env?: string; promoted_from_version?: number }
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

// usage metrics (D) — on-demand read counts from audit_events (secret.reveal).
export interface ConfigReads { config_id: string; config_name: string; project_name?: string; reads: number }
export interface TokenReads { token_id: string; token_name: string; reads: number }
export interface Reads24h { reads_24h: number; top_configs: ConfigReads[]; top_tokens: TokenReads[] }

// tokens & users & members (B4)
export interface TokenMeta {
  id: string; name: string
  scope_kind: 'config' | 'environment' | 'transit'
  scope_id: string
  access: string
  created_by: string
  created_at: string
  expires_at?: string
  revoked_at?: string
}
export interface MintTokenRequest {
  name: string
  scope: { kind: 'config' | 'environment' | 'transit'; id: string }
  access: string
  ttl_seconds?: number
}
export interface MintTokenResult {
  token: string
  id: string
  name: string
  scope: { kind: 'config' | 'environment' | 'transit'; id: string }
  access: string
  expires_at: string | null
}
export interface UserInfo { id: string; email: string; disabled: boolean }
export type MemberRole = 'viewer' | 'developer' | 'admin' | 'owner'
export interface Member { user_id: string; role: MemberRole }
export type MemberScope =
  | { kind: 'instance' }
  | { kind: 'project'; pid: string }
  | { kind: 'environment'; pid: string; eid: string }

export function memberScopePath(s: MemberScope): string {
  switch (s.kind) {
    case 'instance': return '/v1/instance/members'
    case 'project': return `/v1/projects/${s.pid}/members`
    case 'environment': return `/v1/projects/${s.pid}/environments/${s.eid}/members`
  }
}

// transit (B5) — instance-scoped KMS. Key-mgmt is audited; crypto ops are not.
export type TransitKeyType = 'aes256-gcm' | 'ed25519'
export interface TransitKey {
  name: string
  type: TransitKeyType
  latest_version: number
  min_decryption_version: number
  deletion_allowed: boolean
  versions: readonly number[]
}
export interface TransitKeyConfig { min_decryption_version?: number; deletion_allowed?: boolean }

// OIDC provider (N5 T3) — instance-scoped admin config (needs OIDCManage).
// The client secret is WRITE-ONLY: the read view carries only `secret_set`,
// never the secret itself. PUT is a full replace and REQUIRES `client_secret`
// (empty → 400 validation), so it must be re-entered on every save.
export interface OIDCProviderView {
  name: string; issuer: string; client_id: string; scopes: string[]
  redirect_url: string; enabled: boolean; secret_set: boolean
}
export interface OIDCConfigInput {
  name: string; issuer: string; client_id: string; client_secret: string
  scopes: string[]; redirect_url: string; enabled: boolean
}

// CI federation (N5 T4) — instance-scoped admin config (needs OIDCManage).
// Federation config + trust bindings carry NO secret values; match_claims
// values are identity claims (repo names etc.), treated as metadata only.
export interface FederationConfigView { issuer: string; audience: string; enabled: boolean }
export interface FederationBindingView {
  id: string; name: string; match_claims: Record<string, string>
  scope_kind: 'config' | 'environment'; scope_id: string
  access: 'read' | 'readwrite'; ttl_seconds: number; enabled: boolean
}
export type FederationBindingInput = Omit<FederationBindingView, 'id'>

// Master-key rotation (owner-only). Status carries NO key material: only the
// unseal method, the current master-key version, the last-rotated timestamp, and
// (during a Shamir rekey) the in-progress share-submission progress.
export interface MasterKeyStatus {
  unseal_type: 'shamir' | 'awskms'
  master_key_version: number
  rotated_at: string | null
  rekey_in_progress: boolean
  submitted: number
  required: number
}

// OIDC login status (N5 T5) — unauthenticated, rate-limited probe that gates the
// "Sign in with SSO" button on the login page. Names-only; carries no secret.
export interface OIDCLoginStatus { enabled: boolean; name?: string }

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
  seal: () => api.post<{ sealed: boolean }>('/v1/sys/seal'),
  unsealShare: (share: string) => api.post<SealStatus>('/v1/sys/unseal', { share }),
  unsealKms: () => api.post<SealStatus>('/v1/sys/unseal', {}),
  unsealReset: () => api.post<SealStatus>('/v1/sys/unseal/reset'),
  me: () => api.get<User>('/v1/auth/me'),
  login: (email: string, password: string) =>
    api.post<{ user: { id: string; email: string } }>('/v1/auth/login', { email, password }),
  logout: () => api.post<void>('/v1/auth/logout'),
  // Unauthenticated: reports whether an OIDC provider is enabled (+ its name),
  // gating the login page's "Sign in with SSO" button. No secret in any shape.
  oidcLoginStatus: () => api.get<OIDCLoginStatus>('/v1/auth/oidc/status'),
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

  // trash & lifecycle (§1.10). Soft-delete moves to Trash; restore undeletes;
  // destroy is a permanent, cascading (project) hard delete. All value-free.
  listTrash: () => api.get<Trash>('/v1/trash'),
  deleteProject: (pid: string) => api.del<void>(`/v1/projects/${pid}`),
  restoreProject: (pid: string) => api.post<Project>(`/v1/projects/${pid}/restore`, {}),
  destroyProject: (pid: string) => api.del<void>(`/v1/projects/${pid}?destroy=true`),
  deleteEnvironment: (pid: string, eid: string) => api.del<void>(`/v1/projects/${pid}/environments/${eid}`),
  restoreEnvironment: (pid: string, eid: string) =>
    api.post<Environment>(`/v1/projects/${pid}/environments/${eid}/restore`, {}),
  destroyEnvironment: (pid: string, eid: string) =>
    api.del<void>(`/v1/projects/${pid}/environments/${eid}?destroy=true`),
  deleteConfig: (cid: string) => api.del<void>(`/v1/configs/${cid}`),
  restoreConfig: (cid: string) => api.post<Config>(`/v1/configs/${cid}/restore`, {}),
  destroyConfig: (cid: string) => api.del<void>(`/v1/configs/${cid}?destroy=true`),

  // secrets
  maskedSecrets: (cid: string) =>
    api.get<{ secrets: Record<string, MaskedSecret> }>(`/v1/configs/${cid}/secrets`).then((r) => r.secrets),
  // Raw (unresolved) single value for the editor — audited secret.reveal.
  revealKeyRaw: (cid: string, key: string) =>
    api.get<{ key: string; value: string }>(`/v1/configs/${cid}/secrets/${encodeURIComponent(key)}?raw=true`),
  // The config's own stored values verbatim (unresolved), plus the config
  // version — the editable truth the secret editor diffs against.
  rawConfig: (cid: string) =>
    api.get<{ version: number; secrets: Record<string, string> }>(`/v1/configs/${cid}/secrets?reveal=true&raw=true`),
  saveSecrets: (cid: string, changes: SecretChange[], message: string) =>
    api.put<VersionResult>(`/v1/configs/${cid}/secrets`, { message, changes }),
  // per-key value history (§1.11). List is value-free metadata (NOT audited);
  // revealKeyVersion reveals ONE historical value and IS audited (secret.reveal).
  keyHistory: (cid: string, key: string) =>
    api.get<{ key: string; history: KeyVersionMeta[] }>(`/v1/configs/${cid}/secrets/${encodeURIComponent(key)}/history`),
  revealKeyVersion: (cid: string, key: string, version: number) =>
    api.get<{ key: string; value: string; value_version: number }>(
      `/v1/configs/${cid}/secrets/${encodeURIComponent(key)}?version=${version}`),

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

  // usage metrics (D). Metadata reads (no secret values); NOT self-audited.
  metricsReads24h: () => api.get<Reads24h>('/v1/metrics/reads-24h'),
  projectMetricsReads24h: (pid: string) =>
    api.get<Reads24h>(`/v1/projects/${pid}/metrics/reads-24h`),

  // tokens & users & members (B4). Raw token / one-time password appear ONLY in
  // mint/create responses — never cached, logged, or shown twice.
  mintToken: (req: MintTokenRequest) => api.post<MintTokenResult>('/v1/tokens', req),
  listTokens: () => api.get<{ tokens: TokenMeta[] }>('/v1/tokens').then((r) => r.tokens),
  revokeToken: (id: string) => api.del<void>(`/v1/tokens/${id}`),
  createUser: (email: string) =>
    api.post<{ id: string; email: string; password: string }>('/v1/users', { email }),
  listUsers: () => api.get<{ users: UserInfo[] }>('/v1/users').then((r) => r.users),
  disableUser: (id: string) => api.post<void>(`/v1/users/${id}/disable`),
  listMembers: (s: MemberScope) => api.get<{ members: Member[] }>(memberScopePath(s)).then((r) => r.members),
  putMember: (s: MemberScope, uid: string, role: MemberRole) =>
    api.put<void>(`${memberScopePath(s)}/${uid}`, { role }),
  deleteMember: (s: MemberScope, uid: string) => api.del<void>(`${memberScopePath(s)}/${uid}`),

  // transit (B5). Crypto op responses are used in ephemeral component state only
  // (never cached); no decrypt/datakey here, so no plaintext ever returns.
  listTransitKeys: () => api.get<{ keys: TransitKey[] }>('/v1/transit/keys').then((r) => r.keys),
  getTransitKey: (name: string) => api.get<TransitKey>(`/v1/transit/keys/${encodeURIComponent(name)}`),
  createTransitKey: (name: string, type: TransitKeyType) =>
    api.post<TransitKey>('/v1/transit/keys', { name, type }),
  rotateTransitKey: (name: string) =>
    api.post<TransitKey>(`/v1/transit/keys/${encodeURIComponent(name)}/rotate`, {}),
  configTransitKey: (name: string, cfg: TransitKeyConfig) =>
    api.post<TransitKey>(`/v1/transit/keys/${encodeURIComponent(name)}/config`, cfg),
  trimTransitKey: (name: string, min_available_version: number) =>
    api.post<TransitKey>(`/v1/transit/keys/${encodeURIComponent(name)}/trim`, { min_available_version }),
  deleteTransitKey: (name: string) =>
    api.del<void>(`/v1/transit/keys/${encodeURIComponent(name)}`),
  transitEncrypt: (name: string, plaintext: string, associated_data?: string) =>
    api.post<{ ciphertext: string }>(`/v1/transit/encrypt/${encodeURIComponent(name)}`, { plaintext, associated_data }),
  transitRewrap: (name: string, ciphertext: string, associated_data?: string) =>
    api.post<{ ciphertext: string }>(`/v1/transit/rewrap/${encodeURIComponent(name)}`, { ciphertext, associated_data }),
  transitSign: (name: string, input: string) =>
    api.post<{ signature: string }>(`/v1/transit/sign/${encodeURIComponent(name)}`, { input }),
  transitVerify: (name: string, input: string, signature: string) =>
    api.post<{ valid: boolean }>(`/v1/transit/verify/${encodeURIComponent(name)}`, { input, signature }),

  // OIDC provider (N5 T3). getOIDCConfig NEVER returns the client secret
  // (only `secret_set`); setOIDCConfig is a full replace that requires it.
  getOIDCConfig: () => api.get<OIDCProviderView>('/v1/sys/oidc'),
  setOIDCConfig: (cfg: OIDCConfigInput) => api.put<{ ok: boolean }>('/v1/sys/oidc', cfg),
  deleteOIDCConfig: () => api.del<void>('/v1/sys/oidc'),

  // Master-key rotation (owner-only). Status is safe metadata; the rotate/rekey
  // mutations (wired in a later task) carry no key material in requests, and a
  // Shamir rekey returns fresh shares ONCE in the submit response.
  masterKeyStatus: () => api.get<MasterKeyStatus>('/v1/sys/master-key'),
  rotateMasterKey: () => api.post<{ master_key_version: number }>('/v1/sys/master-key/rotate', {}),
  rekeyInit: () => api.post<{ nonce: string; required: number; submitted: number }>('/v1/sys/master-key/rekey/init', {}),
  rekeySubmit: (nonce: string, share: string) =>
    api.post<{ complete: boolean; submitted?: number; required?: number; master_key_version?: number; new_shares?: string[] }>(
      '/v1/sys/master-key/rekey/submit', { nonce, share }),
  rekeyCancel: () => api.del('/v1/sys/master-key/rekey'),

  // CI federation (N5 T4). Config mirrors OIDC (200/404/403); no secret in any
  // shape. Server validates: match_claims.repository required; access enum;
  // ttl_seconds default 900 / cap 3600; scope_id must exist.
  getFederationConfig: () => api.get<FederationConfigView>('/v1/sys/oidc/federation'),
  setFederationConfig: (cfg: FederationConfigView) => api.put<{ ok: boolean }>('/v1/sys/oidc/federation', cfg),
  deleteFederationConfig: () => api.del<void>('/v1/sys/oidc/federation'),
  listFederationBindings: () => api.get<FederationBindingView[]>('/v1/sys/oidc/federation/bindings'),
  createFederationBinding: (b: FederationBindingInput) => api.post<FederationBindingView>('/v1/sys/oidc/federation/bindings', b),
  deleteFederationBinding: (id: string) => api.del<void>(`/v1/sys/oidc/federation/bindings/${id}`),
}
