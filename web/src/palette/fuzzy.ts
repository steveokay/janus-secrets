// Subsequence fuzzy matcher. Returns null when `query` is not a subsequence of
// `target`; otherwise a score in [0,1] where 1 is best (contiguous prefix).
export function fuzzyScore(query: string, target: string): number | null {
  const q = query.trim().toLowerCase()
  if (q === '') return 0
  const t = target.toLowerCase()

  // Fast path: contiguous substring — score by earliness + tightness.
  const idx = t.indexOf(q)
  if (idx !== -1) {
    const positionBonus = 1 - idx / (t.length + 1) // earlier = higher
    const coverage = q.length / t.length
    return 0.6 + 0.25 * positionBonus + 0.15 * coverage
  }

  // Subsequence match: every query char appears in order.
  let ti = 0
  let gaps = 0
  for (let qi = 0; qi < q.length; qi++) {
    const found = t.indexOf(q[qi], ti)
    if (found === -1) return null
    if (qi > 0 && found > ti) gaps += found - ti
    ti = found + 1
  }
  // Scattered subsequence scores below any substring match (< 0.6).
  const tightness = 1 - Math.min(1, gaps / (t.length + 1))
  return 0.55 * tightness
}
