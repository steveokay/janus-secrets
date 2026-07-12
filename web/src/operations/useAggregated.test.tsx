import { http, HttpResponse } from 'msw'
import { screen } from '@testing-library/react'
import { server } from '../test/msw'
import { renderApp } from '../test/render'
import { useProjectConfigMap, useFanOut } from './useAggregated'
import { ApiError } from '../lib/api'

function mockTopology() {
  server.use(http.get('/v1/projects', () => HttpResponse.json({ projects: [
    { id: 'p1', slug: 'acme', name: 'Acme' },
    { id: 'p2', slug: 'billing', name: 'Billing' },
  ] })))
  server.use(http.get('/v1/projects/:pid/environments', ({ params }) =>
    HttpResponse.json({ environments: [{ id: `${params.pid}-e`, slug: 'prod', name: 'prod' }] })))
  server.use(http.get('/v1/projects/:pid/environments/:eid/configs', ({ params }) =>
    HttpResponse.json({ configs: [{ id: `${params.pid}-cfg`, environment_id: params.eid, name: 'prod', inherits_from: null, created_at: 'x' }] })))
}

function MapProbe() {
  const { map, isLoading, isError } = useProjectConfigMap('all')
  if (isLoading) return <div>loading</div>
  if (isError) return <div>error</div>
  const info = map.get('p1-cfg')
  return <div>{info ? `${info.projectName}/${info.envName}/${info.configName}` : 'none'}</div>
}

test('useProjectConfigMap resolves config_id → project/env/config names', async () => {
  mockTopology()
  renderApp(<MapProbe />, { route: '/operations', withAuth: false })
  expect(await screen.findByText('Acme/prod/prod')).toBeInTheDocument()
})

test('useProjectConfigMap tolerates a 403 sub-list without erroring', async () => {
  mockTopology()
  // configs forbidden for one project → entry just absent, not an error
  server.use(http.get('/v1/projects/:pid/environments/:eid/configs', () =>
    HttpResponse.json({ error: { code: 'forbidden', message: 'no' } }, { status: 403 })))
  renderApp(<MapProbe />, { route: '/operations', withAuth: false })
  expect(await screen.findByText('none')).toBeInTheDocument()
})

test('useProjectConfigMap surfaces a non-403 enumeration error as isError', async () => {
  mockTopology()
  server.use(http.get('/v1/projects/:pid/environments', () =>
    HttpResponse.json({ error: { code: 'internal', message: 'boom' } }, { status: 500 })))
  renderApp(<MapProbe />, { route: '/operations', withAuth: false })
  expect(await screen.findByText('error')).toBeInTheDocument()
})

function FanProbe() {
  const scopes = [{ id: 'p1' }, { id: 'p2' }]
  const { perScope, someForbidden, isLoading } = useFanOut(scopes, ['t', 'x'], async (id) => {
    if (id === 'p2') throw new ApiError(403, 'forbidden', 'nope')
    return [{ id: 'a' }, { id: 'b' }]
  })
  if (isLoading) return <div>loading</div>
  return <div>rows={perScope.flatMap((s) => s.data).length} forbidden={String(someForbidden)}</div>
}

test('useFanOut drops a 403 scope and flags someForbidden', async () => {
  renderApp(<FanProbe />, { route: '/operations', withAuth: false })
  expect(await screen.findByText('rows=2 forbidden=true')).toBeInTheDocument()
})
