import { test, expect, type BrowserContext, type Page } from '@playwright/test'
import { createTree, ownerRequest, stamp, type Tree } from '../helpers/janus'
import { COLLEAGUE_STATE } from '../helpers/paths'

/**
 * Protected configs — four-eyes approval, and the refusal to self-approve.
 *
 * This is a security control with a bypass history: PR #148 fixed two ways to
 * get a change into a protected config without a second pair of eyes (rollback
 * and promote-apply both routed around the gate), and the approve handler
 * carries two separate self-approval checks for the same reason. It is worth
 * testing from the outside, through the UI, where the property is stated: "a
 * DIFFERENT reviewer must approve".
 *
 * The flow needs two accounts, so the requester half runs in a second browser
 * context signed in as the developer minted by auth.setup.ts, and the reviewer
 * half as the owner (this project's default storage state).
 *
 * The load-bearing assertions:
 *   • a save on a protected config does NOT commit — the folio version is
 *     unchanged and the key is not in the config afterwards;
 *   • the requester cannot approve, or reject, their own request — as a user who
 *     genuinely holds secret:write, so the refusal is the four-eyes rule and not
 *     a missing permission;
 *   • somebody else can, and only then does a version appear.
 *
 * A note on navigation. The editor reads the protected flag out of the registry
 * store once, when it loads, and does not wait for the registry to hydrate. Deep
 * -linking straight to /projects/:pid/configs/:cid therefore loses the race and
 * renders the config as unprotected — no banner, and a Save button that still
 * says "Save as vN". (The SERVER is unaffected: the save still comes back as a
 * pending request, so the control holds. It is the operator who is misled.)
 * Every navigation below goes through the dossier so the registry is already
 * populated, which is also how a person reaches a config.
 */

