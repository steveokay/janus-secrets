// Format already-revealed [key,value] pairs into .env text. Pure: the caller
// is responsible for the audited reveal; nothing here reveals, caches, or logs.
// Quoting is the inverse of parseDotenv/unquote in rowState.ts so a written
// file round-trips back to identical pairs — guaranteed for any value that does
// NOT contain both quote kinds (" and ') at once. A value with both falls back
// to backslash-escaping, which is valid for external .env tools but lossy on
// Janus re-import (unquote strips a matching outer pair literally, without
// processing \" escapes).
export function toEnvText(entries: Array<[string, string]>): string {
  return [...entries]
    .sort(([a], [b]) => a.localeCompare(b))
    .map(([k, v]) => `${k}=${format(v)}`)
    .join('\n')
}

function format(v: string): string {
  // Bare only when parseDotenv returns v unchanged: no whitespace, no '#',
  // no '"', non-empty. Otherwise pick a quote style that round-trips through
  // unquote (which strips a matching outer pair literally, no escape handling):
  // double-quote normally; single-quote when the value contains '"' so no
  // escaping is needed. Only when a value contains BOTH quote kinds do we fall
  // back to escaping (lossy on Janus re-import, but valid for external tools).
  const needsQuote = v === '' || /[\s"#]/.test(v)
  if (!needsQuote) return v
  if (!v.includes('"')) return `"${v}"`
  if (!v.includes("'")) return `'${v}'`
  return `"${v.replace(/"/g, '\\"')}"`
}
