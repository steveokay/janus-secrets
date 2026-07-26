/* Passkey (WebAuthn) browser glue.

   WebAuthn is a native browser API — `navigator.credentials` — so nothing here
   loads external script and the strict CSP ('self', no unsafe-inline/eval)
   stays intact. This module only translates between the server's JSON ceremony
   options and the ArrayBuffers the platform API expects.

   No credential material is ever persisted: the browser holds the private key
   inside the authenticator, and the values passed through here are the public
   challenge/credential-id handles. */

/** Whether this browser can do WebAuthn at all (requires a secure context). */
export function passkeysSupported(): boolean {
  return typeof window !== 'undefined' &&
    typeof window.PublicKeyCredential !== 'undefined' &&
    !!navigator.credentials
}

/* Conditional mediation ("passkey autofill"): the browser may offer a
   discoverable passkey inside the sign-in field instead of a modal. Not every
   engine implements it, so it is feature-detected and degraded to the explicit
   button rather than assumed. */
export async function conditionalMediationAvailable(): Promise<boolean> {
  if (!passkeysSupported()) return false
  const c = window.PublicKeyCredential as unknown as
    { isConditionalMediationAvailable?: () => Promise<boolean> }
  if (typeof c.isConditionalMediationAvailable !== 'function') return false
  try {
    return await c.isConditionalMediationAvailable()
  } catch {
    return false
  }
}

/** The user dismissed or cancelled the platform prompt — not an error to shout about. */
export class PasskeyAbortError extends Error {
  constructor(message = 'Passkey prompt was dismissed.') {
    super(message)
    this.name = 'PasskeyAbortError'
  }
}

/* Returns an ArrayBuffer (not a Uint8Array view) so the value satisfies
   BufferSource without the SharedArrayBuffer widening TypeScript applies to
   `Uint8Array<ArrayBufferLike>`. */
function b64urlToBytes(s: string): ArrayBuffer {
  const pad = s.length % 4 === 0 ? '' : '='.repeat(4 - (s.length % 4))
  const bin = atob(s.replace(/-/g, '+').replace(/_/g, '/') + pad)
  const buf = new ArrayBuffer(bin.length)
  const out = new Uint8Array(buf)
  for (let i = 0; i < bin.length; i++) out[i] = bin.charCodeAt(i)
  return buf
}

