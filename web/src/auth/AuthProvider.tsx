import { createContext, useContext, useEffect, useState, ReactNode } from 'react'
import { endpoints, User } from '../lib/endpoints'
import { queryClient } from '../lib/queryClient'

interface AuthState {
  user: User | null
  loading: boolean
  refresh: () => Promise<void>
  logout: () => Promise<void>
}
const Ctx = createContext<AuthState | null>(null)

export function AuthProvider({ children }: { children: ReactNode }) {
  const [user, setUser] = useState<User | null>(null)
  const [loading, setLoading] = useState(true)

  async function refresh() {
    setLoading(true)
    try {
      setUser(await endpoints.me())
    } catch {
      // Any failure to load /me — 401 (unauthenticated) or 503 (sealed) — means
      // "not authenticated" for the UI; the Gate handles the sealed case via
      // seal-status. Swallow it so no promise rejection dangles.
      setUser(null)
    } finally {
      setLoading(false)
    }
  }
  async function logout() {
    await endpoints.logout()
    setUser(null)
    queryClient.clear() // drop any cached secret plaintext on sign-out
  }
  useEffect(() => { void refresh() }, [])

  return <Ctx.Provider value={{ user, loading, refresh, logout }}>{children}</Ctx.Provider>
}

export function useAuth() {
  const c = useContext(Ctx)
  if (!c) throw new Error('useAuth used outside AuthProvider')
  return c
}
