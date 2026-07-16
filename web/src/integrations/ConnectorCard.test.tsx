import { screen } from '@testing-library/react'
import { GitBranch } from 'lucide-react'
import { renderApp } from '../test/render'
import { ConnectorCard, StatusLine } from './ConnectorCard'

test('StatusLine renders a numeric count as text', () => {
  renderApp(<StatusLine label="Actions sync" value={2} />, { withAuth: false })
  expect(screen.getByText('Actions sync')).toBeInTheDocument()
  expect(screen.getByText('2')).toBeInTheDocument()
})

test('StatusLine renders null as an em-dash (neutral)', () => {
  renderApp(<StatusLine label="Actions sync" value={null} />, { withAuth: false })
  expect(screen.getByText('—')).toBeInTheDocument()
})

test('StatusLine renders booleans as enabled/disabled', () => {
  const { unmount } = renderApp(<StatusLine label="Login" value={true} />, { withAuth: false })
  expect(screen.getByText('enabled')).toBeInTheDocument()
  unmount()
  renderApp(<StatusLine label="Login" value={false} />, { withAuth: false })
  expect(screen.getByText('disabled')).toBeInTheDocument()
})

test('StatusLine shows neither dash nor value while loading (undefined)', () => {
  renderApp(<StatusLine label="Actions sync" value={undefined} />, { withAuth: false })
  expect(screen.queryByText('—')).toBeNull()
  expect(screen.queryByText('enabled')).toBeNull()
})

test('ConnectorCard renders title, description and action links with hrefs', () => {
  renderApp(
    <ConnectorCard
      icon={<GitBranch size={18} />}
      title="GitHub"
      description="Sync secrets to GitHub Actions."
      statuses={<StatusLine label="Actions sync" value={2} />}
      actions={[{ label: 'Sync →', to: '/operations?tab=sync' }]}
    />,
    { withAuth: false },
  )
  expect(screen.getByRole('heading', { name: 'GitHub' })).toBeInTheDocument()
  expect(screen.getByText('Sync secrets to GitHub Actions.')).toBeInTheDocument()
  expect(screen.getByRole('link', { name: 'Sync →' })).toHaveAttribute('href', '/operations?tab=sync')
})
