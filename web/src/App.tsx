import { useEffect, useState } from 'react'
import { BrowserRouter, Routes, Route, Navigate } from 'react-router-dom'
import { QueryClientProvider } from '@tanstack/react-query'
import { queryClient, setAuthEventHandler } from './lib/queryClient'
import { endpoints, SealStatus } from './lib/endpoints'
import { AuthProvider, useAuth } from './auth/AuthProvider'
import { LoginPage } from './auth/LoginPage'
import { UnsealPage } from './unseal/UnsealPage'
import { AppLayout } from './shell/AppLayout'
import { Sidebar } from './shell/Sidebar'
import { SecretEditor } from './secrets/SecretEditor'
import { Placeholder } from './shell/Placeholder'

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
    <AppLayout sidebar={<Sidebar />}>
      <Routes>
        <Route path="/" element={<div className="mt-16 text-center text-gray-500">Select or create a project to begin.</div>} />
        <Route path="/projects/:projectId" element={<div className="mt-16 text-center text-gray-500">Select a config from the sidebar.</div>} />
        <Route path="/projects/:projectId/configs/:configId" element={<SecretEditor />} />
        <Route path="/projects/:projectId/audit" element={<Placeholder feature="Audit viewer" />} />
        <Route path="/tokens" element={<Placeholder feature="Token management" />} />
        <Route path="/members" element={<Placeholder feature="Member management" />} />
        <Route path="/transit" element={<Placeholder feature="Transit UI" />} />
        <Route path="/settings" element={<Placeholder feature="Settings" />} />
        <Route path="*" element={<Navigate to="/" replace />} />
      </Routes>
    </AppLayout>
  )
}

export default function App() {
  return (
    <QueryClientProvider client={queryClient}>
      <BrowserRouter>
        <AuthProvider>
          <Gate />
        </AuthProvider>
      </BrowserRouter>
    </QueryClientProvider>
  )
}
