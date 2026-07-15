import { http, HttpResponse } from 'msw'
import { QueryClientProvider, QueryClient } from '@tanstack/react-query'
import { MemoryRouter, Routes, Route } from 'react-router-dom'
import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { expect, test } from 'vitest'
import { server } from '../test/msw'
import { ToastProvider } from '../ui/Toast'
import { PipelineSettings } from './PipelineSettings'

function seedEnvs() {
  // Envs returned in a non-pipeline order so the test proves we reorder to the
  // pipeline order (dev → staging → prod), not just echo the API order.
  server.use(
    http.get('/v1/projects/p1/environments', () =>
      HttpResponse.json({
        environments: [
          { id: 'env-prod', slug: 'prod', name: 'Production' },
          { id: 'env-dev', slug: 'dev', name: 'Development' },
          { id: 'env-staging', slug: 'staging', name: 'Staging' },
        ],
      }),
    ),
    http.get('/v1/projects/p1/pipeline', () =>
      HttpResponse.json({ environment_ids: ['env-dev', 'env-staging', 'env-prod'] }),
    ),
  )
}

function renderPage() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return render(
    <QueryClientProvider client={qc}>
      <ToastProvider>
        <MemoryRouter initialEntries={['/projects/p1/pipeline']}>
          <Routes>
            <Route path="/projects/:projectId/pipeline" element={<PipelineSettings />} />
          </Routes>
        </MemoryRouter>
      </ToastProvider>
    </QueryClientProvider>,
  )
}

test('lists environments in pipeline order', async () => {
  seedEnvs()
  renderPage()
  await screen.findByText('Development')
  const names = screen
    .getAllByText(/^(Development|Staging|Production)$/)
    .map((n) => n.textContent)
  expect(names).toEqual(['Development', 'Staging', 'Production'])
})

test('up/down reorders a row', async () => {
  seedEnvs()
  renderPage()
  await screen.findByText('Development')
  // Move Staging up (above Development).
  await userEvent.click(screen.getByRole('button', { name: /move staging up/i }))
  const names = screen
    .getAllByText(/^(Development|Staging|Production)$/)
    .map((n) => n.textContent)
  expect(names).toEqual(['Staging', 'Development', 'Production'])
})

test('Save sends the included env ids in the shown order', async () => {
  seedEnvs()
  let sent: string[] | null = null
  server.use(
    http.put('/v1/projects/p1/pipeline', async ({ request }) => {
      const body = (await request.json()) as { environment_ids: string[] }
      sent = body.environment_ids
      return HttpResponse.json({ environment_ids: body.environment_ids })
    }),
  )
  renderPage()
  await screen.findByText('Development')
  // Exclude Production.
  const prodRow = screen.getByText('Production').closest('li')!
  await userEvent.click(within(prodRow).getByRole('checkbox'))
  await userEvent.click(screen.getByRole('button', { name: /^save$/i }))
  await waitFor(() => expect(sent).toEqual(['env-dev', 'env-staging']))
  expect(await screen.findByText(/pipeline saved/i)).toBeInTheDocument()
})
