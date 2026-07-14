// Format already-revealed [key,value] pairs into .env text. Pure: the caller
// is responsible for the audited reveal; nothing here reveals, caches, or logs.
// Quoting is the inverse of parseDotenv/unquote in rowState.ts so a written
// file round-trips back to identical pairs.
export function toEnvText(entries: Array<[string, string]>): string {
  return [...entries]
    .sort(([a], [b]) => a.localeCompare(b))
    .map(([k, v]) => `${k}=${format(v)}`)
    .join('\n')
}

function format(v: string): string {
  // Bare only when parseDotenv would return v unchanged and the value carries no
  // ambiguity: no whitespace, no '#', no quote/newline. Otherwise double-quote
  // and escape embedded quotes.
  const needsQuote = v === '' || /[\s"#]/.test(v)
  if (!needsQuote) return v
  return `"${v.replace(/"/g, '\\"')}"`
}