test.describe.serial('Protected configs and four-eyes approval', () => {
  let tree: Tree
  let colleague: BrowserContext
  let asColleague: Page
  const SECRET_KEY = 'FOUR_EYES_KEY'
  const SECRET_VALUE = 'should-not-land-yet'

  test.beforeAll(async ({ playwright, browser }) => {
    const request = await ownerRequest(playwright.request)
    tree = await createTree(request, { slug: stamp('approve'), envSlug: 'prod', configName: 'default' })
    // A first committed version, so "the version did not move" is a meaningful
    // statement rather than an artefact of an empty config.
    const seed = await request.put(`/v1/configs/${tree.configId}/secrets`, {
      data: { message: 'seed', changes: [{ key: 'BASELINE', value: 'baseline' }] },
    })
    expect(seed.ok(), `seeding a secret → ${seed.status()}`).toBe(true)
    await request.dispose()

    colleague = await browser.newContext({ storageState: COLLEAGUE_STATE })
    asColleague = await colleague.newPage()
  })

  test.afterAll(async () => {
    await colleague?.close()
  })

  /** Reaches the editor the way a person does: through the dossier. */
  async function openEditor(page: Page): Promise<void> {
    await page.goto(`/projects/${tree.projectId}`)
    const card = page.getByRole('link', { name: tree.configName })
    await expect(card).toBeVisible()
    await card.click()
    await expect(page.getByRole('heading', { name: tree.configName })).toBeVisible()
    // The folio chip renders "FOL. v0" until the version list arrives. Waiting
    // for it to settle is what makes a later read of it meaningful — every
    // config here is seeded, so v0 always means "still loading".
    await expect(page.locator('.ver-chip')).not.toHaveText(/\bv0\b/)
    await expect(page.locator('table.ledger tbody tr', { hasText: 'BASELINE' })).toHaveCount(1)
  }

  /** The "FOL. vN" chip in the editor header. */
  async function folioVersion(page: Page): Promise<number> {
    return Number(/v(\d+)/.exec(await page.locator('.ver-chip').innerText())![1])
  }

  test('an owner can mark the config protected', async ({ page }) => {
    await openEditor(page)
    await expect(page.getByRole('note'), 'the config started out protected').toHaveCount(0)

    await page.getByRole('button', { name: /^Protect…$/ }).click()
    await expect(page.getByText(/now protected — direct saves become approval requests/i)).toBeVisible()

    // The standing banner is what a user actually navigates by.
    await expect(page.getByRole('note')).toContainText(/Protected config/i)
    await expect(page.getByRole('note')).toContainText(/different.*reviewer must approve/i)
    await expect(page.getByRole('button', { name: '🛡 Protected' })).toBeVisible()
  })

  test('a save by the developer becomes a pending request, not a commit', async () => {
    await openEditor(asColleague)
    const before = await folioVersion(asColleague)

    // The protection is visible to the second user too, before they type
    // anything — they are told the save will not commit.
    await expect(asColleague.getByRole('note')).toContainText(/Protected config/i)

    await asColleague.getByRole('button', { name: '+ Add secret' }).click()
    await asColleague.getByTestId('new-key-input').fill(SECRET_KEY)
    await asColleague.getByTestId('value-input').fill(SECRET_VALUE)
    await asColleague.getByTestId('new-key-input').click() // blur the value out

    // The button itself states the outcome — a protected config never offers
    // "Save as vN".
    const save = asColleague.getByTestId('save-secrets')
    await expect(save).toHaveText(/Submit for approval/i)
    await save.click()

    await expect(asColleague.getByText(/Changes submitted for approval/i)).toBeVisible()

    // Nothing was committed: the folio has not moved and the key is not there.
    await openEditor(asColleague)
    expect(await folioVersion(asColleague), 'the protected save committed a version').toBe(before)
    await expect(asColleague.locator('table.ledger tbody tr', { hasText: SECRET_KEY })).toHaveCount(0)
  })

  test('the requester cannot approve their own request', async () => {
    await openEditor(asColleague)
    const before = await folioVersion(asColleague)

    await asColleague.getByRole('button', { name: /Review pending \(1\)/ }).click()
    const panel = asColleague.getByRole('region', { name: 'Pending edit requests' })
    await expect(panel).toBeVisible()
    await expect(panel.locator('tbody tr')).toContainText(SECRET_KEY)
    // Value-free: the request names keys, never values.
    await expect(panel).not.toContainText(SECRET_VALUE)

    await panel.getByRole('button', { name: 'Approve' }).click()

    // Refused, in as many words. This account holds secret:write on the config
    // (it is a developer), so the only thing standing in the way is four-eyes.
    await expect(
      asColleague.getByTestId('reveal-toast'),
      'the requester was allowed to approve their own request',
    ).toContainText(/cannot approve your own request/i)

    // Still pending, still uncommitted.
    await openEditor(asColleague)
    expect(await folioVersion(asColleague)).toBe(before)
    await expect(asColleague.getByRole('button', { name: /Review pending \(1\)/ })).toBeVisible()
  })

  test('rejecting your own request is refused too', async () => {
    await openEditor(asColleague)
    await asColleague.getByRole('button', { name: /Review pending \(1\)/ }).click()
    const panel = asColleague.getByRole('region', { name: 'Pending edit requests' })
    await panel.getByRole('button', { name: 'Reject' }).click()

    await expect(asColleague.getByTestId('reveal-toast')).toContainText(/cannot reject your own request/i)

    await openEditor(asColleague)
    await expect(asColleague.getByRole('button', { name: /Review pending \(1\)/ })).toBeVisible()
  })

  test('a different reviewer approves it from the Approvals screen', async ({ page }) => {
    await page.goto('/approvals')
    await expect(page.getByRole('heading', { name: /^Approvals$/ })).toBeVisible()
    await page.locator('.head-actions select').selectOption({ label: tree.projectName })

    const table = page.locator('[data-testid="edit-requests-table"]')
    const row = table.locator('tbody tr').filter({ hasText: SECRET_KEY })
    await expect(row).toHaveCount(1)
    await expect(row).toContainText('pending')
    await expect(table, 'a value leaked into the approvals queue').not.toContainText(SECRET_VALUE)

    await row.getByRole('button', { name: 'Approve' }).click()
    await expect(page.getByText(/Approved — committed as v\d+/)).toBeVisible()

    // Now — and only now — the change is in the config.
    await openEditor(page)
    await expect(page.locator('table.ledger tbody tr', { hasText: SECRET_KEY })).toHaveCount(1)
    expect(await folioVersion(page), 'approval did not commit a new version').toBeGreaterThan(1)
  })

  test('unprotecting the config restores direct saves', async ({ page }) => {
    await openEditor(page)
    await page.getByRole('button', { name: '🛡 Protected' }).click()
    await expect(page.getByText(/Protection removed — saves commit directly again/i)).toBeVisible()
    await expect(page.getByRole('note')).toHaveCount(0)

    // The save bar only exists while there are uncommitted amendments, so stage
    // one to read the button back: it offers a direct commit again.
    await page.getByRole('button', { name: '+ Add secret' }).click()
    await page.getByTestId('new-key-input').fill('AFTER_UNPROTECT')
    await page.getByTestId('value-input').fill('committed-directly')
    await page.getByTestId('new-key-input').click()
    await expect(page.getByTestId('save-secrets')).toHaveText(/Save as v\d+/)

    await page.getByTestId('save-secrets').click()
    await expect(page.getByText(/Committed — v\d+/i)).toBeVisible()
  })
})
