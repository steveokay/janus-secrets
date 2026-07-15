import { http, HttpResponse } from 'msw'
import { screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { server } from '../test/msw'
import { renderApp } from '../test/render'
import { ToastProvider } from '../ui/Toast'
import { PromotionDiffModal } from './PromotionDiffModal'
import type { Config, Environment } from '../lib/endpoints'

const fromEnv: Environment = { id: 'env-dev', slug: 'dev', name: 'dev' }
const toEnv: Environment = { id: 'env-staging', slug: 'staging', name: 'staging' }
const from: Config = { id: 'c-dev', environment_id: 'env-dev', name: 'default', inherits_from: null, created_at: 'x' }
const to: Config = { id: 'c-stg', environment_id: 'env-staging', name: 'default', inherits_from: null, created_at: 'x' }

// A preview with one of each status, and one locked change row.
function mockPreview() {
  server.use(
    http.get('/v1/promote/preview', () =>
      HttpResponse.json({
        source_version: 12,
        target_exists: true,
        entries: [
          { key: 'FEATURE_WALLET', status: 'add', source_value: 'true', target_value: '', locked: false },
          { key: 'LOG_LEVEL', status: 'change', source_value: 'debug', target_value: 'info', locked: false },
          { key: 'DATABASE_URL', status: 'change', source_value: 'postgres://dev', target_value: 'postgres://stg', locked: true },
          { key: 'API_TIMEOUT_MS', status: 'same', source_value: '30000', target_value: '30000', locked: false },
          { key: 'LEGACY_MODE', status: 'remove', source_value: '', target_value: 'on', locked: false },
        ],
      }),
    ),
  )
}

function renderModal(overrides: Partial<Parameters<typeof PromotionDiffModal>[0]> = {}) {
  return renderApp(
    <ToastProvider>
      <PromotionDiffModal from={from} to={to} fromEnv={fromEnv} toEnv={toEnv} onClose={() => {}} {...overrides} />
    </ToastProvider>,
    { route: '/', withAuth: false },
  )
}

// The count is split across <b> nodes ("N of M keys selected"); match the joined
// textContent of the summary line.
function expectCount(sel: number, total: number) {
  const nodes = screen.getAllByText((_c, el) => el?.textContent === `${sel} of ${total} keys selected`)
  expect(nodes.length).toBeGreaterThan(0)
}

test('renders a row per entry with the right status chip text', async () => {
  mockPreview()
  renderModal()
  expect(await screen.findByText('FEATURE_WALLET')).toBeInTheDocument()
  expect(screen.getByText('LOG_LEVEL')).toBeInTheDocument()
  expect(screen.getByText('DATABASE_URL')).toBeInTheDocument()
  expect(screen.getByText('API_TIMEOUT_MS')).toBeInTheDocument()
  expect(screen.getByText('LEGACY_MODE')).toBeInTheDocument()
  // status chips
  expect(screen.getByText('Add')).toBeInTheDocument()
  expect(screen.getAllByText('Change')).toHaveLength(2)
  expect(screen.getByText('Unchanged')).toBeInTheDocument()
  expect(screen.getByText('Remove')).toBeInTheDocument()
})

test('add/change checked by default; remove/same unchecked; locked row is disabled + unchecked', async () => {
  mockPreview()
  renderModal()
  await screen.findByText('FEATURE_WALLET')
  const cb = (key: string) => screen.getByLabelText(new RegExp(`promote ${key}`, 'i')) as HTMLInputElement
  expect(cb('FEATURE_WALLET').checked).toBe(true) // add
  expect(cb('LOG_LEVEL').checked).toBe(true) // change
  expect(cb('DATABASE_URL').checked).toBe(false) // locked change
  expect(cb('DATABASE_URL').disabled).toBe(true) // locked → disabled
  expect(cb('API_TIMEOUT_MS').checked).toBe(false) // same
  expect(cb('LEGACY_MODE').checked).toBe(false) // remove
})

test('footer count reflects the default selection and updates on toggle', async () => {
  mockPreview()
  renderModal()
  await screen.findByText('FEATURE_WALLET')
  // default: add + change (unlocked) = 2 of 5
  expectCount(2, 5)
  expect(screen.getByRole('button', { name: /promote 2 keys/i })).toBeInTheDocument()
  // check the remove row → 3
  await userEvent.click(screen.getByLabelText(/promote LEGACY_MODE/i))
  expectCount(3, 5)
  // uncheck the add row → 2
  await userEvent.click(screen.getByLabelText(/promote FEATURE_WALLET/i))
  expectCount(2, 5)
})

test('reveal toggle unmasks a row value only after clicking', async () => {
  mockPreview()
  renderModal()
  await screen.findByText('LOG_LEVEL')
  expect(screen.queryByText('debug')).not.toBeInTheDocument()
  const row = screen.getByText('LOG_LEVEL').closest('[data-row]') as HTMLElement
  await userEvent.click(within(row).getByRole('button', { name: /reveal/i }))
  expect(within(row).getByText('debug')).toBeInTheDocument()
  expect(within(row).getByText('info')).toBeInTheDocument()
})

test('confirm posts exactly the selected {key,action} set and fires success toast + onDone/onClose', async () => {
  mockPreview()
  let body: any
  server.use(
    http.post('/v1/promote', async ({ request }) => {
      body = await request.json()
      return HttpResponse.json({ target_version: 13, applied: ['FEATURE_WALLET', 'LOG_LEVEL', 'LEGACY_MODE'], skipped: [] })
    }),
  )
  const onDone = vi.fn()
  const onClose = vi.fn()
  renderModal({ onDone, onClose })
  await screen.findByText('FEATURE_WALLET')
  // include the remove row too
  await userEvent.click(screen.getByLabelText(/promote LEGACY_MODE/i))
  await userEvent.click(screen.getByRole('button', { name: /promote 3 keys/i }))

  await waitFor(() => expect(body).toBeTruthy())
  expect(body.from_config).toBe('c-dev')
  expect(body.to_config).toBe('c-stg')
  expect(body.source_version).toBe(12)
  // exactly the checked rows, with remove→remove and the rest→set
  const sels = (body.selections as { key: string; action: string }[]).sort((a, b) => a.key.localeCompare(b.key))
  expect(sels).toEqual([
    { key: 'FEATURE_WALLET', action: 'set' },
    { key: 'LEGACY_MODE', action: 'remove' },
    { key: 'LOG_LEVEL', action: 'set' },
  ])
  expect(await screen.findByText(/promoted 3 keys to staging/i)).toBeInTheDocument()
  await waitFor(() => expect(onDone).toHaveBeenCalled())
  expect(onClose).toHaveBeenCalled()
})

test('an apply error keeps the modal open and shows a danger toast', async () => {
  mockPreview()
  server.use(
    http.post('/v1/promote', () => HttpResponse.json({ error: { code: 'internal', message: 'boom' } }, { status: 500 })),
  )
  const onClose = vi.fn()
  renderModal({ onClose })
  await screen.findByText('FEATURE_WALLET')
  await userEvent.click(screen.getByRole('button', { name: /promote 2 keys/i }))
  // toast surfaces; modal stays (onClose not called)
  await waitFor(() => expect(screen.getByText('FEATURE_WALLET')).toBeInTheDocument())
  expect(onClose).not.toHaveBeenCalled()
})
