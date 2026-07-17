import { http, HttpResponse } from 'msw'
import { screen, within, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { server } from '../test/msw'
import { renderApp } from '../test/render'
import { ToastProvider } from '../ui/Toast'
import { Member, UserInfo } from '../lib/endpoints'
import { MembersPage } from './MembersPage'

function mockUsers(users: UserInfo[]) {
  server.use(http.get('/v1/users', () => HttpResponse.json({ users })))
}

function mockInstanceMembers(members: Member[]) {
  server.use(http.get('/v1/instance/members', () => HttpResponse.json({ members })))
}

function mockProjects() {
  server.use(
    http.get('/v1/projects', () =>
      HttpResponse.json({ projects: [{ id: 'p1', slug: 'proj', name: 'Proj' }] })),
    http.get('/v1/projects/p1/environments', () =>
      HttpResponse.json({ environments: [{ id: 'e1', slug: 'prod', name: 'Prod' }] })),
  )
}

function mount() {
  return renderApp(
    <ToastProvider><MembersPage /></ToastProvider>,
    { route: '/members', withAuth: false },
  )
}

test('instance members render with emails joined from /v1/users; unknown id falls back to prefix', async () => {
  mockUsers([{ id: 'u2', email: 'bob@example.com', disabled: false }])
  mockInstanceMembers([
    { user_id: 'u2', role: 'admin' },
    { user_id: 'ghost123456789', role: 'viewer' },
  ])
  mount()

  expect((await screen.findAllByText('bob@example.com')).length).toBeGreaterThan(0)
  expect(screen.getByText('ghost123')).toBeInTheDocument()
})

test('scope switch to Project + pick p1 fetches members from /v1/projects/p1/members', async () => {
  mockUsers([])
  mockInstanceMembers([])
  mockProjects()
  let projectPath = ''
  server.use(http.get('/v1/projects/p1/members', () => {
    projectPath = '/v1/projects/p1/members'
    return HttpResponse.json({ members: [{ user_id: 'u2', role: 'developer' }] })
  }))

  mount()
  await screen.findByLabelText('scope')

  await userEvent.selectOptions(screen.getByLabelText('scope'), 'project')
  await screen.findByRole('option', { name: 'Proj' })
  await userEvent.selectOptions(screen.getByLabelText('project'), 'p1')

  await waitFor(() => expect(projectPath).toBe('/v1/projects/p1/members'))
})

test('role change opens confirm dialog, confirm PUTs {role} and toasts', async () => {
  mockUsers([{ id: 'u2', email: 'bob@example.com', disabled: false }])
  mockInstanceMembers([{ user_id: 'u2', role: 'viewer' }])
  let body: unknown
  server.use(http.put('/v1/instance/members/u2', async ({ request }) => {
    body = await request.json()
    return new HttpResponse(null, { status: 204 })
  }))

  mount()
  const roleSelect = await screen.findByLabelText('role for bob@example.com')
  await userEvent.selectOptions(roleSelect, 'admin')

  const dialog = await screen.findByRole('alertdialog')
  await userEvent.click(within(dialog).getByRole('button', { name: 'Change role' }))

  await waitFor(() => expect(body).toEqual({ role: 'admin' }))
  expect(await screen.findByText('Member updated')).toBeInTheDocument()
})

test('role change ceiling: PUT 403 surfaces the exact server message as a danger toast', async () => {
  mockUsers([{ id: 'u2', email: 'bob@example.com', disabled: false }])
  mockInstanceMembers([{ user_id: 'u2', role: 'viewer' }])
  server.use(http.put('/v1/instance/members/u2', () =>
    HttpResponse.json(
      { error: { code: 'forbidden', message: 'cannot grant a role above your own' } },
      { status: 403 },
    )))

  mount()
  const roleSelect = await screen.findByLabelText('role for bob@example.com')
  await userEvent.selectOptions(roleSelect, 'owner')

  const dialog = await screen.findByRole('alertdialog')
  await userEvent.click(within(dialog).getByRole('button', { name: 'Change role' }))

  expect(await screen.findByText('cannot grant a role above your own')).toBeInTheDocument()
})

test('remove member: confirm then DELETE and toast', async () => {
  mockUsers([{ id: 'u2', email: 'bob@example.com', disabled: false }])
  mockInstanceMembers([{ user_id: 'u2', role: 'developer' }])
  let deleted = false
  server.use(http.delete('/v1/instance/members/u2', () => {
    deleted = true
    return new HttpResponse(null, { status: 204 })
  }))

  mount()
  await screen.findAllByText('bob@example.com')
  await userEvent.click(screen.getByRole('button', { name: 'Remove' }))

  const dialog = await screen.findByRole('alertdialog')
  await userEvent.click(within(dialog).getByRole('button', { name: 'Remove' }))

  await waitFor(() => expect(deleted).toBe(true))
  expect(await screen.findByText('Member removed')).toBeInTheDocument()
})

test('add member: sheet lists enabled non-member users only and PUTs chosen role', async () => {
  mockUsers([
    { id: 'u1', email: 'owner@example.com', disabled: false },
    { id: 'u2', email: 'new@example.com', disabled: false },
    { id: 'u3', email: 'gone@example.com', disabled: true },
  ])
  mockInstanceMembers([{ user_id: 'u1', role: 'owner' }])
  let body: unknown
  let path = ''
  server.use(http.put('/v1/instance/members/u2', async ({ request }) => {
    path = '/v1/instance/members/u2'
    body = await request.json()
    return new HttpResponse(null, { status: 204 })
  }))

  mount()
  await screen.findAllByText('owner@example.com')
  await userEvent.click(screen.getByRole('button', { name: 'Add member' }))

  const sheet = await screen.findByRole('dialog')
  expect(within(sheet).getByText('new@example.com')).toBeInTheDocument()
  expect(within(sheet).queryByText('owner@example.com')).toBeNull()
  expect(within(sheet).queryByText('gone@example.com')).toBeNull()

  await userEvent.click(within(sheet).getByText('new@example.com'))
  await userEvent.selectOptions(within(sheet).getByLabelText('role'), 'developer')
  await userEvent.click(within(sheet).getByRole('button', { name: 'Add' }))

  await waitFor(() => expect(path).toBe('/v1/instance/members/u2'))
  expect(body).toEqual({ role: 'developer' })
})

test('create user: sheet email input POSTs and RevealOnce shows the one-time password', async () => {
  mockUsers([])
  mockInstanceMembers([])
  let body: unknown
  server.use(http.post('/v1/users', async ({ request }) => {
    body = await request.json()
    return HttpResponse.json({ id: 'u9', email: 'fresh@example.com', password: 'init-pw-onetime' })
  }))

  mount()
  await userEvent.click(await screen.findByRole('button', { name: 'Create user' }))

  const sheet = await screen.findByRole('dialog')
  await userEvent.type(within(sheet).getByLabelText('email'), 'fresh@example.com')
  await userEvent.click(within(sheet).getByRole('button', { name: 'Create' }))

  await waitFor(() => expect(body).toEqual({ email: 'fresh@example.com' }))
  expect(await screen.findByText('init-pw-onetime')).toBeInTheDocument()
})

test('create user: one-time password lives only in the reveal modal — never a toast, gone after close', async () => {
  mockUsers([])
  mockInstanceMembers([])
  server.use(http.post('/v1/users', () =>
    HttpResponse.json({ id: 'u9', email: 'fresh@example.com', password: 'init-pw-onetime' })))

  mount()
  await userEvent.click(await screen.findByRole('button', { name: 'Create user' }))

  const createSheet = await screen.findByRole('dialog')
  await userEvent.type(within(createSheet).getByLabelText('email'), 'fresh@example.com')
  await userEvent.click(within(createSheet).getByRole('button', { name: 'Create' }))

  // The password surfaces in the RevealOnce modal (a dialog)...
  const revealed = await screen.findByText('init-pw-onetime')
  expect(revealed).toBeInTheDocument()
  expect(revealed.closest('[role="dialog"]')).not.toBeNull()

  // ...but never in a toast title. The toast viewport is a role=region surface;
  // the password value must never appear inside it.
  for (const region of screen.queryAllByRole('region')) {
    expect(within(region).queryByText('init-pw-onetime')).not.toBeInTheDocument()
  }

  // Closing the reveal ("I've stored it") removes the password from the DOM.
  const modal = await screen.findByRole('dialog', { name: 'Initial password' })
  await userEvent.click(within(modal).getByRole('button', { name: "I've stored it" }))
  await waitFor(() => expect(screen.queryByText('init-pw-onetime')).not.toBeInTheDocument())
})

test('disable user self-guard: 409 surfaces the exact server message as a danger toast', async () => {
  mockUsers([{ id: 'u2', email: 'me@example.com', disabled: false }])
  mockInstanceMembers([])
  server.use(http.post('/v1/users/u2/disable', () =>
    HttpResponse.json(
      { error: { code: 'validation', message: 'cannot disable yourself' } },
      { status: 409 },
    )))

  mount()
  await screen.findByText('me@example.com')
  await userEvent.click(screen.getByRole('button', { name: 'Disable' }))

  const dialog = await screen.findByRole('alertdialog')
  await userEvent.click(within(dialog).getByRole('button', { name: 'Disable' }))

  expect(await screen.findByText('cannot disable yourself')).toBeInTheDocument()
})

test('403 on members list shows the "Member access required" empty state', async () => {
  mockUsers([])
  server.use(http.get('/v1/instance/members', () =>
    HttpResponse.json({ error: { code: 'forbidden', message: 'x' } }, { status: 403 })))

  mount()
  expect(await screen.findByText('Member access required')).toBeInTheDocument()
})

it('filters role-bindings by email substring', async () => {
  mockInstanceMembers([
    { user_id: 'u-alice', role: 'developer' },
    { user_id: 'u-bob', role: 'admin' },
  ])
  mockUsers([
    { id: 'u-alice', email: 'alice@corp.io', disabled: false },
    { id: 'u-bob', email: 'bob@corp.io', disabled: false },
  ])
  mount()
  await screen.findByLabelText('search members')
  // Both tables render at instance scope; scope assertions to the members table.
  const membersTable = () => screen.getAllByRole('table')[0]
  await waitFor(() => expect(within(membersTable()).getByText('alice@corp.io')).toBeInTheDocument())
  await userEvent.type(screen.getByLabelText('search members'), 'bob')
  expect(within(membersTable()).queryByText('alice@corp.io')).toBeNull()
  expect(within(membersTable()).getByText('bob@corp.io')).toBeInTheDocument()
})

it('sorts role-bindings by privilege rank, not alphabetically', async () => {
  mockInstanceMembers([
    { user_id: 'u-owner', role: 'owner' },
    { user_id: 'u-viewer', role: 'viewer' },
  ])
  mockUsers([
    { id: 'u-owner', email: 'owner@corp.io', disabled: false },
    { id: 'u-viewer', email: 'viewer@corp.io', disabled: false },
  ])
  mount()
  await screen.findByLabelText('search members')
  const membersTable = () => screen.getAllByRole('table')[0]
  await waitFor(() => expect(within(membersTable()).getByText('owner@corp.io')).toBeInTheDocument())
  await userEvent.click(screen.getByRole('button', { name: /^role/i }))
  const emailCells = within(membersTable()).getAllByRole('cell').filter((c) => /@corp\.io/.test(c.textContent ?? ''))
  expect(emailCells[0]).toHaveTextContent('viewer@corp.io') // viewer(0) before owner(3) ascending
})

it('filters the Users table independently of the members search', async () => {
  mockInstanceMembers([{ user_id: 'u-alice', role: 'developer' }])
  mockUsers([
    { id: 'u-alice', email: 'alice@corp.io', disabled: false },
    { id: 'u-bob', email: 'bob@corp.io', disabled: true },
  ])
  mount()
  await screen.findByText('Users')
  await userEvent.type(screen.getByLabelText('search users'), 'bob')
  expect(screen.getByLabelText('search users')).toHaveValue('bob')
  expect(screen.getByLabelText('search members')).toHaveValue('')
})

function mockMatrixFanOut() {
  // Reuses RbacMatrix.test.tsx's mock shape: one project (p1/App) with one
  // env (e1/Prod), an instance owner, and a project-scoped admin — enough to
  // produce at least one clickable project cell in the matrix.
  mockProjects()
  server.use(
    http.get('/v1/projects/p1/members', () =>
      HttpResponse.json({ members: [{ user_id: 'u1', role: 'admin' }] })),
    http.get('/v1/projects/p1/environments/e1/members', () =>
      HttpResponse.json({ members: [] })),
  )
  mockInstanceMembers([{ user_id: 'u1', role: 'owner' }])
  mockUsers([{ id: 'u1', email: 'a@x.io', disabled: false }])
}

it('defaults to List view with the scope selector visible', async () => {
  mockUsers([])
  mockInstanceMembers([])
  mount()
  await screen.findByLabelText('scope')
  expect(screen.getByRole('button', { name: 'List' })).toHaveAttribute('aria-pressed', 'true')
  expect(screen.getByRole('button', { name: 'Matrix' })).toHaveAttribute('aria-pressed', 'false')
})

it('clicking Matrix hides the scope selector and renders the matrix', async () => {
  mockMatrixFanOut()
  mount()
  await screen.findByLabelText('scope')

  await userEvent.click(screen.getByRole('button', { name: 'Matrix' }))

  expect(await screen.findByText(/explicit role bindings/i)).toBeInTheDocument()
  expect(screen.queryByLabelText('scope')).not.toBeInTheDocument()
  expect(screen.getByRole('button', { name: 'Matrix' })).toHaveAttribute('aria-pressed', 'true')
})

it('clicking a matrix project cell returns to List with that scope selected', async () => {
  mockMatrixFanOut()
  mount()
  await screen.findByLabelText('scope')

  await userEvent.click(screen.getByRole('button', { name: 'Matrix' }))
  const cell = await screen.findByRole('button', { name: /Proj role for a@x\.io/i })
  await userEvent.click(cell)

  expect(await screen.findByLabelText('scope')).toHaveValue('project')
  await waitFor(() => expect(screen.getByLabelText('project')).toHaveValue('p1'))
})

it('rendering at ?view=matrix shows the matrix directly', async () => {
  mockMatrixFanOut()
  renderApp(<ToastProvider><MembersPage /></ToastProvider>, { route: '/members?view=matrix', withAuth: false })

  expect(await screen.findByText(/explicit role bindings/i)).toBeInTheDocument()
  expect(screen.queryByLabelText('scope')).not.toBeInTheDocument()
})
