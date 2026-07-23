/* Thin typed fetch client for the Janus /v1 API. Same-origin, cookie session.
   Errors parse the server's {error:{code,message}} envelope. */

export class ApiError extends Error {
  constructor(
    readonly status: number,
    readonly code: string,
    message: string,
  ) {
    super(message)
    this.name = 'ApiError'
  }
}

async function request<T>(method: string, path: string, body?: unknown): Promise<T> {
  const res = await fetch(path, {
    method,
    credentials: 'include',
    headers: body === undefined ? undefined : { 'Content-Type': 'application/json' },
    body: body === undefined ? undefined : JSON.stringify(body),
  })
  if (res.status === 204) return undefined as T
  const text = await res.text()
  const data = text ? JSON.parse(text) : undefined
  if (!res.ok) {
    const e = (data as { error?: { code?: string; message?: string } } | undefined)?.error
    throw new ApiError(res.status, e?.code ?? 'error', e?.message ?? res.statusText)
  }
  return data as T
}

const get = <T>(path: string) => request<T>('GET', path)
const post = <T>(path: string, body?: unknown) => request<T>('POST', path, body)
const put = <T>(path: string, body?: unknown) => request<T>('PUT', path, body)
const patch = <T>(path: string, body?: unknown) => request<T>('PATCH', path, body)
const del = <T>(path: string) => request<T>('DELETE', path)

/** Streams GET /v1/sys/backup and triggers a download. Sealed material only —
    no plaintext — but still sensitive: never cached, object URL revoked. */
export async function downloadBackup(): Promise<void> {
  const res = await fetch('/v1/sys/backup', { credentials: 'include' })
  if (!res.ok) throw new ApiError(res.status, 'error', res.statusText)
  const blob = await res.blob()
  const cd = res.headers.get('Content-Disposition') ?? ''
  const name = /filename="?([^";]+)"?/.exec(cd)?.[1] ?? 'janus-backup.jsonl'
  const url = URL.createObjectURL(blob)
  try {
    const a = document.createElement('a')
    a.href = url
    a.download = name
    document.body.appendChild(a)
    a.click()
    a.remove()
  } finally {
    URL.revokeObjectURL(url)
  }
}

/** Safe user-facing message: pass curated 403/409 text through, collapse the rest. */
export function errorMessage(e: unknown, fallback = 'Request failed.'): string {
  if (e instanceof ApiError) {
    if (e.status === 403 || e.status === 409) return e.message || fallback
    if (e.code === 'validation') return 'Please check your input.'
    if (e.code === 'account_locked') return e.message || 'This account is temporarily locked — try again later.'
    if (e.code === 'rate_limited') return 'Too many attempts — try again shortly.'
    if (e.code === 'sealed') return 'The server is sealed.'
  }
  return fallback
}

/* ── API shapes (mirror internal/api) ─────────────────────────── */

export type SealTypeName = 'shamir' | 'awskms' | 'gcpkms' | 'azurekv'

