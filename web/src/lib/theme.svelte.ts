/* Theme: 'daylight' (default) or 'nightwatch'. Applied via data-theme on <html>. */

export type Theme = 'daylight' | 'nightwatch'

const KEY = 'atrium-theme'
const stored = localStorage.getItem(KEY) as Theme | null
let current = $state<Theme>(stored === 'nightwatch' ? 'nightwatch' : 'daylight')

function apply(t: Theme) {
  if (t === 'nightwatch') document.documentElement.setAttribute('data-theme', 'nightwatch')
  else document.documentElement.removeAttribute('data-theme')
}
apply(current)

export const theme = {
  get current() { return current },
  set(t: Theme) {
    current = t
    localStorage.setItem(KEY, t)
    apply(t)
  },
  toggle() {
    this.set(current === 'daylight' ? 'nightwatch' : 'daylight')
  },
}