function bytesToB64url(buf: ArrayBuffer): string {
  const bytes = new Uint8Array(buf)
  let bin = ''
  for (let i = 0; i < bytes.length; i++) bin += String.fromCharCode(bytes[i])
  return btoa(bin).replace(/\+/g, '-').replace(/\//g, '_').replace(/=+$/, '')
}

/* The server sends the standard JSON encoding of the ceremony options, in which
   every binary field is base64url. These shapes cover only the fields Janus
   actually emits. */
type JsonDescriptor = { id: string; type: string; transports?: string[] }
type JsonCreationOptions = {
  challenge: string
  rp: { id?: string; name: string }
  user: { id: string; name: string; displayName: string }
  pubKeyCredParams: { type: string; alg: number }[]
  timeout?: number
  excludeCredentials?: JsonDescriptor[]
  authenticatorSelection?: Record<string, unknown>
  attestation?: string
  /* Janus requests `credProps`, which is how the server learns whether the
     authenticator really stored a discoverable credential. It must be forwarded
     verbatim — dropping it silently loses that answer. */
  extensions?: Record<string, unknown>
}
type JsonRequestOptions = {
  challenge: string
  timeout?: number
  rpId?: string
  allowCredentials?: JsonDescriptor[]
  userVerification?: string
}

function toDescriptors(list?: JsonDescriptor[]): PublicKeyCredentialDescriptor[] | undefined {
  if (!list) return undefined
  return list.map(d => ({
    id: b64urlToBytes(d.id),
    type: d.type as PublicKeyCredentialType,
    ...(d.transports ? { transports: d.transports as AuthenticatorTransport[] } : {}),
  }))
}

/** Runs navigator.credentials.create() and returns the JSON body to POST back. */
export async function createCredential(options: unknown): Promise<string> {
  const o = options as JsonCreationOptions
  const publicKey: PublicKeyCredentialCreationOptions = {
    challenge: b64urlToBytes(o.challenge),
    rp: o.rp,
    user: {
      id: b64urlToBytes(o.user.id),
      name: o.user.name,
      displayName: o.user.displayName,
    },
    pubKeyCredParams: o.pubKeyCredParams as PublicKeyCredentialParameters[],
    ...(o.timeout ? { timeout: o.timeout } : {}),
    ...(o.excludeCredentials ? { excludeCredentials: toDescriptors(o.excludeCredentials) } : {}),
    ...(o.authenticatorSelection ? { authenticatorSelection: o.authenticatorSelection as AuthenticatorSelectionCriteria } : {}),
    ...(o.attestation ? { attestation: o.attestation as AttestationConveyancePreference } : {}),
    ...(o.extensions ? { extensions: o.extensions as AuthenticationExtensionsClientInputs } : {}),
  }
  const cred = (await navigator.credentials.create({ publicKey })) as PublicKeyCredential | null
  if (!cred) throw new PasskeyAbortError()
  const r = cred.response as AuthenticatorAttestationResponse
  return JSON.stringify({
    id: cred.id,
    rawId: bytesToB64url(cred.rawId),
    type: cred.type,
    response: {
      clientDataJSON: bytesToB64url(r.clientDataJSON),
      attestationObject: bytesToB64url(r.attestationObject),
    },
    /* credProps tells the server whether the authenticator really stored a
       DISCOVERABLE credential, which is what decides if this passkey can be
       used for passwordless sign-in. Advisory display metadata — the server
       treats it as a hint, never as an authorization input. */
    clientExtensionResults: cred.getClientExtensionResults(),
  })
}

/** Options for a get() ceremony beyond the server-supplied request options. */
export interface AssertionOptions {
  /* 'conditional' asks the browser to surface a passkey through autofill on a
     field marked autocomplete="… webauthn", instead of showing a modal. The
     call then sits pending until the user picks one — so it must be abortable. */
  mediation?: CredentialMediationRequirement
  signal?: AbortSignal
}

/** Runs navigator.credentials.get() and returns the JSON body to POST back. */
export async function getAssertion(options: unknown, extra: AssertionOptions = {}): Promise<string> {
  const o = options as JsonRequestOptions
  const publicKey: PublicKeyCredentialRequestOptions = {
    challenge: b64urlToBytes(o.challenge),
    ...(o.timeout ? { timeout: o.timeout } : {}),
    ...(o.rpId ? { rpId: o.rpId } : {}),
    ...(o.allowCredentials ? { allowCredentials: toDescriptors(o.allowCredentials) } : {}),
    ...(o.userVerification ? { userVerification: o.userVerification as UserVerificationRequirement } : {}),
  }
  const cred = (await navigator.credentials.get({
    publicKey,
    ...(extra.mediation ? { mediation: extra.mediation } : {}),
    ...(extra.signal ? { signal: extra.signal } : {}),
  })) as PublicKeyCredential | null
  if (!cred) throw new PasskeyAbortError()
  const r = cred.response as AuthenticatorAssertionResponse
  return JSON.stringify({
    id: cred.id,
    rawId: bytesToB64url(cred.rawId),
    type: cred.type,
    response: {
      clientDataJSON: bytesToB64url(r.clientDataJSON),
      authenticatorData: bytesToB64url(r.authenticatorData),
      signature: bytesToB64url(r.signature),
      /* The userHandle is what a PASSWORDLESS ceremony uses to name the
         account. The server never trusts it on its own: it resolves the account
         from the credential id in its own store and only cross-checks this
         against it. */
      ...(r.userHandle ? { userHandle: bytesToB64url(r.userHandle) } : {}),
    },
  })
}

/** Friendly text for a platform-side failure (never leaks ceremony detail). */
export function passkeyMessage(e: unknown, fallback: string): string {
  if (e instanceof PasskeyAbortError) return e.message
  if (e instanceof DOMException) {
    switch (e.name) {
      case 'NotAllowedError':
        return 'The passkey prompt was dismissed or timed out.'
      case 'InvalidStateError':
        return 'This device already has a passkey registered for your account.'
      case 'SecurityError':
        return 'This page’s origin does not match the server’s passkey configuration.'
      case 'NotSupportedError':
        return 'This device cannot create the kind of passkey Janus requires.'
    }
  }
  return fallback
}
