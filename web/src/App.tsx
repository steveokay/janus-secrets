import { useEffect, useState } from 'react'
import { BrowserRouter, Routes, Route, Navigate } from 'react-router-dom'
import { QueryClientProvider } from '@tanstack/react-query'
import { queryClient, setAuthEventHandler } from './lib/queryClient'
import { ToastProvider } from './ui/Toast'
import { endpoints, SealStatus } from './lib/endpoints'
import { AuthProvider, useAuth } from './auth/AuthProvider'
import { LoginPage } from './auth/LoginPage'
import { UnsealPage } from './unseal/UnsealPage'
import { AppLayout } from './shell/AppLayout'
import { Sidebar } from './shell/Sidebar'
import { SecretEditor } from './secrets/SecretEditor'
import { Placeholder } from './shell/Placeholder'
import { ProjectsList } from './home/ProjectsList'
import { ProjectBoard } from './home/ProjectBoard'
import { AuditPage } from './audit/AuditPage'
import { TokensPage } from './tokens/TokensPage'
import { MembersPage } from './members/MembersPage'
import { PaletteProvider } from './palette/PaletteProvider'

function Gate() {
  const { user, loading, refresh } = useAuth()
  const [seal, setSeal] = useState<SealStatus | null>(null)

  useEffect(() => { endpoints.sealStatus().then(setSeal).catch(() => setSeal(null)) }, [])
  useEffect(() => {
    setAuthEventHandler((kind) => {
      if (kind === 'sealed') {
        endpoints.sealStatus().then(setSeal)
      } else {
        // A 401 from any query means the session expired: drop cached data
        // (incl. any secret plaintext) and re-bootstrap — /me will 401 and
        // Gate falls back to the login screen.
        queryClient.clear()
        void refresh()
      }
    })
  }, [refresh])

  if (!seal || loading) return <p className="mt-24 text-center">Loading…</p>
  if (seal.initialized === false)
    return <p className="mt-24 text-center">Server not initialized — run <code>janus init</code>.</p>
  if (seal.sealed) return <UnsealPage onUnsealed={() => endpoints.sealStatus().then(setSeal)} />
  if (!user) return <LoginPage />

  return (
    <PaletteProvider>
      <AppLayout sealed={seal.sealed} sidebar={<Sidebar />}>
        <Routes>
          <Route path="/" element={<ProjectsList />} />
          <Route path="/projects/:projectId" element={<ProjectBoard />} />
          <Route path="/projects/:projectId/configs/:configId" element={<SecretEditor />} />
          <Route path="/projects/:projectId/audit" element={<AuditPage />} />
          <Route path="/audit" element={<AuditPage />} />
          <Route path="/tokens" element={<TokensPage />} />
          <Route path="/members" element={<MembersPage />} />
          <Route path="/transit" element={<Placeholder feature="Transit UI" />} />
          <Route path="/settings" element={<Placeholder feature="Settings" />} />
          <Route path="*" element={<Navigate to="/" replace />} />
        </Routes>
      </AppLayout>
    </PaletteProvider>
  )
}

export default function App() {
  return (
    <QueryClientProvider client={queryClient}>
      <ToastProvider>
        <BrowserRouter>
          <AuthProvider>
            <Gate />
          </AuthProvider>
        </BrowserRouter>
      </ToastProvider>
    </QueryClientProvider>
  )
}
