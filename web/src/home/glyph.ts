const CLASSES = ['bg-glyph-a', 'bg-glyph-b', 'bg-glyph-c', 'bg-glyph-d'] as const

/** Deterministic glyph gradient class for a project slug. */
export function glyphClass(slug: string): (typeof CLASSES)[number] {
  let h = 0
  for (let i = 0; i < slug.length; i++) h = (h * 31 + slug.charCodeAt(i)) | 0
  return CLASSES[Math.abs(h) % CLASSES.length]
}
