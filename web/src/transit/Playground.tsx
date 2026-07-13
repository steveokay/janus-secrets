import { FormEvent, ReactNode, useState } from 'react'
import { useMutation } from '@tanstack/react-query'
import { Copy } from 'lucide-react'
import { endpoints, TransitKey } from '../lib/endpoints'
import { apiErrorTitle } from '../lib/api'
import { Pill } from '../ui/Pill'
import { Button } from '../ui/Button'
import { FIELD } from '../ui/Input'
import { useToast } from '../ui/Toast'

// UTF-8 text → base64. Encrypt/sign inputs are typed text the UI encodes before
// sending; ciphertext/signature envelopes (janus:vN:…) are pasted verbatim.
const toB64 = (s: string) => btoa(String.fromCharCode(...new TextEncoder().encode(s)))

// Presentational field wrapper — label + control, token classes only.
function Field({ label, children }: { label: string; children: ReactNode }) {
  return (
    <label className="flex flex-col gap-1 text-[12px] font-semibold text-ink">
      {label}
      {children}
    </label>
  )
}

// Reuse the kit field token class (bg-surface-3, focus glow, transition) but
// keep monospace — these controls hold ciphertext / base64 / signatures.
const inputCls = `${FIELD} font-mono`

// Output envelope (ciphertext / signature). NOT secret — no plaintext or key
// material ever reaches here. Selectable mono block + guarded clipboard copy.
function OutBlock({ label, value }: { label: string; value: string }) {
  const toast = useToast()
  function copy() {
    const clipboard = navigator.clipboard
    if (!clipboard) {
      toast({ title: 'Copy failed', tone: 'danger' })
      return
    }
    clipboard.writeText(value).then(
      () => toast({ title: 'Copied' }),
      () => toast({ title: 'Copy failed', tone: 'danger' }),
    )
  }
  return (
    <div className="flex flex-col gap-1">
      <div className="flex items-center justify-between">
        <span className="text-[11px] font-semibold uppercase tracking-[.08em] text-ink-faint">{label}</span>
        <button
          type="button"
          onClick={copy}
          className="flex items-center gap-1 text-[11.5px] font-semibold text-ink-mute transition-nocturne hover:text-ink"
        >
          <Copy size={13} strokeWidth={1.7} /> Copy
        </button>
      </div>
      <div className="select-all break-all rounded border border-line bg-line-soft px-3 py-2 font-mono text-[12.5px] text-ink">
        {value}
      </div>
    </div>
  )
}

function OpCard({ title, hint, children }: { title: string; hint: string; children: ReactNode }) {
  return (
    <section className="flex flex-col gap-2.5 rounded-card border border-line bg-card p-4 shadow-elev-1">
      <div>
        <h4 className="text-[13.5px] font-semibold text-ink">{title}</h4>
        <p className="text-[11.5px] text-ink-faint">{hint}</p>
      </div>
      {children}
    </section>
  )
}

// --- AES-256-GCM ops ---------------------------------------------------------

function EncryptCard({ name }: { name: string }) {
  const [text, setText] = useState('')
  const [aad, setAad] = useState('')
  const [out, setOut] = useState<string | null>(null)
  const [error, setError] = useState('')

  // Crypto op: useMutation with NO query key — result lives in local state only,
  // never in the query cache, never logged.
  const m = useMutation({
    mutationFn: () => endpoints.transitEncrypt(name, toB64(text), aad ? toB64(aad) : undefined),
    onSuccess: (r) => setOut(r.ciphertext),
    onError: (e) => { setOut(null); setError(apiErrorTitle(e)) },
  })

  function submit(e: FormEvent) {
    e.preventDefault()
    setError('')
    m.mutate()
  }

  return (
    <OpCard title="Encrypt" hint="Text is UTF-8 → base64 encoded, then encrypted with the latest key version.">
      <form onSubmit={submit} className="flex flex-col gap-2.5">
        <Field label="Plaintext">
          <textarea
            aria-label="plaintext"
            value={text}
            onChange={(e) => setText(e.target.value)}
            rows={3}
            className={inputCls}
          />
        </Field>
        <Field label="Associated data (optional)">
          <input
            aria-label="associated data"
            value={aad}
            onChange={(e) => setAad(e.target.value)}
            className={inputCls}
          />
        </Field>
        <Button type="submit" size="sm" className="self-start" disabled={m.isPending}>Encrypt</Button>
        {error && <p role="alert" className="text-[12.5px] text-danger">{error}</p>}
        {out && <OutBlock label="Ciphertext" value={out} />}
      </form>
    </OpCard>
  )
}

