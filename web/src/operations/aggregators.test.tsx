import { http, HttpResponse } from 'msw'
import { screen } from '@testing-library/react'
import { server } from '../test/msw'
import { renderApp } from '../test/render'
import { useRotation } from './useAggregated'

function topology() {
  server.use(http.get('/v1/projects', () => HttpResponse.json({ projects: [
    { id: 'p1', slug: 'acme', name: 'Acme' },
    { id: 'p2', slug: 'billing', name: 'Billing' },
  ] })))
  server.use(http.get('/v1/projects/:pid/environments', ({ params }) =>
    HttpResponse.json({ environments: [{ id: `${params.pid}-e`, slug: 'prod', name: 'prod' }] })))
  server.use(http.get('/v1/projects/:pid/environments/:eid/configs', ({ params }) =>
    HttpResponse.json({ configs: [{ id: `${params.pid}-cfg`, environment_id: params.eid, name: 'prod', inherits_from: null, created_at: 'x' }] })))
}

function RotProbe() {
  const { rows, isLoading } = useRotation('all')
  if (isLoading) return <div>loading</div>
  return (
    <ul>
      {rows.map((r) => (
        <li key={r.data.id}>{r.projectName}:{r.cfg?.configName ?? '?'}:{r.data.secret_key}</li>
      ))}
    </ul>
  )
}

test('useRotation merges policies across projects and joins config names', async () => {
  topology()
  server.use(http.get('/v1/rotation/policies', ({ request }) => {
    const pid = new URL(request.url).searchParams.get('project_id')
    if (pid === 'p1') return HttpResponse.json({ policies: [{ id: 'r1', project_id: 'p1', config_id: 'p1-cfg', secret_key: 'DB', type: 'postgres', status: 'active', failure_count: 0, next_rotation_at: 'x', created_at: 'x', interval_seconds: 3600 }] })
    return HttpResponse.json({ policies: [] })
  }))
  renderApp(<RotProbe />, { route: '/operations', withAuth: false })
  expect(await screen.findByText('Acme:prod:DB')).toBeInTheDocument()
})
