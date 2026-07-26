import { test, expect } from '@playwright/test'
import { confirmDialog, createTree, ownerRequest, stamp, tokenRequest, type Tree } from '../helpers/janus'

/**
 * Service tokens — mint scoped, shown exactly once, revoke.
 *
 * "Shown exactly once" is a security property, not a nicety: Janus stores only
 * the HMAC-SHA256 of a token, so if the plaintext could be read back from any
 * later response the storage design would be moot. This spec asserts it can
 * not — in the rendered page AND in the list endpoint the page is built from,
 * which is where a regression would actually live.
 *
 * The minted value is also used for real, against a config it is scoped to,
 * before and after revocation. A token that is never presented proves nothing
 * about whether minting works; a revocation that is never re-tested proves
 * nothing about whether revoking works.
 */

test.describe.serial('Service tokens', () => {
  let tree: Tree
  let token = ''
  const tokenName = stamp('e2e-runtime')

  test.beforeAll(async ({ playwright }) => {
    const request = await ownerRequest(playwright.request)
    tree = await createTree(request, { slug: stamp('tokens'), envSlug: 'dev', configName: 'default' })
    // Something for a read-scoped token to actually read.
    const res = await request.put(`/v1/configs/${tree.configId}/secrets`, {
      data: { message: 'seed', changes: [{ key: 'SEEDED', value: 'seed-value' }] },
    })
    expect(res.ok(), `seeding a secret → ${res.status()}`).toBe(true)
    await request.dispose()
  })

  test('minting a config-scoped token shows the value exactly once', async ({ page, playwright }) => {
    await page.goto('/tokens')
    await expect(page.getByRole('heading', { name: /Service tokens/i })).toBeVisible()

    await page.getByRole('button', { name: '+ Mint token' }).click()
    await page.locator('#tk-name').fill(tokenName)
    await page.locator('#tk-kind').selectOption('config')
    await page.locator('#tk-scope').selectOption({
      label: `${tree.projectName} / ${tree.environmentSlug} / ${tree.configName}`,
    })
    await page.locator('#tk-access').selectOption('read')
    await page.getByRole('button', { name: 'Mint', exact: true }).click()

    await expect(page.getByText(/Minted — shown exactly once/i)).toBeVisible()
    token = (await page.getByTestId('minted-token').innerText()).trim()
    expect(token, 'the minted token does not look like a Janus token').toMatch(/^janus_[a-z]+_\S+$/)

    // It is a real credential, on its own, with no cookie in sight.
    const asToken = await tokenRequest(playwright.request, token)
    const read = await asToken.get(`/v1/configs/${tree.configId}/secrets`)
    expect(read.status(), 'the freshly minted token could not read its own config').toBe(200)
    await asToken.dispose()
  })

  test('the token value is not retrievable afterwards', async ({ page }) => {
    await page.goto('/tokens')
    const row = page.locator('[data-testid="tokens-table"] tbody tr').filter({ hasText: tokenName })
    await expect(row).toHaveCount(1)
    await expect(row).toContainText('read')

    // Not in the rendered page…
    await expect(page.locator('body')).not.toContainText(token)

    // …and, more to the point, not in the response the page is built from.
    // Checking only the DOM would pass even if the API started echoing it.
    const list = await page.request.get('/v1/tokens')
    expect(list.ok()).toBe(true)
    const body = await list.text()
    expect(body, 'the token list echoed the plaintext token').not.toContain(token)
    expect(body, 'sanity: the token we minted is not in the list at all').toContain(tokenName)
  })

  test('revoking the token withdraws access immediately', async ({ page, playwright }) => {
    await page.goto('/tokens')
    const row = page.locator('[data-testid="tokens-table"] tbody tr').filter({ hasText: tokenName })
    await row.getByRole('button', { name: 'Revoke' }).click()
    await confirmDialog(page, 'Revoke')

    // The list shows active tokens only, so a revoked one leaves it.
    await expect(row).toHaveCount(0)

    const asToken = await tokenRequest(playwright.request, token)
    const read = await asToken.get(`/v1/configs/${tree.configId}/secrets`)
    expect(read.status(), 'the revoked token still authenticated').toBe(401)
    await asToken.dispose()
  })

  test('cancelling the revoke confirmation leaves the token alone', async ({ page }) => {
    const secondName = stamp('e2e-keepme')
    await page.goto('/tokens')
    await page.getByRole('button', { name: '+ Mint token' }).click()
    await page.locator('#tk-name').fill(secondName)
    await page.locator('#tk-kind').selectOption('environment')
    await page.locator('#tk-scope').selectOption({
      label: `${tree.projectName} / ${tree.environmentSlug}`,
    })
    await page.getByRole('button', { name: 'Mint', exact: true }).click()
    await expect(page.getByTestId('minted-token')).toBeVisible()
    await page.getByRole('button', { name: 'Done' }).click()

    const row = page.locator('[data-testid="tokens-table"] tbody tr').filter({ hasText: secondName })
    await expect(row).toHaveCount(1)
    await row.getByRole('button', { name: 'Revoke' }).click()

    const modal = page.locator('div.modal[role="dialog"]')
    await expect(modal).toContainText(/cannot be undone/i)
    await modal.getByRole('button', { name: 'Cancel' }).click()
    await expect(modal).toBeHidden()

    await expect(row).toHaveCount(1)
    await page.reload()
    await expect(page.locator('[data-testid="tokens-table"] tbody tr').filter({ hasText: secondName })).toHaveCount(1)
  })
})
