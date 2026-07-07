import { createContext, useCallback, useContext, useEffect, useMemo, useState } from 'react'

export type Theme = 'light' | 'dark' | 'system'
type Resolved = 'light' | 'dark'

const KEY = 'janus.theme'
const MQ = '(prefers-color-scheme: dark)'

function readStored(): Theme {
  try {
    const v = localStorage.getItem(KEY)
    if (v === 'light' || v === 'dark' || v === 'system') return v
  } catch {
    /* localStorage unavailable — fall through to default */
  }
  return 'system'
}

function systemDark(): boolean {
  // Guarded: jsdom and older environments lack matchMedia — treat as light.
  return typeof window !== 'undefined' &&
    typeof window.matchMedia === 'function' &&
    window.matchMedia(MQ).matches
}

function resolve(theme: Theme): Resolved {
  if (theme === 'system') return systemDark() ? 'dark' : 'light'
  return theme
}

function applyClass(resolved: Resolved): void {
  document.documentElement.classList.toggle('dark', resolved === 'dark')
}

interface ThemeCtx {
  theme: Theme
  resolved: Resolved
  setTheme: (t: Theme) => void
}

const Ctx = createContext<ThemeCtx | null>(null)

export function ThemeProvider({ children }: { children: React.ReactNode }) {
  const [theme, setThemeState] = useState<Theme>(() => readStored())
  const [resolved, setResolved] = useState<Resolved>(() => resolve(theme))

  useEffect(() => {
    const r = resolve(theme)
    setResolved(r)
    applyClass(r)
  }, [theme])

  useEffect(() => {
    if (theme !== 'system' || typeof window.matchMedia !== 'function') return
    const mql = window.matchMedia(MQ)
    const onChange = () => {
      const r: Resolved = mql.matches ? 'dark' : 'light'
      setResolved(r)
      applyClass(r)
    }
    mql.addEventListener('change', onChange)
    return () => mql.removeEventListener('change', onChange)
  }, [theme])

  const setTheme = useCallback((t: Theme) => {
    try {
      localStorage.setItem(KEY, t)
    } catch {
      /* ignore persistence failure */
    }
    setThemeState(t)
  }, [])

  const value = useMemo(() => ({ theme, resolved, setTheme }), [theme, resolved, setTheme])
  return <Ctx.Provider value={value}>{children}</Ctx.Provider>
}

export function useTheme(): ThemeCtx {
  const v = useContext(Ctx)
  if (!v) throw new Error('useTheme must be used within ThemeProvider')
  return v
}
