import { ReactElement, ReactNode } from 'react'
import { render } from '@testing-library/react'
import { QueryClientProvider, QueryClient } from '@tanstack/react-query'
import { MemoryRouter, Routes, Route, matchPath } from 'react-router-dom'
import { AuthProvider } from '../auth/AuthProvider'

// Mirrors the dynamic path patterns declared in App.tsx's <Routes>. Rendered
// components may call useParams(), which only resolves against a matched
// <Route>'s `path` — not against the raw pathname — so tests must route the
// element through the same patterns the real app uses. Falls back to a
// catch-all when no pattern matches (e.g. static routes like `/tokens`).
const ROUTE_PATTERNS = [
  '/',
  '/projects/:projectId',
  '/projects/:projectId/configs/:configId',
  '/projects/:projectId/audit',
]

export function renderApp(ui: ReactElement, { route = '/', withAuth = true }: { route?: string; withAuth?: boolean } = {}) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  const wrap = (node: ReactNode) => (withAuth ? <AuthProvider>{node}</AuthProvider> : node)
  const pattern = ROUTE_PATTERNS.find((p) => matchPath(p, route)) ?? '*'
  return render(
    <QueryClientProvider client={qc}>
      <MemoryRouter initialEntries={[route]}>
        {wrap(
          <Routes>
            <Route path={pattern} element={ui} />
          </Routes>,
        )}
      </MemoryRouter>
    </QueryClientProvider>,
  )
}
