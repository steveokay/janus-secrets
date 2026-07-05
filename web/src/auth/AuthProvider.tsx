import { createContext, useContext, useEffect, useState, ReactNode } from 'react'
import { endpoints, User } from '../lib/endpoints'
import { ApiError } from '../lib/api'

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
    } catch (e) {
      if (e instanceof ApiError && e.status === 401) setUser(null)
      else throw e
    } finally {
      setLoading(false)
    }
  }
  async function logout() {
    await endpoints.logout()
    setUser(null)
  }
  useEffect(() => { void refresh() }, [])

  return <Ctx.Provider value={{ user, loading, refresh, logout }}>{children}</Ctx.Provider>
}

export function useAuth() {
  const c = useContext(Ctx)
  if (!c) throw new Error('useAuth used outside AuthProvider')
  return c
}
