import { render, screen } from '@testing-library/react'
import { ApiError } from '../lib/api'
import { RunHistorySheet } from './RunHistorySheet'
import type { RunsPage } from './endpoints'

const PAGE: RunsPage = {
  runs: [
    { id: 2, started_at: new Date(Date.now() - 60_000).toISOString(), ended_at: new Date(Date.now() - 59_660).toISOString(), status: 'success', config_version: 5, attempt_num: 1, keys_count: 4 },
    { id: 1, started_at: new Date(Date.now() - 120_000).toISOString(), ended_at: new Date(Date.now() - 118_800).toISOString(), status: 'failure', error: 'apply failed', attempt_num: 2 },
  ],
  next_cursor: 1,
}

test('renders a success + a failure run and a Load more button', async () => {
  render(<RunHistorySheet open onOpenChange={() => {}} title="Runs · DB_PASSWORD" load={async () => PAGE} />)
  expect(await screen.findByText('success')).toBeInTheDocument()
  expect(screen.getByText('failure')).toBeInTheDocument()
  // sanitized error category surfaces as the "last error" marker (value-free)
  expect(screen.getByLabelText('last error')).toBeInTheDocument()
  expect(screen.getByRole('button', { name: /load more/i })).toBeInTheDocument()
})

test('surfaces keys_count for a sync run that has it, omits it for a run without', async () => {
  render(<RunHistorySheet open onOpenChange={() => {}} title="Runs · apps/app-secrets" load={async () => PAGE} />)
  // the success run carries keys_count → shown next to its config version
  expect(await screen.findByText(/4 keys/)).toBeInTheDocument()
  // exactly one "· N keys" cell — the failure run has no keys_count
  expect(screen.getAllByText(/keys$/)).toHaveLength(1)
})

test('shows the access hint on a 403 without crashing', async () => {
  const load = async () => { throw new ApiError(403, 'forbidden', 'no') }
  render(<RunHistorySheet open onOpenChange={() => {}} title="Runs · DB_PASSWORD" load={load} />)
  expect(await screen.findByText(/access required/i)).toBeInTheDocument()
})
