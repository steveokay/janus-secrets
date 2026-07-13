import { useState } from 'react'
import { http, HttpResponse } from 'msw'
import { screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { server } from '../test/msw'
import { renderApp } from '../test/render'
import { ConfigPicker } from './ConfigPicker'

function topo() {
  server.use(http.get('/v1/projects', () => HttpResponse.json({ projects: [{ id: 'p1', slug: 'acme', name: 'Acme' }] })))
  server.use(http.get('/v1/projects/:pid/environments', () => HttpResponse.json({ environments: [{ id: 'e1', slug: 'prod', name: 'prod' }] })))
  server.use(http.get('/v1/projects/:pid/environments/:eid/configs', () => HttpResponse.json({ configs: [{ id: 'c1', environment_id: 'e1', name: 'prod', inherits_from: null, created_at: 'x' }] })))
}

function Probe({ onPick }: { onPick: (id: string) => void }) {
  const [value, setValue] = useState('')
  return <ConfigPicker filter="all" value={value} onChange={(id) => { setValue(id); onPick(id) }} />
}

test('renders one option per config labeled project / env / config', async () => {
  topo()
  renderApp(<Probe onPick={() => {}} />, { route: '/operations', withAuth: false })
  expect(await screen.findByRole('option', { name: 'Acme / prod / prod' })).toBeInTheDocument()
})

test('selecting a config fires onChange with the config id', async () => {
  topo()
  let picked = ''
  renderApp(<Probe onPick={(id) => { picked = id }} />, { route: '/operations', withAuth: false })
  await screen.findByRole('option', { name: 'Acme / prod / prod' })
  await userEvent.selectOptions(screen.getByRole('combobox'), 'c1')
  expect(picked).toBe('c1')
})
