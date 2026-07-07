import { http, HttpResponse } from 'msw'
import { screen, waitFor } from '@testing-library/react'
import { server } from '../test/msw'
import { renderApp } from '../test/render'
import { InstanceReadsStrip } from './ReadsStrip'

const DATA = {
  reads_24h: 1284,
  top_configs: [{ config_id: 'c1', config_name: 'prod', project_name: 'api', reads: 210 }],
  top_tokens: [{ token_id: 't1', token_name: 'ci-deploy', reads: 88 }],
}

test('renders the total, top configs, and top tokens', async () => {
  server.use(http.get('/v1/metrics/reads-24h', () => HttpResponse.json(DATA)))
  renderApp(<InstanceReadsStrip />, { withAuth: false })
  expect(await screen.findByText('1,284')).toBeInTheDocument()
  expect(screen.getByText('prod')).toBeInTheDocument()
  expect(screen.getByText('ci-deploy')).toBeInTheDocument()
})

test('renders a zero state when there are no reads', async () => {
  server.use(http.get('/v1/metrics/reads-24h', () =>
    HttpResponse.json({ reads_24h: 0, top_configs: [], top_tokens: [] })))
  renderApp(<InstanceReadsStrip />, { withAuth: false })
  expect(await screen.findByText('No reads yet')).toBeInTheDocument()
})

test('hides itself entirely on a 403 (viewer without audit read)', async () => {
  server.use(http.get('/v1/metrics/reads-24h', () =>
    HttpResponse.json({ error: { code: 'forbidden', message: 'denied' } }, { status: 403 })))
  const { container } = renderApp(<InstanceReadsStrip />, { withAuth: false })
  await waitFor(() => expect(screen.queryByText(/reads 24h/i)).toBeNull())
  expect(container.querySelector('[data-metrics-strip]')).toBeNull()
})
