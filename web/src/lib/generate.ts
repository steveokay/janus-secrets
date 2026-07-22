// Pure, dependency-free random value generators for the secret editor.
//
// All randomness comes from the browser CSPRNG (`crypto.getRandomValues`);
// never `Math.random()`. The generated plaintext lives only in the editor's
// dirty buffer and is saved through the normal encrypted path — this module
// owns randomness so it can be reasoned about in isolation.

const LOWER = 'abcdefghijklmnopqrstuvwxyz'
const UPPER = 'ABCDEFGHIJKLMNOPQRSTUVWXYZ'
const DIGITS = '0123456789'
const SYMBOLS = '!@#$%^&*()-_=+[]{};:,.?'
// Visually ambiguous characters removed when `excludeAmbiguous` is set.
const AMBIGUOUS = new Set(['0', 'O', 'o', '1', 'l', 'I', '|'])

export const PW_MIN = 8
export const PW_MAX = 128
export const BYTES_MIN = 8
export const BYTES_MAX = 256

/** Clamp `n` to [min, max]; fall back to `fallback` when `n` is not a finite number. */
function clampInt(n: number, min: number, max: number, fallback: number): number {
  if (!Number.isFinite(n)) return fallback
  const i = Math.trunc(n)
  if (i < min) return min
  if (i > max) return max
  return i
}

function randomBytes(n: number): Uint8Array {
  const buf = new Uint8Array(n)
  crypto.getRandomValues(buf)
  return buf
}

/**
 * Pick `count` characters uniformly from `pool` using rejection sampling over
 * random bytes. Bytes >= floor(256 / n) * n are rejected and redrawn so there
 * is no modulo bias toward low indices. Draws more random bytes as needed.
 */
function sampleUnbiased(pool: string, count: number): string {
  const n = pool.length
  const limit = Math.floor(256 / n) * n // largest multiple of n that fits in a byte
  let out = ''
  while (out.length < count) {
    // Over-draw a little to reduce the number of getRandomValues calls.
    const batch = randomBytes(Math.max(count - out.length, 16))
    for (let i = 0; i < batch.length && out.length < count; i++) {
      const b = batch[i]
      if (b >= limit) continue // reject to avoid modulo bias
      out += pool[b % n]
    }
  }
  return out
}

/**
 * Generate a random password of `length` characters (clamped to 8–128).
 *
 * Pool: a–z A–Z 0–9 always, plus symbols when `opts.symbols`. When
 * `opts.excludeAmbiguous`, the characters 0 O o 1 l I | are removed. The base
 * alphanumerics are always present, so the pool is never empty.
 */
export function generatePassword(
  length: number,
  opts: { symbols: boolean; excludeAmbiguous: boolean },
): string {
  const len = clampInt(length, PW_MIN, PW_MAX, 24)
  let pool = LOWER + UPPER + DIGITS + (opts.symbols ? SYMBOLS : '')
  if (opts.excludeAmbiguous) {
    pool = [...pool].filter(c => !AMBIGUOUS.has(c)).join('')
  }
  return sampleUnbiased(pool, len)
}

/** Generate `2*bytes` lowercase hex chars from `bytes` random bytes (bytes clamped 8–256). */
export function generateHex(bytes: number): string {
  const n = clampInt(bytes, BYTES_MIN, BYTES_MAX, 32)
  let out = ''
  for (const b of randomBytes(n)) out += b.toString(16).padStart(2, '0')
  return out
}

/** Generate standard base64 of `bytes` random bytes (bytes clamped 8–256). */
export function generateBase64(bytes: number): string {
  const n = clampInt(bytes, BYTES_MIN, BYTES_MAX, 32)
  const buf = randomBytes(n)
  let bin = ''
  for (const b of buf) bin += String.fromCharCode(b)
  return btoa(bin)
}
