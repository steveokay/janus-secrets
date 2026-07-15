import { http, HttpResponse } from 'msw'
import { screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { server } from '../test/msw'
import { renderApp } from '../test/render'
import { ToastProvider } from '../ui/Toast'
import { ProjectsList } from './ProjectsList'

function mountList() {
  server.use(
    http.get('/v1/projects', () => HttpResponse.json({ projects: [{ id: 'p1', slug: 'web', name: 'Web' }] })),
    http.get('/v1/projects/:pid/environments', () => HttpResponse.json({ environments: [] })),
    http.get('/v1/projects/:pid/metrics/reads-24h', () => new HttpResponse(null, { status: 403 })),
    http.get('/v1/metrics/reads-24h', () => new HttpResponse(null, { status: 403 })),
  )
  return renderApp(<ToastProvider><ProjectsList /></ToastProvider>, { route: '/projects', withAuth: false })
}

test('project delete is ConfirmDialog-gated and soft-deletes', async () => {
  let deleted = ''
  server.use(http.delete('/v1/projects/:pid', ({ params, request }) => {
    if (!new URL(request.url).search.includes('destroy')) deleted = String(params.pid)
    return new HttpResponse(null, { status: 204 })
  }))
  mountList()
  await userEvent.click(await screen.findByRole('button', { name: /delete Web/i }))
  await userEvent.click(await screen.findByRole('button', { name: /move to trash/i }))
  expect(deleted).toBe('p1')
})
