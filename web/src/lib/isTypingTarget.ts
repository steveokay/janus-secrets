// Shared guard for global keydown listeners: true while focus is in a text
// field, so single-letter shortcuts (e.g. `?`, `e`, `x`) don't fire while the
// user is typing.
export function isTypingTarget(t: EventTarget | null): boolean {
  const el = t as HTMLElement | null
  if (!el || !el.tagName) return false
  const tag = el.tagName
  return tag === 'INPUT' || tag === 'TEXTAREA' || (el as HTMLElement).isContentEditable === true
}
