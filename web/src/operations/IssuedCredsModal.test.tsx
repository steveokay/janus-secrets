import { useState } from 'react'
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { IssuedCredsModal } from './IssuedCredsModal'
import type { IssuedCreds } from './endpoints'

const CREDS: IssuedCreds = { lease_id: 'l1', username: 'janus_ro_abc', password: 'p@ss-SHOWN-ONCE', expires_at: new Date().toISOString() }

function Harness() {
  const [creds, setCreds] = useState<IssuedCreds | null>(CREDS)
  return <IssuedCredsModal creds={creds} onClose={() => setCreds(null)} />
}

test('shows the password once, then wipes it from the DOM on close', async () => {
  render(<Harness />)
  expect(screen.getByText('p@ss-SHOWN-ONCE')).toBeInTheDocument()
  expect(screen.getByText(/shown once/i)).toBeInTheDocument()
  await userEvent.click(screen.getByRole('button', { name: 'Done' }))
  expect(screen.queryByText('p@ss-SHOWN-ONCE')).toBeNull()
})

test('renders nothing when creds is null', () => {
  render(<IssuedCredsModal creds={null} onClose={() => {}} />)
  expect(screen.queryByText(/shown once/i)).toBeNull()
})
