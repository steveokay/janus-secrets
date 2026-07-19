/* Tiny history-based router built on Svelte 5 runes. */

let path = $state(window.location.pathname)

export const router = {
  get path() {
    return path
  },
  go(to: string) {
    history.pushState({}, '', to)
    // path drives route matching and holds only the pathname; the query
    // string stays in the URL for screens to read via location.search.
    path = new URL(to, window.location.origin).pathname
    window.scrollTo(0, 0)
  },
  replace(to: string) {
    history.replaceState({}, '', to)
    path = new URL(to, window.location.origin).pathname
  },
}

window.addEventListener('popstate', () => {
  path = window.location.pathname
})

/** Intercept plain left-clicks on internal <a> links. */
export function linkHandler(e: MouseEvent) {
  if (e.defaultPrevented || e.button !== 0 || e.metaKey || e.ctrlKey || e.shiftKey || e.altKey) return
  const a = (e.target as HTMLElement).closest('a')
  if (!a) return
  const href = a.getAttribute('href')
  if (!href || !href.startsWith('/') || a.target === '_blank') return
  e.preventDefault()
  router.go(href)
}

/** Match a pattern like "/projects/:id/configs/:cid" against the current path. */
export function match(pattern: string, p: string): Record<string, string> | null {
  const patSegs = pattern.split('/').filter(Boolean)
  const pathSegs = p.split('/').filter(Boolean)
  if (patSegs.length !== pathSegs.length) return null
  const params: Record<string, string> = {}
  for (let i = 0; i < patSegs.length; i++) {
    const ps = patSegs[i]
    if (ps.startsWith(':')) params[ps.slice(1)] = decodeURIComponent(pathSegs[i])
    else if (ps !== pathSegs[i]) return null
  }
  return params
}
