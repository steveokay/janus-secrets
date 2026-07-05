import { useEffect, useState } from 'react'
import { BrowserRouter, Routes, Route, Navigate, useNavigate } from 'react-router-dom'
import { QueryClientProvider } from '@tanstack/react-query'
import { queryClient, setAuthEventHandler } from './lib/queryClient'
import { endpoints, SealStatus } from './lib/endpoints'
import { AuthProvider, useAuth } from './auth/AuthProvider'
import { LoginPage } from './auth/LoginPage'
import { UnsealPage } from './unseal/UnsealPage'
import { AppLayout } from './shell/AppLayout'
import { Sidebar } from './shell/Sidebar'

function Gate() {
  const { user, loading } = useAuth()
  const [seal, setSeal] = useState<SealStatus | null>(null)
  const navigate = useNavigate()

  useEffect(() => { endpoints.sealStatus().then(setSeal).catch(() => setSeal(null)) }, [])
  useEffect(() => {
    setAuthEventHandler((kind) => {
      if (kind === 'sealed') endpoints.sealStatus().then(setSeal)
      else navigate('/login')
    })
  }, [navigate])

  if (!seal || loading) return <p className="mt-24 text-center">Loading…</p>
  if (seal.initialized === false)
    return <p className="mt-24 text-center">Server not initialized — run <code>janus init</code>.</p>
  if (seal.sealed) return <UnsealPage onUnsealed={() => endpoints.sealStatus().then(setSeal)} />
  if (!user) return <LoginPage />

  return (
    <AppLayout sidebar={<Sidebar />}>
      <Routes>
        <Route path="/" element={<p>Select a project.</p>} />
        <Route path="/projects/:projectId" element={<p>Select a config.</p>} />
        <Route path="/projects/:projectId/configs/:configId" element={<p>Editor…</p>} />
        <Route path="/tokens" element={<p>Coming soon.</p>} />
        <Route path="/members" element={<p>Coming soon.</p>} />
        <Route path="/transit" element={<p>Coming soon.</p>} />
        <Route path="/settings" element={<p>Coming soon.</p>} />
        <Route path="/projects/:projectId/audit" element={<p>Coming soon.</p>} />
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
