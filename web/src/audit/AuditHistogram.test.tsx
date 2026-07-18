import { expect, test, vi, afterEach } from 'vitest'
import { screen } from '@testing-library/react'
import { render } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { QueryClientProvider, QueryClient } from '@tanstack/react-query'
import { endpoints } from '../lib/endpoints'
import { AuditHistogram } from './AuditHistogram'

afterEach(() => {
  vi.restoreAllMocks()
})

function mount(onRange = vi.fn()) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  const utils = render(
    <QueryClientProvider client={qc}>
      <AuditHistogram
        filters={{ from: '2026-01-01T00:00:00Z', to: '2026-01-03T00:00:00Z' }}
        onRange={onRange}
      />
    </QueryClientProvider>,
  )
  return { onRange, ...utils }
}

test('renders a bar with an accessible name including the count and click-to-zoom calls onRange', async () => {
  vi.spyOn(endpoints, 'auditHistogram').mockResolvedValue([
    { start: '2026-01-02T00:00:00Z', success: 10, denied: 2, error: 0 },
  ])
  const onRange = vi.fn()
  mount(onRange)

  const bar = await screen.findByRole('button', { name: /10/ })
  expect(bar).toBeInTheDocument()
  expect(bar.getAttribute('aria-label')).toMatch(/denied/i)
  expect(bar.getAttribute('aria-label')).toContain('2')

  await userEvent.click(bar)
  expect(onRange).toHaveBeenCalledTimes(1)
  const [fromISO, toISO] = onRange.mock.calls[0]
  expect(fromISO).toBe('2026-01-02T00:00:00Z')
  expect(toISO).toBe('2026-01-02T01:00:00.000Z')
})

test('empty response shows a muted no-activity message and does not throw', async () => {
  vi.spyOn(endpoints, 'auditHistogram').mockResolvedValue([])
  mount()

  expect(await screen.findByText('No activity in range')).toBeInTheDocument()
})
