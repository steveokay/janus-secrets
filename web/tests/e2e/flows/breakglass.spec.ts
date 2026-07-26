import { test, expect, type BrowserContext, type Page } from '@playwright/test'
import { confirmDialog, createTree, ownerRequest, stamp, type Tree } from '../helpers/janus'
import { COLLEAGUE_STATE } from '../helpers/paths'

/**
 * Break-glass — time-boxed role elevation, and the noise it is supposed to make.
 *
 * The whole design intent is "a paved path, not shared root credentials", which
 * makes three things worth asserting from the outside:
 *
 *   • it is DENY-BY-DEFAULT in both directions. A reason is mandatory, and the
 *     target role must be strictly above the role you already hold on the scope
 *     — you cannot elevate sideways, and you cannot elevate into a scope you
 *     cannot already see.
 *   • it is LOUD. The activation lands in the hash-chained audit ledger with the
 *     reason attached, where an operator will find it.
 *   • it ENDS. The grant is time-boxed, and can be cut short.
 *
 * It runs as the developer minted by auth.setup.ts, not the owner: the guard
 * requires a bound role strictly BELOW the target, and the bootstrapped
 * instance owner already holds owner everywhere, so break-glass is (correctly)
 * unavailable to them and cannot be exercised from the default context.
 */

test.describe.serial('Break-glass emergency access', () => {
  let tree: Tree
  let colleague: BrowserContext
  let asColleague: Page
  const REASON = `E2E incident ${stamp('inc')}`

  test.beforeAll(async ({ playwright, browser }) => {
    const request = await ownerRequest(playwright.request)
    tree = await createTree(request, { slug: stamp('bg'), envSlug: 'prod', configName: 'default' })
    await request.dispose()

    colleague = await browser.newContext({ storageState: COLLEAGUE_STATE })
    asColleague = await colleague.newPage()
  })

  test.afterAll(async () => {
    await colleague?.close()
  })

  async function openBreakGlass(page: Page): Promise<void> {
    await page.goto('/break-glass')
    await expect(page.getByRole('heading', { name: /Emergency access/i })).toBeVisible()
  }

  async function fillForm(
    page: Page,
    opts: { role: string; ttl: string; reason: string },
  ): Promise<void> {
    // By test id, not by label: the form's labels wrap their <select>, so the
    // accessible name absorbs the SELECTED OPTION's text. With scope set to
    // "project", getByLabel('Project') matches the Scope control as well.
    await page.getByTestId('bg-scope').selectOption('project')
    await page.getByTestId('bg-project').selectOption({ label: tree.projectName })
    await page.getByTestId('bg-role').selectOption(opts.role)
    await page.getByTestId('bg-ttl').fill(opts.ttl)
    await page.getByTestId('bg-reason').fill(opts.reason)
  }

  test('a reason is mandatory', async () => {
    await openBreakGlass(asColleague)
    await fillForm(asColleague, { role: 'admin', ttl: '5m', reason: '' })
    await asColleague.getByRole('button', { name: 'Break the glass' }).click()

    await expect(asColleague.getByTestId('bg-form-error')).toContainText(
      /reason is required — it is stamped into the audit chain/i,
    )
    await expect(asColleague.getByTestId('bg-grants')).toHaveCount(0)
  })

  test('you cannot elevate sideways into a role you already hold', async () => {
    await openBreakGlass(asColleague)
    // This account is a developer at instance scope, so `developer` on the
    // project is exactly what it already has. Nothing to break the glass for.
    await fillForm(asColleague, { role: 'developer', ttl: '5m', reason: REASON })
    await asColleague.getByRole('button', { name: 'Break the glass' }).click()

    // The screen reports a refusal, but genericises it: `errorMessage()` maps
    // every `validation` code to "Please check your input." so internals are
    // never echoed back. That keeps the UI honest and this assertion shallow…
    await expect(asColleague.getByTestId('bg-form-error')).toBeVisible()
    await expect(asColleague.getByTestId('bg-grants')).toHaveCount(0)

    // …so the actual guard is pinned against the API, where the reason survives.
    const refused = await asColleague.request.post('/v1/break-glass', {
      data: {
        scope_level: 'project',
        project_id: tree.projectId,
        role: 'developer',
        reason: REASON,
        ttl: '5m',
      },
    })
    expect(refused.status(), 'a sideways elevation was accepted').toBe(400)
    expect(await refused.text()).toContain('must raise your role above')
  })

  test('activating a time-boxed elevation announces it and lists the grant', async () => {
    await openBreakGlass(asColleague)
    await fillForm(asColleague, { role: 'admin', ttl: '5m', reason: REASON })
    await asColleague.getByRole('button', { name: 'Break the glass' }).click()

    await expect(asColleague.getByTestId('bg-banner')).toContainText(/Break-glass activated/i)

    const row = asColleague.locator('[data-testid="bg-grants"] tbody tr').filter({ hasText: REASON })
    await expect(row).toHaveCount(1)
    await expect(row).toContainText(`Project · ${tree.projectName}`)
    await expect(row).toContainText('admin')
    // Time-boxed: a live countdown, not an open-ended grant.
    await expect(row.locator('.mono')).toHaveText(/^(\d+h \d+m|\d+m \d+s|\d+s)$/)

    // The elevation is real, not cosmetic: this developer can now do something
    // only an admin may do on this project — mark a config protected.
    const protect = await asColleague.request.put(
      `/v1/configs/${tree.configId}/require-approval`,
      { data: { enabled: true } },
    )
    expect(protect.status(), 'the elevated role did not take effect').toBe(200)
  })

  test('the activation is stamped into the audit ledger', async ({ page }) => {
    await page.goto('/audit')
    await expect(page.getByRole('heading', { name: /Audit ledger/i })).toBeVisible()
    await page.locator('input.search').fill('breakglass.activate')

    const row = page
      .locator('table.ledger tbody tr', { hasText: 'breakglass.activate' })
      .filter({ hasText: `break-glass/project/${tree.projectId}` })
    await expect(row, 'the break-glass activation was not audited').toHaveCount(1)

    // The reason rides along in the event detail — that is the point of making
    // it mandatory. The role and the expiry are there too; no value ever is.
    await expect(row).toContainText(REASON)
    await expect(row).toContainText('role=admin')

    // ...and the chain is still intact after a loud, privileged write. Matched
    // by event count so this hits the ledger's stamp and not the shell's bare
    // "Chain verified" chip, which would be a strict-mode violation.
    await expect(page.getByText(/Chain verified · [\d,]+ events/i)).toBeVisible()
  })

  test('the grant can be ended early', async () => {
    await openBreakGlass(asColleague)
    const row = asColleague.locator('[data-testid="bg-grants"] tbody tr').filter({ hasText: REASON })
    await expect(row).toHaveCount(1)

    await row.getByRole('button', { name: 'End' }).click()
    await confirmDialog(asColleague, 'End now')

    await expect(asColleague.getByTestId('bg-banner')).toContainText(/Grant ended/i)
    await expect(row).toHaveCount(0)

    // The elevation really is gone: the admin-only call now fails again.
    const protect = await asColleague.request.put(
      `/v1/configs/${tree.configId}/require-approval`,
      { data: { enabled: false } },
    )
    expect(protect.status(), 'the revoked elevation still granted admin').toBe(403)
  })
})
