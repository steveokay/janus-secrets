const CHARS = 'ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789!@#$%^&*()-_=+'
export function generatePassword(length = 24): string {
  const out = new Uint32Array(length)
  crypto.getRandomValues(out)
  let s = ''
  for (let i = 0; i < length; i++) s += CHARS[out[i] % CHARS.length]
  return s
}