function RewrapCard({ name }: { name: string }) {
  const [ct, setCt] = useState('')
  const [aad, setAad] = useState('')
  const [out, setOut] = useState<string | null>(null)
  const [error, setError] = useState('')

  const m = useMutation({
    mutationFn: () => endpoints.transitRewrap(name, ct, aad ? toB64(aad) : undefined),
    onSuccess: (r) => setOut(r.ciphertext),
    onError: (e) => { setOut(null); setError(apiErrorTitle(e)) },
  })

  function submit(e: FormEvent) {
    e.preventDefault()
    setError('')
    m.mutate()
  }

  return (
    <OpCard title="Rewrap" hint="Re-encrypt an existing ciphertext to the latest key version without exposing plaintext.">
      <form onSubmit={submit} className="flex flex-col gap-2.5">
        <Field label="Ciphertext">
          <textarea
            aria-label="ciphertext to rewrap"
            value={ct}
            onChange={(e) => setCt(e.target.value)}
            rows={2}
            placeholder="janus:v1:…"
            className={inputCls}
          />
        </Field>
        <Field label="Associated data (optional)">
          <input
            aria-label="associated data for rewrap"
            value={aad}
            onChange={(e) => setAad(e.target.value)}
            className={inputCls}
          />
        </Field>
        <Button type="submit" size="sm" className="self-start" disabled={m.isPending}>Rewrap</Button>
        {error && <p role="alert" className="text-[12.5px] text-danger">{error}</p>}
        {out && <OutBlock label="New ciphertext" value={out} />}
      </form>
    </OpCard>
  )
}

// --- Ed25519 ops -------------------------------------------------------------

function SignCard({ name }: { name: string }) {
  const [text, setText] = useState('')
  const [out, setOut] = useState<string | null>(null)
  const [error, setError] = useState('')

  const m = useMutation({
    mutationFn: () => endpoints.transitSign(name, toB64(text)),
    onSuccess: (r) => setOut(r.signature),
    onError: (e) => { setOut(null); setError(apiErrorTitle(e)) },
  })

  function submit(e: FormEvent) {
    e.preventDefault()
    setError('')
    m.mutate()
  }

  return (
    <OpCard title="Sign" hint="Message is UTF-8 → base64 encoded, then signed with the latest key version.">
      <form onSubmit={submit} className="flex flex-col gap-2.5">
        <Field label="Text">
          <textarea
            aria-label="text to sign"
            value={text}
            onChange={(e) => setText(e.target.value)}
            rows={2}
            className={inputCls}
          />
        </Field>
        <Button type="submit" size="sm" className="self-start" disabled={m.isPending}>Sign</Button>
        {error && <p role="alert" className="text-[12.5px] text-danger">{error}</p>}
        {out && <OutBlock label="Signature" value={out} />}
      </form>
    </OpCard>
  )
}

function VerifyCard({ name }: { name: string }) {
  const [text, setText] = useState('')
  const [sig, setSig] = useState('')
  // valid holds the boolean result; a false is a legitimate answer, not an error.
  const [valid, setValid] = useState<boolean | null>(null)
  const [error, setError] = useState('')

  const m = useMutation({
    mutationFn: () => endpoints.transitVerify(name, toB64(text), sig),
    onSuccess: (r) => setValid(r.valid),
    onError: (e) => { setValid(null); setError(apiErrorTitle(e)) },
  })

  function submit(e: FormEvent) {
    e.preventDefault()
    setError('')
    setValid(null)
    m.mutate()
  }

  return (
    <OpCard title="Verify" hint="Checks a signature against the message across all decryptable versions.">
      <form onSubmit={submit} className="flex flex-col gap-2.5">
        <Field label="Message">
          <textarea
            aria-label="message input"
            value={text}
            onChange={(e) => setText(e.target.value)}
            rows={2}
            className={inputCls}
          />
        </Field>
        <Field label="Signature">
          <input
            aria-label="signature"
            value={sig}
            onChange={(e) => setSig(e.target.value)}
            placeholder="janus:v1:…"
            className={inputCls}
          />
        </Field>
        <Button type="submit" size="sm" className="self-start" disabled={m.isPending}>Verify</Button>
        {error && <p role="alert" className="text-[12.5px] text-danger">{error}</p>}
        {valid !== null && (
          valid
            ? <Pill tone="success" dot>Valid</Pill>
            : <Pill tone="danger" dot>Invalid</Pill>
        )}
      </form>
    </OpCard>
  )
}

// Crypto playground for the selected key. NEVER decrypts, NEVER generates data
// keys — so no plaintext ever returns from the server. Each op is an
// independent useMutation with no query key; outputs live in local state only.
// TransitPage remounts this via `key={selected}` so switching keys clears all
// input/result state.
export function Playground({ keyMeta }: { keyMeta: TransitKey }) {
  return (
    <div className="mt-6">
      <div className="mb-3 flex items-center gap-2">
        <h3 className="font-mono text-[14px] font-semibold text-ink">{keyMeta.name}</h3>
        <Pill tone={keyMeta.type === 'aes256-gcm' ? 'info' : 'brand'}>{keyMeta.type}</Pill>
        <span className="text-[11.5px] text-ink-faint">Crypto playground · no plaintext leaves the server</span>
      </div>
      <div className="grid gap-3 md:grid-cols-2">
        {keyMeta.type === 'aes256-gcm' ? (
          <>
            <EncryptCard name={keyMeta.name} />
            <RewrapCard name={keyMeta.name} />
          </>
        ) : (
          <>
            <SignCard name={keyMeta.name} />
            <VerifyCard name={keyMeta.name} />
          </>
        )}
      </div>
    </div>
  )
}
