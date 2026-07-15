import { http, HttpResponse } from 'msw'
import { fireEvent, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { server } from '../test/msw'
import { renderApp } from '../test/render'
import { ToastProvider } from '../ui/Toast'
import { ProjectBoard } from './ProjectBoard'

// Silence the ops health chips under onUnhandledRequest:'error'.
function opsEmpty() {
  server.use(
    http.get('/v1/rotation/policies', () => HttpResponse.json({ policies: [] })),
    http.get('/v1/sync/targets', () => HttpResponse.json({ targets: [] })),
  )
}

const CREATED_AT = new Date(Date.now() - 3_600_000).toISOString()

// Three envs; the pipeline orders them dev → staging → prod. dev + staging both
// have a "default" config; prod has "prod-only".
function base({ stagingHasDefault = true }: { stagingHasDefault?: boolean } = {}) {
  server.use(
    http.get('/v1/projects', () => HttpResponse.json({ projects: [{ id: 'p1', slug: 'gw', name: 'api-gateway' }] })),
    http.get('/v1/projects/p1/environments', () =>
      HttpResponse.json({ environments: [
        { id: 'e-prod', slug: 'prod', name: 'Production' },
        { id: 'e-dev', slug: 'dev', name: 'Development' },
        { id: 'e-staging', slug: 'staging', name: 'Staging' },
      ] })),
    http.get('/v1/projects/p1/environments/e-dev/configs', () =>
      HttpResponse.json({ configs: [
        { id: 'c-dev', environment_id: 'e-dev', name: 'default', inherits_from: null, created_at: CREATED_AT },
      ] })),
    http.get('/v1/projects/p1/environments/e-staging/configs', () =>
      HttpResponse.json({ configs: stagingHasDefault
        ? [{ id: 'c-stg', environment_id: 'e-staging', name: 'default', inherits_from: null, created_at: CREATED_AT }]
        : [{ id: 'c-stg-other', environment_id: 'e-staging', name: 'other', inherits_from: null, created_at: CREATED_AT }] })),
    http.get('/v1/projects/p1/environments/e-prod/configs', () =>
      HttpResponse.json({ configs: [
        { id: 'c-prod', environment_id: 'e-prod', name: 'prod-only', inherits_from: null, created_at: CREATED_AT },
      ] })),
    http.get('/v1/projects/p1/metrics/reads-24h', () =>
      HttpResponse.json({ reads_24h: 0, top_configs: [], top_tokens: [] })),
  )
  opsEmpty()
}

function pipeline(ids: string[]) {
  server.use(http.get('/v1/projects/p1/pipeline', () => HttpResponse.json({ environment_ids: ids })))
}

function render() {
  return renderApp(
    <ToastProvider><ProjectBoard /></ToastProvider>,
    { route: '/projects/p1', withAuth: false },
  )
}

// Locate a column's <section> by its env heading, then scope queries to it —
// both dev and staging own a "default" config, so global link queries are
// ambiguous.
function column(name: string): HTMLElement {
  return screen.getByRole('heading', { name }).closest('section') as HTMLElement
}

test('columns are ordered by the pipeline (dev → staging → prod)', async () => {
  base()
  pipeline(['e-dev', 'e-staging', 'e-prod'])
  render()
  await screen.findByRole('heading', { name: 'Development' })
  const headings = screen.getAllByRole('heading', { level: 3 }).map((h) => h.textContent)
  expect(headings).toEqual(['Development', 'Staging', 'Production'])
})

test('a dev "default" card is draggable and shows a Promote button; the last env has none', async () => {
  base()
  pipeline(['e-dev', 'e-staging', 'e-prod'])
  render()
  await screen.findByRole('heading', { name: 'Development' })
  const devCard = within(column('Development')).getByRole('link', { name: /^default\b/i })
  expect(devCard).toHaveAttribute('draggable', 'true')
  expect(within(devCard).getByRole('button', { name: /promote/i })).toBeInTheDocument()

  const prodCard = within(column('Production')).getByRole('link', { name: /prod-only/i })
  expect(within(prodCard).queryByRole('button', { name: /promote/i })).not.toBeInTheDocument()
})

test('clicking Promote → opens the diff modal with a preview for from=dev to=staging', async () => {
  base()
  pipeline(['e-dev', 'e-staging', 'e-prod'])
  let previewUrl = ''
  server.use(
    http.get('/v1/promote/preview', ({ request }) => {
      previewUrl = request.url
      return HttpResponse.json({ source_version: 1, target_exists: true, entries: [] })
    }),
  )
  render()
  await screen.findByRole('heading', { name: 'Development' })
  const devCard = within(column('Development')).getByRole('link', { name: /^default\b/i })
  await userEvent.click(within(devCard).getByRole('button', { name: /promote/i }))

  expect(await screen.findByRole('dialog', { name: /promote development to staging/i })).toBeInTheDocument()
  await waitFor(() => expect(previewUrl).toContain('from=c-dev'))
  expect(previewUrl).toContain('to=c-stg')
})

test('dragging a dev card onto the staging column opens the modal', async () => {
  base()
  pipeline(['e-dev', 'e-staging', 'e-prod'])
  server.use(
    http.get('/v1/promote/preview', () => HttpResponse.json({ source_version: 1, target_exists: true, entries: [] })),
  )
  render()
  await screen.findByRole('heading', { name: 'Development' })
  const devCard = within(column('Development')).getByRole('link', { name: /^default\b/i })
  const stagingCol = column('Staging')

  const dataTransfer = { effectAllowed: '', setData: () => {}, getData: () => '' }
  fireEvent.dragStart(devCard, { dataTransfer })
  fireEvent.dragOver(stagingCol, { dataTransfer })
  fireEvent.drop(stagingCol, { dataTransfer })

  expect(await screen.findByRole('dialog', { name: /promote development to staging/i })).toBeInTheDocument()
})

test('when staging lacks a matching config, Promote opens the modal in CREATE mode', async () => {
  base({ stagingHasDefault: false })
  pipeline(['e-dev', 'e-staging', 'e-prod'])
  // Create mode fetches the source config's keys + versions (names only, no values).
  server.use(
    http.get('/v1/configs/c-dev/secrets', () =>
      HttpResponse.json({ secrets: { API_KEY: { value_version: 1, created_at: 'x', origin: 'own' } } })),
    http.get('/v1/configs/c-dev/versions', () =>
      HttpResponse.json({ versions: [{ version: 3, message: '', created_by: 's', created_at: 'x' }] })),
  )
  render()
  await screen.findByRole('heading', { name: 'Development' })
  const devCard = within(column('Development')).getByRole('link', { name: /^default\b/i })
  await userEvent.click(within(devCard).getByRole('button', { name: /promote/i }))

  const dialog = await screen.findByRole('dialog', { name: /promote development to staging/i })
  // create-mode banner: the target config doesn't exist yet
  expect(await within(dialog).findByText(/has no/i)).toBeInTheDocument()
  expect(await within(dialog).findByText('API_KEY')).toBeInTheDocument()
})

test('with NO pipeline, cards are not draggable, no Promote button, env order is raw', async () => {
  base()
  pipeline([])
  render()
  await screen.findByRole('heading', { name: 'Production' })
  const headings = screen.getAllByRole('heading', { level: 3 }).map((h) => h.textContent)
  // raw environments order (as returned by the API), pipeline disabled
  expect(headings).toEqual(['Production', 'Development', 'Staging'])

  const devCard = within(column('Development')).getByRole('link', { name: /^default\b/i })
  expect(devCard).not.toHaveAttribute('draggable', 'true')
  expect(within(devCard).queryByRole('button', { name: /promote/i })).not.toBeInTheDocument()
})
