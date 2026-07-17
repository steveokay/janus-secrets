import { http, HttpResponse } from 'msw'
import { screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { server } from '../test/msw'
import { renderApp } from '../test/render'
import { ProjectBoard } from './ProjectBoard'
import { ToastProvider } from '../ui/Toast'
import * as endpointsModule from '../lib/endpoints'

// Default rotation/sync handlers so every existing board test keeps passing
// under the global onUnhandledRequest:'error' mode (see web/src/test/setup.ts).
// Tests that want specific ops data/errors override via server.use(...) after
// calling mock() / opsEmpty(), since msw matches the most-recently-added handler.
function opsEmpty() {
  server.use(
    http.get('/v1/rotation/policies', () => HttpResponse.json({ policies: [] })),
    http.get('/v1/sync/targets', () => HttpResponse.json({ targets: [] })),
  )
}

// A real timestamp (not '') so relativeTime() renders "N ago" instead of ''.
const CREATED_AT = new Date(Date.now() - 3_600_000).toISOString()

function mock() {
  server.use(
    http.get('/v1/projects', () => HttpResponse.json({ projects: [{ id: 'p1', slug: 'gw', name: 'api-gateway' }] })),
    http.get('/v1/projects/p1/environments', () =>
      HttpResponse.json({ environments: [
        { id: 'e1', slug: 'dev', name: 'Development' },
        { id: 'e2', slug: 'prod', name: 'Production' },
      ] })),
    http.get('/v1/projects/p1/environments/e1/configs', () =>
      HttpResponse.json({ configs: [
        { id: 'c1', environment_id: 'e1', name: 'dev', inherits_from: null, created_at: CREATED_AT },
        { id: 'c2', environment_id: 'e1', name: 'dev_personal', inherits_from: 'c1', created_at: CREATED_AT },
      ] })),
    http.get('/v1/projects/p1/environments/e2/configs', () =>
      HttpResponse.json({ configs: [{ id: 'c3', environment_id: 'e2', name: 'prod', inherits_from: null, created_at: CREATED_AT }] })),
  )
  opsEmpty()
}

test('renders a column per environment with its configs', async () => {
  mock()
  renderApp(<ProjectBoard />, { route: '/projects/p1', withAuth: false })
  expect(await screen.findByRole('heading', { name: 'Development' })).toBeInTheDocument()
  expect(screen.getByRole('heading', { name: 'Production' })).toBeInTheDocument()
  expect(await screen.findByRole('link', { name: /^dev\b/i })).toHaveAttribute('href', '/projects/p1/configs/c1')
  expect(screen.getByRole('link', { name: /prod/i })).toHaveAttribute('href', '/projects/p1/configs/c3')
})

test('inherited config renders nested under its base', async () => {
  mock()
  renderApp(<ProjectBoard />, { route: '/projects/p1', withAuth: false })
  const branch = await screen.findByRole('link', { name: /dev_personal/i })
  expect(branch).toHaveAttribute('data-inherited', 'true')
})

test('shows the CLI hint and breadcrumb', async () => {
  mock()
  renderApp(<ProjectBoard />, { route: '/projects/p1', withAuth: false })
  // project name appears in both the sr-only h1 and the visible breadcrumb
  expect((await screen.findAllByText('api-gateway')).length).toBeGreaterThan(0)
  expect(screen.getByRole('link', { name: 'Projects' })).toHaveAttribute('href', '/projects')
  expect(screen.getByText(/janus run/i)).toBeInTheDocument()
})

test('board exposes an h1 for the project', async () => {
  mock()
  renderApp(<ProjectBoard />, { route: '/projects/p1', withAuth: false })
  expect(await screen.findByRole('heading', { level: 1, name: 'api-gateway' })).toBeInTheDocument()
})

test('a config whose base is absent still renders (orphan promoted to root)', async () => {
  server.use(
    http.get('/v1/projects', () => HttpResponse.json({ projects: [{ id: 'p1', slug: 'gw', name: 'api-gateway' }] })),
    http.get('/v1/projects/p1/environments', () =>
      HttpResponse.json({ environments: [{ id: 'e1', slug: 'dev', name: 'Development' }] })),
    http.get('/v1/projects/p1/environments/e1/configs', () =>
      HttpResponse.json({ configs: [
        { id: 'c9', environment_id: 'e1', name: 'stray', inherits_from: 'missing', created_at: '' },
      ] })),
  )
  opsEmpty()
  renderApp(<ProjectBoard />, { route: '/projects/p1', withAuth: false })
  expect(await screen.findByRole('link', { name: /stray/i })).toBeInTheDocument()
})

test('a failed config fetch surfaces an error, not a permanent skeleton', async () => {
  mock()
  // e1's config list errors; the Development column must show an error, and the
  // other column must still render normally.
  server.use(
    http.get('/v1/projects/p1/environments/e1/configs', () =>
      HttpResponse.json({ error: { code: 'internal', message: 'boom' } }, { status: 500 })),
  )
  renderApp(<ProjectBoard />, { route: '/projects/p1', withAuth: false })
  expect(await screen.findByRole('link', { name: /prod/i })).toBeInTheDocument()
  expect(await screen.findByText(/couldn't load configs/i)).toBeInTheDocument()
})

test('shows the project-scoped Reads 24h row', async () => {
  server.use(
    http.get('/v1/projects', () => HttpResponse.json({ projects: [{ id: 'p1', slug: 'api', name: 'api' }] })),
    http.get('/v1/projects/:pid/environments', () => HttpResponse.json({ environments: [] })),
    http.get('/v1/projects/:pid/metrics/reads-24h', () =>
      HttpResponse.json({ reads_24h: 7, top_configs: [], top_tokens: [] })),
  )
  opsEmpty()
  renderApp(<ProjectBoard />, { route: '/projects/p1', withAuth: false })
  expect(await screen.findByText('7')).toBeInTheDocument()
})

test('config with a failed rotation policy shows "rotation ⚠"; a healthy-sync config shows "sync ✓"', async () => {
  mock()
  server.use(
    http.get('/v1/rotation/policies', ({ request }) => {
      const pid = new URL(request.url).searchParams.get('project_id')
      if (pid !== 'p1') return HttpResponse.json({ policies: [] })
      return HttpResponse.json({ policies: [
        { id: 'r1', project_id: 'p1', config_id: 'c1', secret_key: 'DB_PASSWORD', type: 'postgres',
          interval_seconds: 3600, status: 'failed', failure_count: 3, last_error: 'boom',
          next_rotation_at: '', created_at: '' },
      ] })
    }),
    http.get('/v1/sync/targets', ({ request }) => {
      const pid = new URL(request.url).searchParams.get('project_id')
      if (pid !== 'p1') return HttpResponse.json({ targets: [] })
      return HttpResponse.json({ targets: [
        { id: 's1', project_id: 'p1', config_id: 'c3', provider: 'github', prune: false,
          interval_seconds: 3600, addr: {}, status: 'active', failure_count: 0, last_error: null,
          next_sync_at: '', managed_keys: [], created_at: '' },
      ] })
    }),
  )
  renderApp(<ProjectBoard />, { route: '/projects/p1', withAuth: false })

  const devLink = await screen.findByRole('link', { name: /^dev\b/i })
  expect(within(devLink).getByText('rotation ⚠')).toBeInTheDocument()

  const prodLink = await screen.findByRole('link', { name: /prod/i })
  expect(within(prodLink).getByText('sync ✓')).toBeInTheDocument()
})

test('rotation and sync both 403 → no chips anywhere, board otherwise intact', async () => {
  mock()
  server.use(
    http.get('/v1/rotation/policies', () =>
      HttpResponse.json({ error: { code: 'forbidden', message: 'x' } }, { status: 403 })),
    http.get('/v1/sync/targets', () =>
      HttpResponse.json({ error: { code: 'forbidden', message: 'x' } }, { status: 403 })),
  )
  renderApp(<ProjectBoard />, { route: '/projects/p1', withAuth: false })

  expect(await screen.findByRole('link', { name: /^dev\b/i })).toHaveAttribute('href', '/projects/p1/configs/c1')
  expect(screen.getByRole('link', { name: /prod/i })).toHaveAttribute('href', '/projects/p1/configs/c3')
  expect(screen.queryByText(/rotation ✓|rotation ⚠/)).not.toBeInTheDocument()
  expect(screen.queryByText(/sync ✓|sync ⚠/)).not.toBeInTheDocument()
})

test('config card renders a created-at line', async () => {
  mock()
  renderApp(<ProjectBoard />, { route: '/projects/p1', withAuth: false })
  const devLink = await screen.findByRole('link', { name: /^dev\b/i })
  expect(within(devLink).getByText(/created .* ago|created just now/)).toBeInTheDocument()
})

test('the env column header exposes a quick-action menu with Rename, Clone environment, and Delete', async () => {
  mock()
  renderApp(<ProjectBoard />, { route: '/projects/p1', withAuth: false })
  await screen.findByRole('heading', { name: 'Development' })
  await userEvent.click(screen.getByRole('button', { name: /actions for Development/i }))
  expect(await screen.findByRole('menuitem', { name: /^rename$/i })).toBeInTheDocument()
  expect(screen.getByRole('menuitem', { name: /clone environment/i })).toBeInTheDocument()
  expect(screen.getByRole('menuitem', { name: /^delete$/i })).toBeInTheDocument()
})

test('Rename opens RenameDialog and submitting calls endpoints.renameEnvironment', async () => {
  mock()
  const renameSpy = vi
    .spyOn(endpointsModule.endpoints, 'renameEnvironment')
    .mockResolvedValue({ id: 'e1', slug: 'dev', name: 'Development 2' })
  renderApp(<ProjectBoard />, { route: '/projects/p1', withAuth: false })
  await screen.findByRole('heading', { name: 'Development' })
  await userEvent.click(screen.getByRole('button', { name: /actions for Development/i }))
  await userEvent.click(await screen.findByRole('menuitem', { name: /^rename$/i }))
  const input = await screen.findByRole('textbox', { name: /name/i })
  expect(input).toHaveValue('Development')
  await userEvent.clear(input)
  await userEvent.type(input, 'Development 2')
  await userEvent.click(screen.getByRole('button', { name: /save/i }))
  expect(renameSpy).toHaveBeenCalledWith('p1', 'e1', 'Development 2')
  renameSpy.mockRestore()
})

test('rename success toast names the NEW env name, not the stale old one', async () => {
  mock()
  const renameSpy = vi
    .spyOn(endpointsModule.endpoints, 'renameEnvironment')
    .mockResolvedValue({ id: 'e1', slug: 'dev', name: 'Development 2' })
  renderApp(
    <ToastProvider>
      <ProjectBoard />
    </ToastProvider>,
    { route: '/projects/p1', withAuth: false },
  )
  await screen.findByRole('heading', { name: 'Development' })
  await userEvent.click(screen.getByRole('button', { name: /actions for Development/i }))
  await userEvent.click(await screen.findByRole('menuitem', { name: /^rename$/i }))
  const input = await screen.findByRole('textbox', { name: /name/i })
  await userEvent.clear(input)
  await userEvent.type(input, 'Development 2')
  await userEvent.click(screen.getByRole('button', { name: /save/i }))
  // The toast must reflect the freshly-entered name, not the stale prop.
  expect(await screen.findByText(/renamed to Development 2/i)).toBeInTheDocument()
  expect(screen.queryByText('Renamed to Development')).not.toBeInTheDocument()
  renameSpy.mockRestore()
})

test('Clone environment opens CloneEnvDialog and submitting calls endpoints.cloneEnvironment', async () => {
  mock()
  const cloneSpy = vi
    .spyOn(endpointsModule.endpoints, 'cloneEnvironment')
    .mockResolvedValue({ id: 'e9', slug: 'dev-2', name: 'Development 2' })
  renderApp(<ProjectBoard />, { route: '/projects/p1', withAuth: false })
  await screen.findByRole('heading', { name: 'Development' })
  await userEvent.click(screen.getByRole('button', { name: /actions for Development/i }))
  await userEvent.click(await screen.findByRole('menuitem', { name: /clone environment/i }))
  await userEvent.type(screen.getByRole('textbox', { name: /slug/i }), 'dev-2')
  await userEvent.type(screen.getByRole('textbox', { name: /name/i }), 'Development 2')
  await userEvent.click(screen.getByRole('button', { name: /save/i }))
  expect(cloneSpy).toHaveBeenCalledWith('p1', 'e1', 'dev-2', 'Development 2')
  cloneSpy.mockRestore()
})

test('inherited config shows a connector line; the root config does not', async () => {
  mock()
  renderApp(<ProjectBoard />, { route: '/projects/p1', withAuth: false })
  const rootLink = await screen.findByRole('link', { name: /^dev\b/i })
  const branchLink = await screen.findByRole('link', { name: /dev_personal/i })

  // Root card (depth 0): no connector.
  expect(within(rootLink).queryByTestId('inherit-connector')).not.toBeInTheDocument()

  // Child card (depth > 0): exactly one connector.
  expect(within(branchLink).getAllByTestId('inherit-connector')).toHaveLength(1)
})

test('shows a recency subline when the env has last_activity_at', async () => {
  server.use(
    http.get('/v1/projects', () => HttpResponse.json({ projects: [{ id: 'p1', slug: 'gw', name: 'api-gateway' }] })),
    http.get('/v1/projects/p1/environments', () =>
      HttpResponse.json({ environments: [
        { id: 'e1', slug: 'dev', name: 'Development', last_activity_at: new Date(Date.now() - 3_600_000).toISOString() },
        { id: 'e2', slug: 'prod', name: 'Production' },
      ] })),
    http.get('/v1/projects/p1/environments/e1/configs', () => HttpResponse.json({ configs: [] })),
    http.get('/v1/projects/p1/environments/e2/configs', () => HttpResponse.json({ configs: [] })),
  )
  opsEmpty()
  renderApp(<ProjectBoard />, { route: '/projects/p1', withAuth: false })
  await screen.findByRole('heading', { name: 'Development' })
  expect(screen.getByText(/active /)).toBeInTheDocument()
})
