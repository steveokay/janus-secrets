import { ReactElement, ReactNode } from 'react'
import { render } from '@testing-library/react'
import { QueryClientProvider, QueryClient } from '@tanstack/react-query'
import { MemoryRouter } from 'react-router-dom'
import { AuthProvider } from '../auth/AuthProvider'

export function renderApp(ui: ReactElement, { route = '/', withAuth = true }: { route?: string; withAuth?: boolean } = {}) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  const wrap = (node: ReactNode) => (withAuth ? <AuthProvider>{node}</AuthProvider> : node)
  return render(
    <QueryClientProvider client={qc}>
      <MemoryRouter initialEntries={[route]}>{wrap(ui)}</MemoryRouter>
    </QueryClientProvider>,
  )
}
