import { test, expect, type Page } from '@playwright/test'
import { confirmDialog, createTree, ownerRequest, stamp, type Tree } from '../helpers/janus'

/**
 * Members — invite a user, bind a role at PROJECT scope, read it back off the
 * matrix, and remove the binding.
 *
 * RBAC here is deny-by-default and scoped, and the interesting assertion is not
 * "the row says developer" but "the row says developer at THIS scope and
 * nothing at the others". A binding that silently applied instance-wide would
 * still render a satisfying green row on the project view; the only way to see
 * the difference is to switch the scope selector and look again. So this spec
 * checks both directions of the same binding.
 */

test.describe.serial('Members and RBAC bindings', () => {
  let tree: Tree
  const memberEmail = `${stamp('member')}@janus.test`
  let memberPassword = ''

  test.beforeAll(async ({ playwright }) => {
    const request = await ownerRequest(playwright.request)
    tree = await createTree(request, { slug: stamp('members'), envSlug: 'dev', configName: 'default' })
    await request.dispose()
  })

  /** The matrix row for our invitee. */
  function memberRow(page: Page) {
    return page.locator('[data-testid="members-table"] tbody tr').filter({ hasText: memberEmail })
  }

  /** Switches the scope selector, and (for project scope) picks our project. */
  async function selectScope(page: Page, kind: 'instance' | 'project' | 'environment'): Promise<void> {
    await page.locator('.scope-bar').getByRole('button', { name: kind, exact: true }).click()
    if (kind !== 'instance') {
      await page.locator('.scope-bar select').first().selectOption({ label: tree.projectName })
    }
    await expect(page.locator('.scope-bar')).toContainText(
      kind === 'instance' ? 'instance' : tree.projectName,
    )
  }

  test('inviting a user surfaces the password exactly once', async ({ page }) => {
    await page.goto('/members')
    await expect(page.getByRole('heading', { name: /^Members$/ })).toBeVisible()

    await page.getByRole('button', { name: '+ Invite member' }).click()
    await page.locator('#inv-email').fill(memberEmail)
    await page.getByRole('button', { name: 'Create user' }).click()

    await expect(page.getByText(/Created — password shown exactly once/i)).toBeVisible()
    memberPassword = (await page.getByTestId('invited-password').innerText()).trim()
    expect(memberPassword.length, 'no one-time password was shown').toBeGreaterThan(0)

    await page.getByRole('button', { name: 'Done' }).click()

    // The new account is listed, and starts with no binding anywhere —
    // deny-by-default, so an invitation on its own grants nothing.
    await expect(memberRow(page)).toHaveCount(1)
    await expect(memberRow(page)).toContainText('no instance binding')
  })

  test('binding a role at project scope shows up on the matrix', async ({ page }) => {
    await page.goto('/members')
    await selectScope(page, 'project')

    const row = memberRow(page)
    await expect(row).toContainText(`no project binding`)
    await row.locator('select').selectOption('developer')

    await expect(row.locator('.role')).toHaveText('developer')
  })

  test('the binding is confined to the scope it was made at', async ({ page }) => {
    await page.goto('/members')

    // Instance: nothing. If this ever reads "developer", a project-scoped grant
    // has leaked instance-wide.
    await selectScope(page, 'instance')
    await expect(memberRow(page)).toContainText('no instance binding')

    // Environment, under the same project: also nothing of its own. The role
    // still applies there by top-down inheritance, but no BINDING was made.
    await selectScope(page, 'environment')
    await expect(memberRow(page)).toContainText('no environment binding')

    // ...and the project binding is still there where we left it.
    await selectScope(page, 'project')
    await expect(memberRow(page).locator('.role')).toHaveText('developer')
  })

  test('removing the binding takes the role away', async ({ page }) => {
    await page.goto('/members')
    await selectScope(page, 'project')

    const row = memberRow(page)
    await expect(row.locator('.role')).toHaveText('developer')
    await row.getByRole('button', { name: 'Remove' }).click()
    await confirmDialog(page, 'Remove binding')

    await expect(row).toContainText('no project binding')
    await expect(row.locator('.role')).toHaveCount(0)

    // Gone from the server too, not just from this render.
    const users = (await (await page.request.get('/v1/users')).json()) as {
      users: Array<{ id: string; email: string }>
    }
    const uid = users.users.find(u => u.email === memberEmail)?.id
    expect(uid, 'the invited user is missing from /v1/users').toBeTruthy()

    const members = (await (
      await page.request.get(`/v1/projects/${tree.projectId}/members`)
    ).json()) as { members: Array<{ user_id: string; role: string }> }
    expect(
      members.members.some(m => m.user_id === uid),
      'the project binding survived the removal',
    ).toBe(false)
  })
})