/** Human-readable label for an auto-unseal seal type (any provider). */
export function sealTypeLabel(type: string): string {
  switch (type) {
    case 'awskms': return 'AWS KMS'
    case 'gcpkms': return 'GCP KMS'
    case 'azurekv': return 'Azure Key Vault'
    case 'shamir': return 'Shamir'
    default: return type
  }
}
export interface SealStatus {
  initialized: boolean
  sealed: boolean
  type: SealTypeName
  threshold?: number
  shares?: number
  progress?: { submitted: number; required: number }
}
export interface Me { kind: 'user' | 'service_token'; id: string; name: string }
export interface ApiProject { id: string; slug: string; name: string; created_at?: string; last_activity_at?: string | null }
export interface ApiEnvironment { id: string; slug: string; name: string; created_at?: string; last_activity_at?: string | null }
export interface ApiConfig { id: string; environment_id: string; name: string; inherits_from: string | null; created_at: string }
export interface MaskedSecret { value_version: number; created_at: string; origin: 'own' | 'inherited' | 'overridden'; type?: string; max_age_seconds?: number; stale?: boolean; last_read_at?: string | null; unused?: boolean }
export interface MaxAgePolicy { key: string; max_age_seconds: number }
export interface SecretChange { key: string; value?: string; delete?: boolean }
export interface VersionMeta { version: number; message: string; created_by: string; created_at: string }
export interface VersionDiff { a: number; b: number; added: string[]; changed: string[]; removed: string[] }
export interface ApiAuditEvent {
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
export interface VerifyResult { valid: boolean; count: number; head_seq: number; head_hash?: string }
export interface HistBucket { start: string; success: number; denied: number; error: number }
export interface Reads24h {
  reads_24h: number
  top_configs: Array<{ config_id: string; config_name: string; project_name?: string; reads: number }>
  top_tokens: Array<{ token_id: string; token_name: string; reads: number }>
}
export interface TokenMeta {
  id: string; name: string
  scope_kind: 'config' | 'environment' | 'transit'
  scope_id: string
  access: string
  created_by: string
  created_at: string
  expires_at?: string
  revoked_at?: string
  last_used_at?: string | null
}
export interface MintTokenResult {
  token: string; id: string; name: string
  scope: { kind: string; id: string }; access: string; expires_at: string | null
}
export interface UserInfo { id: string; email: string; disabled: boolean; locked: boolean; locked_until: string | null; last_login_at?: string | null }
export type Role = 'viewer' | 'developer' | 'admin' | 'owner'
export interface ApiMember { user_id: string; role: Role }
export interface ApiTransitKey {
  name: string
  type: 'aes256-gcm' | 'ed25519'
  latest_version: number
  min_decryption_version: number
  deletion_allowed: boolean
  versions: readonly number[]
}
export interface RotationPolicy {
  id: string; project_id: string; config_id: string; secret_key: string
  type: string; interval_seconds: number; status: string
  failure_count: number; last_error?: string
  next_rotation_at: string; last_rotated_at?: string; created_at: string
}
export interface SyncTargetApi {
  id: string; project_id: string; config_id: string; provider: string
  interval_seconds: number; status: string; failure_count: number; last_error?: string
  next_sync_at: string; last_synced_at?: string
  addr: {
    owner?: string; repo?: string; environment?: string; namespace?: string; secret_name?: string
    gitlab_url?: string; project?: string; environment_scope?: string
    region?: string; path_prefix?: string
    account_id?: string; script_name?: string
  }
}
export interface DynamicRole {
  id: string; project_id: string; config_id: string; name: string
  default_ttl_seconds: number; max_ttl_seconds: number; created_at: string
}
/* run history — value-free: timing, status, sanitized error, resulting version */
export interface RunView {
  id: number; started_at: string; ended_at: string
  status: 'success' | 'failure'; error?: string
  config_version?: number; attempt_num: number; keys_count?: number
}
/* the ONLY dynamic response carrying plaintext — shown once, never cached */
export interface IssuedCreds { lease_id: string; username: string; password: string; expires_at: string }
export interface RotationCreateInput {
  config_id: string; secret_key: string; type: 'postgres' | 'webhook'; interval_seconds: number
  config: {
    admin_dsn?: string; role?: string; password_len?: number
    url?: string; hmac_key?: string; notify_url?: string; notify_hmac_key?: string
  }
}
export type SyncProvider = 'github' | 'k8s' | 'gitlab' | 'aws_ssm' | 'cloudflare' | 'aws_secrets'
export interface SyncCreateInput {
  config_id: string; provider: SyncProvider; prune?: boolean; interval_seconds: number
  addr: {
    owner?: string; repo?: string; environment?: string; namespace?: string; secret_name?: string
    gitlab_url?: string; project?: string; environment_scope?: string
    region?: string; path_prefix?: string
    account_id?: string; script_name?: string
  }
  creds: {
    pat?: string; api_url?: string; ca_cert?: string; token?: string
    access_key_id?: string; secret_access_key?: string; session_token?: string
    api_token?: string
  }
}
export interface DynamicRoleCreateInput {
  config_id: string; name: string; default_ttl_seconds: number; max_ttl_seconds: number
  config: { admin_dsn?: string; creation_statements?: string; revocation_statements?: string; renew_statements?: string }
}
export interface ApiLease {
  id: string; role_id: string; status: string; db_username: string
  expires_at: string; max_expires_at: string; renewed_at?: string; created_at: string
}
export interface OIDCLoginStatus { enabled: boolean; name?: string }
export interface VersionInfo { version: string; commit?: string }

/* operational health snapshot — value-free (GET /v1/sys/status, admin) */
export interface SchedulerHealth {
  enabled: boolean
  last_tick_age_seconds: number | null
  interval_seconds: number
}
export interface SysStatus {
  version: string
  commit: string
  uptime_seconds: number
  sealed: boolean
  seal_type: string
  db: {
    reachable: boolean
    latency_ms: number
    pool: { total: number; idle: number; acquired: number; max: number }
  }
  audit: { head_seq: number; event_count: number }
  schedulers: {
    rotation: SchedulerHealth
    sync: SchedulerHealth
    dynamic: SchedulerHealth
  }
  runs: { rotation_failed: number; sync_failed: number }
  leases: { active: number }
}
export interface InitResult { type: string; shares?: string[]; admin?: { email: string; password: string } }

/* trash — value-free metadata for soft-deleted entities */
export interface TrashProject { id: string; slug: string; name: string; deleted_at: string }
export interface TrashEnvironment { id: string; slug: string; name: string; project_id: string; project_name: string; deleted_at: string }
export interface TrashConfig { id: string; name: string; environment_id: string; environment_name: string; project_id: string; project_name: string; deleted_at: string }
export interface Trash { projects: TrashProject[]; environments: TrashEnvironment[]; configs: TrashConfig[] }

/* per-key value history */
export interface KeyVersionMeta { value_version: number; created_at: string }

/* Per-key advisory read insights (value-free: counts + timestamps only).
   last_read_at is the most recent per-key reveal (null = never read per-key);
   daily is window_days reveal counts, oldest bucket first (last element = today).
   Keys never revealed per-key are ABSENT from ReadInsights.keys. */
export interface KeyReadInsight { last_read_at: string | null; daily: number[] }
export interface ReadInsights { window_days: number; keys: Record<string, KeyReadInsight> }

/* global key-name search (metadata only — never a value) */
export interface KeySearchResult {
  key: string
  project_id: string
  project_name: string
  project_slug: string
  environment_id: string
  environment_slug: string
  config_id: string
  config_name: string
}

/* promotion — wire types mirror the Go handler JSON exactly */
export type PromoteStatus = 'add' | 'change' | 'remove' | 'same'
export interface DiffEntry { key: string; status: PromoteStatus; source_value: string; target_value: string; locked: boolean }
export interface PromoteDiff { source_version: number; target_exists: boolean; entries: DiffEntry[] }
export interface PromoteSelection { key: string; action: 'set' | 'remove' }
export interface PromoteApplyBody {
  from_config: string; to_config?: string; to_env?: string; to_name?: string
  create?: boolean; source_version: number; selections: PromoteSelection[]
}
export interface PromoteApplyResult { target_version: number; applied: string[]; skipped: string[] }

/* cross-config compare — value-free (booleans + key names + origins only) */
export type CompareOrigin = 'own' | 'inherited' | 'overridden' | ''
export interface CompareEntry {
  key: string
  in_a: boolean
  in_b: boolean
  differs: boolean
  origin_a: CompareOrigin
  origin_b: CompareOrigin
}
export interface CompareResult { config_a: string; config_b: string; entries: CompareEntry[] }
export type PromotionRequestStatus = 'pending' | 'applied' | 'rejected' | 'cancelled'
export interface RequestDiff { source_version: number; target_exists: boolean; entries: Array<{ key: string; status: PromoteStatus; locked: boolean }> }
export interface PromotionRequest {
  id: string; project_id: string; source_config_id: string; source_version: number
  target_env_id: string; target_config_id?: string; target_name: string; create_target: boolean
  keys: string[]; selections: PromoteSelection[]; note: string
  status: PromotionRequestStatus; requested_by: string; decided_by?: string
  applied_target_version?: number; created_at: string
}
export interface PromotionRequestDetail extends PromotionRequest { diff?: RequestDiff }

/* OIDC provider + CI federation (admin) */
export interface OIDCProviderView { name: string; issuer: string; client_id: string; scopes: string[]; redirect_url: string; enabled: boolean; secret_set: boolean }
export interface OIDCConfigInput { name: string; issuer: string; client_id: string; client_secret: string; scopes: string[]; redirect_url: string; enabled: boolean }
export interface FederationConfigView { issuer: string; audience: string; enabled: boolean }
export interface FederationBindingView {
  id: string; name: string; match_claims: Record<string, string>
  scope_kind: 'config' | 'environment'; scope_id: string
  access: 'read' | 'readwrite'; ttl_seconds: number; enabled: boolean
}
export type FederationBindingInput = Omit<FederationBindingView, 'id'>

/* master-key rotation */
export interface MasterKeyStatus {
  unseal_type: SealTypeName
  master_key_version: number
  rotated_at: string | null
  rekey_in_progress: boolean
  submitted: number
  required: number
}

export interface SessionInfo {
  id: string
  created_at: string
  last_seen_at: string
  expires_at: string
  ip: string
  user_agent: string
  current: boolean
}

export type NotificationEventKind = 'rotation.failed' | 'sync.failed' | 'promotion.pending' | 'access.denied' | 'breakglass.activated'
export type NotificationChannelType = 'webhook' | 'slack' | 'smtp'
export type SmtpTlsMode = 'starttls' | 'implicit' | 'none'

/* SMTP settings shared by create/update inputs. All optional; the password is
   write-only (send-only) and is NEVER returned by the API, so it never appears
   on NotificationChannel. */
export interface SmtpChannelFields {
  smtp_host?: string
  smtp_port?: number
  smtp_from?: string
  smtp_to?: string[]
  smtp_username?: string
  smtp_password?: string
  smtp_tls_mode?: SmtpTlsMode
  smtp_insecure_skip_verify?: boolean
}

export interface NotificationChannel {
  id: string
  name: string
  type: NotificationChannelType
  enabled: boolean
  events: NotificationEventKind[]
  created_by: string
  created_at: string
  updated_at: string
  /* non-secret SMTP settings echoed back by the API (never the password) */
  smtp_host?: string
  smtp_port?: number
  smtp_from?: string
  smtp_to?: string[]
  smtp_username?: string
  smtp_tls_mode?: SmtpTlsMode
  smtp_insecure_skip_verify?: boolean
}

export interface NotificationDelivery {
  id: string
  event_kind: string
  status: 'pending' | 'delivered' | 'failed'
  attempts: number
  next_attempt_at: string
  last_error?: string
  created_at: string
  delivered_at?: string
}

export interface CreateChannelInput extends SmtpChannelFields {
  name: string
  type: NotificationChannelType
  url: string
  hmac_key?: string
  events: NotificationEventKind[]
}

export interface UpdateChannelInput extends SmtpChannelFields {
  enabled?: boolean
  events?: NotificationEventKind[]
  url?: string
  hmac_key?: string
}

/* Break-glass: guarded, time-boxed emergency role elevation. Value-safe: the
   reason is operator-entered justification text, never a secret. */
export type BreakGlassScopeLevel = 'instance' | 'project' | 'environment'

export interface BreakGlassGrant {
  id: string
  user_id: string
  scope_level: BreakGlassScopeLevel
  project_id?: string
  environment_id?: string
  elevated_role: Role
  reason: string
  activated_at: string
  expires_at: string
  revoked_at?: string
}

export interface BreakGlassActivateInput {
  scope_level: BreakGlassScopeLevel
  project_id?: string
  environment_id?: string
  role: Role
  reason: string
  ttl?: string
}

/* ── endpoints ────────────────────────────────────────────────── */

export const api = {
  // sys / auth
  sealStatus: () => get<SealStatus>('/v1/sys/seal-status'),
  init: (shares: number, threshold: number, admin_email: string) =>
    post<InitResult>('/v1/sys/init', { shares, threshold, admin_email }),
  unsealShare: (share: string) => post<SealStatus>('/v1/sys/unseal', { share }),
  unsealKms: () => post<SealStatus>('/v1/sys/unseal', {}),
  seal: () => post<{ sealed: boolean }>('/v1/sys/seal'),
  version: () => get<VersionInfo>('/v1/sys/version'),
  sysStatus: () => get<SysStatus>('/v1/sys/status'),
  me: () => get<Me>('/v1/auth/me'),
  login: (email: string, password: string, totp_code?: string) =>
    post<{ user: { id: string; email: string } }>('/v1/auth/login', {
      email, password, ...(totp_code ? { totp_code } : {}),
    }),
  logout: () => post<void>('/v1/auth/logout'),
  oidcLoginStatus: () => get<OIDCLoginStatus>('/v1/auth/oidc/status'),

  // structure
  listProjects: () => get<{ projects: ApiProject[] }>('/v1/projects').then(r => r.projects),
  createProject: (slug: string, name: string) => post<ApiProject>('/v1/projects', { slug, name }),
  listEnvironments: (pid: string) =>
    get<{ environments: ApiEnvironment[] }>(`/v1/projects/${pid}/environments`).then(r => r.environments),
  createEnvironment: (pid: string, slug: string, name: string) =>
    post<ApiEnvironment>(`/v1/projects/${pid}/environments`, { slug, name }),
  listConfigs: (pid: string, eid: string) =>
    get<{ configs: ApiConfig[] }>(`/v1/projects/${pid}/environments/${eid}/configs`).then(r => r.configs),
  createConfig: (pid: string, eid: string, name: string, inherits_from?: string) =>
    post<ApiConfig>(`/v1/projects/${pid}/environments/${eid}/configs`, { name, inherits_from }),

  // secrets
  maskedSecrets: (cid: string) =>
    get<{ secrets: Record<string, MaskedSecret> }>(`/v1/configs/${cid}/secrets`).then(r => r.secrets),
  revealKey: (cid: string, key: string) =>
    get<{ key: string; value: string }>(`/v1/configs/${cid}/secrets/${encodeURIComponent(key)}?raw=true`),
  saveSecrets: (cid: string, changes: SecretChange[], message: string) =>
    put<{ version: number }>(`/v1/configs/${cid}/secrets`, { message, changes }),
  listVersions: (cid: string) =>
    get<{ versions: VersionMeta[] }>(`/v1/configs/${cid}/versions`).then(r => r.versions),
  diffVersions: (cid: string, a: number, b: number) =>
    get<VersionDiff>(`/v1/configs/${cid}/versions/diff?a=${a}&b=${b}`),
  rollback: (cid: string, target_version: number, message: string) =>
    post<{ version: number }>(`/v1/configs/${cid}/rollback`, { target_version, message }),

  // audit + metrics
  verifyAudit: () => get<VerifyResult>('/v1/audit/verify'),
  listAuditEvents: (params: Record<string, string | number>) => {
    const q = new URLSearchParams()
    for (const [k, v] of Object.entries(params)) if (v !== '' && v !== undefined) q.set(k, String(v))
    return get<{ events: ApiAuditEvent[]; next_cursor: number | null }>(`/v1/audit/events?${q}`)
  },
  auditExportUrl: (format: 'jsonl' | 'csv') => `/v1/audit/export?format=${format}`,
  auditHistogram: (bucket: 'hour' | 'day', from?: string) => {
    const q = new URLSearchParams({ bucket })
    if (from) q.set('from', from)
    return get<{ buckets: HistBucket[] }>(`/v1/audit/histogram?${q}`).then(r => r.buckets)
  },
  metricsReads24h: () => get<Reads24h>('/v1/metrics/reads-24h'),

  // global key-name search (metadata only; authz-filtered server-side)
  searchKeys: (q: string, limit?: number) =>
    get<{ results: KeySearchResult[]; truncated: boolean }>(
      `/v1/search/keys?q=${encodeURIComponent(q)}${limit ? `&limit=${limit}` : ''}`,
    ),

  // tokens / users / members
  listTokens: () => get<{ tokens: TokenMeta[] }>('/v1/tokens').then(r => r.tokens),
  mintToken: (req: { name: string; scope: { kind: string; id: string }; access: string; ttl_seconds?: number }) =>
    post<MintTokenResult>('/v1/tokens', req),
  revokeToken: (id: string) => del<void>(`/v1/tokens/${id}`),
  listUsers: () => get<{ users: UserInfo[] }>('/v1/users').then(r => r.users),
  createUser: (email: string) => post<{ id: string; email: string; password: string }>('/v1/users', { email }),
  unlockUser: (id: string) => post<void>(`/v1/users/${id}/unlock`),
  listInstanceMembers: () => get<{ members: ApiMember[] }>('/v1/instance/members').then(r => r.members),
  putInstanceMember: (uid: string, role: Role) => put<void>(`/v1/instance/members/${uid}`, { role }),

  // transit
  listTransitKeys: () => get<{ keys: ApiTransitKey[] }>('/v1/transit/keys').then(r => r.keys),
  createTransitKey: (name: string, type: string) => post<ApiTransitKey>('/v1/transit/keys', { name, type }),
  rotateTransitKey: (name: string) => post<ApiTransitKey>(`/v1/transit/keys/${encodeURIComponent(name)}/rotate`, {}),
  transitEncrypt: (name: string, plaintext: string) =>
    post<{ ciphertext: string }>(`/v1/transit/encrypt/${encodeURIComponent(name)}`, { plaintext: btoa(plaintext) }),
  transitSign: (name: string, input: string) =>
    post<{ signature: string }>(`/v1/transit/sign/${encodeURIComponent(name)}`, { input: btoa(input) }),

  // structure lifecycle
  renameProject: (pid: string, name: string) => request<ApiProject>('PATCH', `/v1/projects/${pid}`, { name }),
  deleteProject: (pid: string) => del<void>(`/v1/projects/${pid}`),
  restoreProject: (pid: string) => post<ApiProject>(`/v1/projects/${pid}/restore`, {}),
  destroyProject: (pid: string) => del<void>(`/v1/projects/${pid}?destroy=true`),
  renameEnvironment: (pid: string, eid: string, name: string) =>
    request<ApiEnvironment>('PATCH', `/v1/projects/${pid}/environments/${eid}`, { name }),
  cloneEnvironment: (pid: string, eid: string, slug: string, name: string) =>
    post<ApiEnvironment>(`/v1/projects/${pid}/environments/${eid}/clone`, { slug, name }),
  deleteEnvironment: (pid: string, eid: string) => del<void>(`/v1/projects/${pid}/environments/${eid}`),
  restoreEnvironment: (pid: string, eid: string) => post<ApiEnvironment>(`/v1/projects/${pid}/environments/${eid}/restore`, {}),
  destroyEnvironment: (pid: string, eid: string) => del<void>(`/v1/projects/${pid}/environments/${eid}?destroy=true`),
  deleteConfig: (cid: string) => del<void>(`/v1/configs/${cid}`),
  restoreConfig: (cid: string) => post<ApiConfig>(`/v1/configs/${cid}/restore`, {}),
  destroyConfig: (cid: string) => del<void>(`/v1/configs/${cid}?destroy=true`),
  listTrash: () => get<Trash>('/v1/trash'),

  // per-key history — list is value-free; revealing one version IS audited
  keyHistory: (cid: string, key: string) =>
    get<{ key: string; history: KeyVersionMeta[] }>(`/v1/configs/${cid}/secrets/${encodeURIComponent(key)}/history`),
  revealKeyVersion: (cid: string, key: string, version: number) =>
    get<{ key: string; value: string; value_version: number }>(
      `/v1/configs/${cid}/secrets/${encodeURIComponent(key)}?version=${version}`),
  // per-key advisory read insights (value-free: last-read + 30-day sparkline; not audited)
  readInsights: (cid: string) => get<ReadInsights>(`/v1/configs/${cid}/read-insights`),

  // promotion
  getPipeline: (pid: string) => get<{ environment_ids: string[] }>(`/v1/projects/${pid}/pipeline`),
  setPipeline: (pid: string, ids: string[]) =>
    put<{ environment_ids: string[] }>(`/v1/projects/${pid}/pipeline`, { environment_ids: ids }),
  // advisory max-age policy (never blocks anything)
  listMaxAge: (cid: string) =>
    get<{ policies: MaxAgePolicy[] }>(`/v1/configs/${cid}/max-age`).then(r => r.policies ?? []),
  setConfigMaxAge: (cid: string, seconds: number | null) =>
    put<{ max_age_seconds: number | null }>(`/v1/configs/${cid}/max-age`, { max_age_seconds: seconds }),
  setKeyMaxAge: (cid: string, key: string, seconds: number | null) =>
    put<{ key: string; max_age_seconds: number | null }>(
      `/v1/configs/${cid}/secrets/${encodeURIComponent(key)}/max-age`, { max_age_seconds: seconds }),

  listLockedKeys: (cid: string) => get<{ keys: string[] }>(`/v1/configs/${cid}/locked-keys`).then(r => r.keys ?? []),
  lockKey: (cid: string, key: string) => post<{ key: string; locked: boolean }>(`/v1/configs/${cid}/locked-keys`, { key }),
  unlockKey: (cid: string, key: string) => del<{ key: string; locked: boolean }>(`/v1/configs/${cid}/locked-keys/${encodeURIComponent(key)}`),
  promotePreview: (from: string, target: { to?: string; to_env?: string }) => {
    const q = new URLSearchParams({ from })
    if (target.to) q.set('to', target.to)
    if (target.to_env) q.set('to_env', target.to_env)
    return get<PromoteDiff>(`/v1/promote/preview?${q}`)
  },
  promoteApply: (body: PromoteApplyBody) => post<PromoteApplyResult>('/v1/promote', body),
  // cross-config value-free compare (secret:read on BOTH; never returns values)
  compareConfigs: (cid: string, against: string) =>
    get<CompareResult>(`/v1/configs/${cid}/compare?against=${against}`),
  createPromoteRequest: (body: PromoteApplyBody & { note: string }) =>
    post<{ id: string; status: 'pending' }>('/v1/promote/requests', body),
  listPromoteRequests: (project: string, status?: string) => {
    const q = new URLSearchParams({ project })
    if (status) q.set('status', status)
    return get<{ requests: PromotionRequest[] }>(`/v1/promote/requests?${q}`).then(r => r.requests ?? [])
  },
  getPromoteRequest: (id: string) => get<PromotionRequestDetail>(`/v1/promote/requests/${id}`),
  approvePromoteRequest: (id: string) => post<PromoteApplyResult>(`/v1/promote/requests/${id}/approve`),
  rejectPromoteRequest: (id: string, note: string) => post<{ status: string }>(`/v1/promote/requests/${id}/reject`, { note }),
  cancelPromoteRequest: (id: string) => post<{ status: string }>(`/v1/promote/requests/${id}/cancel`),

  // OIDC provider + federation admin
  getOIDCConfig: () => get<OIDCProviderView>('/v1/sys/oidc'),
  setOIDCConfig: (cfg: OIDCConfigInput) => put<{ ok: boolean }>('/v1/sys/oidc', cfg),
  deleteOIDCConfig: () => del<void>('/v1/sys/oidc'),
  getFederationConfig: () => get<FederationConfigView>('/v1/sys/oidc/federation'),
  setFederationConfig: (cfg: FederationConfigView) => put<{ ok: boolean }>('/v1/sys/oidc/federation', cfg),
  deleteFederationConfig: () => del<void>('/v1/sys/oidc/federation'),
  listFederationBindings: () => get<FederationBindingView[]>('/v1/sys/oidc/federation/bindings'),
  createFederationBinding: (b: FederationBindingInput) => post<FederationBindingView>('/v1/sys/oidc/federation/bindings', b),
  deleteFederationBinding: (id: string) => del<void>(`/v1/sys/oidc/federation/bindings/${id}`),

  // account + master key + backup
  changePassword: (current_password: string, new_password: string) =>
    // Wire fields are {old, new} (see internal/api handlePasswordChange +
    // openapi.yaml). The friendlier parameter names are mapped here.
    post<void>('/v1/auth/password', { old: current_password, new: new_password }),
  listSessions: () => get<{ sessions: SessionInfo[] }>('/v1/auth/sessions').then(r => r.sessions),
  revokeSession: (id: string) => del<void>(`/v1/auth/sessions/${id}`),
  revokeOtherSessions: () => del<{ revoked: number }>('/v1/auth/sessions'),

  // two-factor (TOTP)
  totpStatus: () => get<{ enabled: boolean; recovery_remaining: number }>('/v1/auth/totp'),
  totpEnroll: () => post<{ secret: string; otpauth_url: string }>('/v1/auth/totp/enroll'),
  totpConfirm: (code: string) => post<{ recovery_codes: string[] }>('/v1/auth/totp/confirm', { code }),
  totpDisable: (code: string) => post<void>('/v1/auth/totp/disable', { code }),
  totpRegenerateRecovery: (code: string) => post<{ recovery_codes: string[] }>('/v1/auth/totp/recovery-codes', { code }),

  // notifications (alerting channels)
  listChannels: () => get<{ channels: NotificationChannel[] }>('/v1/notifications/channels').then(r => r.channels),
  createChannel: (input: CreateChannelInput) => post<NotificationChannel>('/v1/notifications/channels', input),
  updateChannel: (id: string, input: UpdateChannelInput) => patch<NotificationChannel>(`/v1/notifications/channels/${id}`, input),
  deleteChannel: (id: string) => del<void>(`/v1/notifications/channels/${id}`),
  testChannel: (id: string) => post<{ delivered: boolean }>(`/v1/notifications/channels/${id}/test`),
  listDeliveries: (id: string) => get<{ deliveries: NotificationDelivery[] }>(`/v1/notifications/channels/${id}/deliveries`).then(r => r.deliveries),

  // break-glass (guarded, time-boxed emergency role elevation)
  listBreakGlass: () => get<{ grants: BreakGlassGrant[] }>('/v1/break-glass').then(r => r.grants ?? []),
  activateBreakGlass: (input: BreakGlassActivateInput) => post<BreakGlassGrant>('/v1/break-glass', input),
  revokeBreakGlass: (id: string) => del<void>(`/v1/break-glass/${id}`),

  masterKeyStatus: () => get<MasterKeyStatus>('/v1/sys/master-key'),
  rotateMasterKey: () => post<{ master_key_version: number }>('/v1/sys/master-key/rotate', {}),
  rekeyInit: () => post<{ nonce: string; required: number; submitted: number }>('/v1/sys/master-key/rekey/init', {}),
  rekeySubmit: (nonce: string, share: string) =>
    post<{ complete: boolean; submitted?: number; required?: number; master_key_version?: number; new_shares?: string[] }>(
      '/v1/sys/master-key/rekey/submit', { nonce, share }),
  rekeyCancel: () => del('/v1/sys/master-key/rekey'),

  // operations — rotation (lists are per-project; see lib/ops.ts aggregators)
  listRotationPolicies: (projectId: string) =>
    get<{ policies: RotationPolicy[] }>(`/v1/rotation/policies?project_id=${encodeURIComponent(projectId)}`).then(r => r.policies ?? []),
  createRotationPolicy: (body: RotationCreateInput) => post<RotationPolicy>('/v1/rotation/policies', body),
  rotateNow: (id: string) => post<{ rotated: boolean }>(`/v1/rotation/policies/${id}/rotate`, {}),
  setRotationStatus: (id: string, status: 'active' | 'paused') =>
    request<RotationPolicy>('PATCH', `/v1/rotation/policies/${id}`, { status }),
  deleteRotationPolicy: (id: string) => del<void>(`/v1/rotation/policies/${id}`),
  rotationRuns: (id: string) =>
    get<{ runs: RunView[]; next_cursor: number | null }>(`/v1/rotation/policies/${id}/runs`).then(r => r.runs ?? []),

  // operations — sync
  listSyncTargets: (projectId: string) =>
    get<{ targets: SyncTargetApi[] }>(`/v1/sync/targets?project_id=${encodeURIComponent(projectId)}`).then(r => r.targets ?? []),
  createSyncTarget: (body: SyncCreateInput) => post<SyncTargetApi>('/v1/sync/targets', body),
  syncNow: (id: string) => post<unknown>(`/v1/sync/targets/${id}/sync`, {}),
  setSyncStatus: (id: string, status: 'active' | 'paused') =>
    request<SyncTargetApi>('PATCH', `/v1/sync/targets/${id}`, { status }),
  deleteSyncTarget: (id: string) => del<void>(`/v1/sync/targets/${id}`),
  syncRuns: (id: string) =>
    get<{ runs: RunView[]; next_cursor: number | null }>(`/v1/sync/targets/${id}/runs`).then(r => r.runs ?? []),

  // operations — dynamic
  listDynamicRoles: (configId: string) =>
    get<{ roles: DynamicRole[] }>(`/v1/dynamic/roles?config_id=${encodeURIComponent(configId)}`).then(r => r.roles ?? []),
  createDynamicRole: (body: DynamicRoleCreateInput) => post<DynamicRole>('/v1/dynamic/roles', body),
  deleteDynamicRole: (id: string) => del<void>(`/v1/dynamic/roles/${id}`),
  issueCreds: (roleId: string) => post<IssuedCreds>(`/v1/dynamic/roles/${roleId}/creds`, {}),
  listLeases: (roleId: string) =>
    get<{ leases: ApiLease[] }>(`/v1/dynamic/leases?role_id=${encodeURIComponent(roleId)}`).then(r => r.leases ?? []),
  renewLease: (id: string) => post<ApiLease>(`/v1/dynamic/leases/${id}/renew`, {}),
  revokeLease: (id: string) => post<{ revoked: boolean }>(`/v1/dynamic/leases/${id}/revoke`, {}),

  // scoped members (instance / project / environment)
  listScopedMembers: (path: string) => get<{ members: ApiMember[] }>(path).then(r => r.members),
  putScopedMember: (path: string, uid: string, role: Role) => put<void>(`${path}/${uid}`, { role }),
  deleteScopedMember: (path: string, uid: string) => del<void>(`${path}/${uid}`),
}

export function memberScopePath(s: { kind: 'instance' } | { kind: 'project'; pid: string } | { kind: 'environment'; pid: string; eid: string }): string {
  switch (s.kind) {
    case 'instance': return '/v1/instance/members'
    case 'project': return `/v1/projects/${s.pid}/members`
    case 'environment': return `/v1/projects/${s.pid}/environments/${s.eid}/members`
  }
}
