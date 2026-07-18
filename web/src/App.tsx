import { useEffect, useState } from 'react'
import { BrowserRouter, Routes, Route, useLocation } from 'react-router-dom'
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
import { HomePage } from './home/HomePage'
import { ProjectsList } from './home/ProjectsList'
import { ProjectBoard } from './home/ProjectBoard'
import { PipelineSettings } from './promotion/PipelineSettings'
import { ApprovalsPage } from './promotion/ApprovalsPage'
import { AuditPage } from './audit/AuditPage'
import { TokensPage } from './tokens/TokensPage'
import { MembersPage } from './members/MembersPage'
import { TransitPage } from './transit/TransitPage'
import { IntegrationsPage } from './integrations/IntegrationsPage'
import { OperationsPage } from './operations/OperationsPage'
import { SettingsPage } from './settings/SettingsPage'
import { TrashPage } from './trash/TrashPage'
import { PaletteProvider } from './palette/PaletteProvider'
import { ErrorBoundary } from './shell/ErrorBoundary'
import { NotFound } from './shell/NotFound'
import { ShortcutsHelp, useShortcutsHelp } from './shell/ShortcutsHelp'

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

  return <AuthedApp sealed={seal.sealed} />
}

function AuthedApp({ sealed }: { sealed: boolean }) {
  const location = useLocation()
  const shortcutsHelp = useShortcutsHelp()

  return (
    <PaletteProvider>
      <ShortcutsHelp open={shortcutsHelp.open} onClose={shortcutsHelp.close} />
      <AppLayout sealed={sealed} sidebar={<Sidebar />}>
        <ErrorBoundary key={location.pathname}>
          <Routes>
            <Route path="/" element={<HomePage />} />
            <Route path="/projects" element={<ProjectsList />} />
            <Route path="/projects/:projectId" element={<ProjectBoard />} />
            <Route path="/projects/:projectId/pipeline" element={<PipelineSettings />} />
            <Route path="/projects/:projectId/configs/:configId" element={<SecretEditor />} />
            <Route path="/projects/:projectId/audit" element={<AuditPage />} />
            <Route path="/audit" element={<AuditPage />} />
            <Route path="/tokens" element={<TokensPage />} />
            <Route path="/members" element={<MembersPage />} />
            <Route path="/transit" element={<TransitPage />} />
            <Route path="/integrations" element={<IntegrationsPage />} />
            <Route path="/operations" element={<OperationsPage />} />
            <Route path="/approvals" element={<ApprovalsPage />} />
            <Route path="/settings" element={<SettingsPage />} />
            <Route path="/trash" element={<TrashPage />} />
            <Route path="*" element={<NotFound />} />
          </Routes>
        </ErrorBoundary>
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
