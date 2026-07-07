// Thin typed fetch client. Every call is same-origin with the session cookie;
// failures parse the server's {error:{code,message}} envelope into ApiError.
export class ApiError extends Error {
  readonly name = 'ApiError'
  constructor(
    readonly status: number,
    readonly code: string,
    message: string,
  ) {
    super(message)
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
    const e = data?.error
    throw new ApiError(res.status, e?.code ?? 'error', e?.message ?? res.statusText)
  }
  return data as T
}

// Danger toast / inline error title for a failed mutation. Only surfaces the
// server's curated message for 403/409 (delegation ceiling, last-owner,
// self-guard etc.); anything else collapses to a generic failure so raw error
// internals never leak to the UI (no-leak posture).
export function apiErrorTitle(e: unknown): string {
  return e instanceof ApiError && (e.status === 403 || e.status === 409) ? e.message : 'Request failed.'
}

// Maps the API's {error:{code,message}} envelope to a friendly, safe message.
// 403/409 pass the server's curated message through (delegation ceiling,
// last-owner, self-guard, etc.); known codes get friendly text; everything
// else (incl. 5xx / non-ApiError) collapses to `fallback` so internals never leak.
const FRIENDLY: Record<string, string> = {
  validation: 'Please check your input.',
  not_found: 'Not found.',
  rate_limited: 'Too many attempts — try again shortly.',
}

export function errorMessage(e: unknown, fallback = 'Request failed.'): string {
  if (e instanceof ApiError) {
    // 403/409 carry the server's CURATED guardrail messages (delegation ceiling,
    // last-owner, self-guard, conflicts) — always surface those verbatim, before
    // any generic friendly mapping, so those precise messages are never lost.
    if (e.status === 403 || e.status === 409) return e.message || fallback
    if (FRIENDLY[e.code]) return FRIENDLY[e.code]
  }
  return fallback
}

export const api = {
  get: <T>(path: string) => request<T>('GET', path),
  post: <T>(path: string, body?: unknown) => request<T>('POST', path, body),
  put: <T>(path: string, body?: unknown) => request<T>('PUT', path, body),
  del: <T>(path: string) => request<T>('DELETE', path),
}
