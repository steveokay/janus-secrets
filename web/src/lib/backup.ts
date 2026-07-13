import { ApiError } from './api'

// Streams GET /v1/sys/backup as a blob and triggers a download. The dump is
// encrypted/sealed material only — no plaintext secrets — but still sensitive:
// we never cache it and revoke the object URL right after the click.
export async function downloadBackup(): Promise<void> {
  const res = await fetch('/v1/sys/backup', { credentials: 'include' })
  if (!res.ok) {
    const text = await res.text().catch(() => '')
    let code = 'error', message = res.statusText
    try { const e = (text ? JSON.parse(text) : undefined)?.error; if (e) { code = e.code ?? code; message = e.message ?? message } } catch { /* non-JSON */ }
    throw new ApiError(res.status, code, message)
  }
  const blob = await res.blob()
  const cd = res.headers.get('Content-Disposition') ?? ''
  const name = /filename="?([^"]+)"?/.exec(cd)?.[1] ?? 'janus-backup.jsonl'
  const url = URL.createObjectURL(blob)
  try {
    const a = document.createElement('a')
    a.href = url; a.download = name
    document.body.appendChild(a); a.click(); a.remove()
  } finally {
    URL.revokeObjectURL(url)
  }
}
