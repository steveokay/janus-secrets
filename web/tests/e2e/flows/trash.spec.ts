import { test, expect, type Page } from '@playwright/test'
import {
  confirmDialog,
  createEnvironment,
  createTree,
  ownerRequest,
  stamp,
  type Tree,
} from '../helpers/janus'

/**
 * Trash — soft-delete → restore → soft-delete → destroy, for a config and for
 * an environment.
 *
 * This is the spec that motivated the file. `destroy` returned 404 for every
 * config and every environment in the Trash: both delete handlers resolved the
 * authorization scope through a LIVE-ONLY repo read, and anything reachable
 * from Trash is by definition already soft-deleted, so the lookup failed before
 * the authorization check ever ran. A documented button could never work, for
 * anyone, including an owner. Fixed in PR #191 — with neither an API test nor an
 * E2E test to have caught it.
 *
 * The assertions here are therefore about the OUTCOME, never the click:
 *
 *   • the row leaves the Trash and stays gone across a full reload. The broken
 *     build re-rendered it: `act()` in Trash.svelte funnels a failed call into
 *     an "Action failed." flash and then reloads the list unchanged.
 *   • restoring afterwards 404s. That is the assertion the old behaviour cannot
 *     satisfy under any reading — if destroy silently did nothing, the row is
 *     still merely soft-deleted and restore returns 200. Anything you can
 *     restore was not destroyed.
 *
 * Soft-delete does not cascade, so a config and its environment are deleted
 * independently; both are exercised, plus a project (the third kind Trash
 * lists, and the one the bug did NOT affect — `handleProjectDelete` builds its
 * scope from the URL param and never reads the row).
 */

test.describe.serial('Trash — restore and destroy', () => {
  let tree: Tree
  let envId = ''
  const envSlug = 'staging'

  test.beforeAll(async ({ playwright }) => {
    // Scaffolding via the API. The lifecycle below is what is under test;
    // clicking a project into existence again proves nothing smoke.spec.ts has
    // not already proved and only adds ways to be flaky.
    const request = await ownerRequest(playwright.request)
    tree = await createTree(request, { slug: stamp('trash'), envSlug: 'dev', configName: 'default' })
    envId = (await createEnvironment(request, tree.projectId, envSlug)).id
    await request.dispose()
  })

  /** The Trash row of a given kind, located by this run's unique project slug. */
  function trashRow(page: Page, kind: 'configs' | 'environments' | 'projects') {
    return page.locator(`[data-testid="trash-${kind}"] tbody tr`).filter({ hasText: tree.projectSlug })
  }

  async function openTrash(page: Page): Promise<void> {
    await page.goto('/trash')
    await expect(page.getByRole('heading', { name: /^Trash$/ })).toBeVisible()
  }

  /** The editor's / board's own Delete, never a per-row one. */
  function headerDelete(page: Page) {
    return page.locator('.head-actions').getByRole('button', { name: 'Delete', exact: true })
  }

  function envColumn(page: Page) {
    return page.getByRole('group', { name: new RegExp(`^${envSlug} environment`) })
  }

  /* ── config ──────────────────────────────────────────────── */

  test('soft-deleting a config from its editor lands it in the Trash', async ({ page }) => {
    await page.goto(`/projects/${tree.projectId}/configs/${tree.configId}`)
    await expect(page.getByRole('heading', { name: tree.configName })).toBeVisible()

    await headerDelete(page).click()
    await confirmDialog(page, 'Move to trash')

    // Deleting bounces out of the editor. NOTE: it lands on the registry, not
    // on the dossier the code aims for — `ctx` is derived from the registry and
    // the re-hydration that precedes the redirect has already dropped the
    // now-deleted config, so the `ctx ? … : '/projects'` fallback wins. Harmless,
    // but pinned here so the assertion documents what actually happens.
    await expect(page).toHaveURL(/\/projects$/)

    // ...and the card is gone from the dossier.
    await page.goto(`/projects/${tree.projectId}`)
    await expect(page.getByRole('link', { name: tree.configName })).toHaveCount(0)

    await openTrash(page)
    await expect(trashRow(page, 'configs')).toHaveCount(1)
    await expect(trashRow(page, 'configs')).toContainText(tree.configName)
  })

  test('restoring the config undeletes it in place', async ({ page }) => {
    await openTrash(page)
    await trashRow(page, 'configs').getByRole('button', { name: 'Restore' }).click()

    await expect(page.getByTestId('trash-note')).toHaveText(`Restored ${tree.configName}.`)
    await expect(trashRow(page, 'configs')).toHaveCount(0)

    // Really back, not merely absent from the Trash: the board lists it again.
    await page.goto(`/projects/${tree.projectId}`)
    await expect(page.getByRole('link', { name: tree.configName })).toBeVisible()
  })

  test('destroying the config removes it permanently', async ({ page }) => {
    // Soft-delete it again so there is something to destroy. Wait for the
    // redirect before navigating on: leaving the page while the DELETE is still
    // in flight races the Trash listing that follows.
    await page.goto(`/projects/${tree.projectId}/configs/${tree.configId}`)
    await headerDelete(page).click()
    await confirmDialog(page, 'Move to trash')
    await expect(page).toHaveURL(/\/projects$/)

    await openTrash(page)
    const row = trashRow(page, 'configs')
    await expect(row).toHaveCount(1)
    await row.getByRole('button', { name: 'Destroy' }).click()
    await confirmDialog(page, 'Destroy')

    // A failure lands in this same element with the server's message, so
    // asserting the exact success text also rules the failure out.
    await expect(page.getByTestId('trash-note')).toHaveText(`Destroyed ${tree.configName}.`)
    await expect(row).toHaveCount(0)

    // ...and it stays gone across a full reload.
    await openTrash(page)
    await expect(row).toHaveCount(0)

    const restore = await page.request.post(`/v1/configs/${tree.configId}/restore`, { data: {} })
    expect(
      restore.status(),
      'the config could still be restored — destroy did not hard-delete it',
    ).toBe(404)
  })

  /* ── environment ─────────────────────────────────────────── */

  test('soft-deleting an environment from the board lands it in the Trash', async ({ page }) => {
    await page.goto(`/projects/${tree.projectId}`)
    await expect(envColumn(page)).toBeVisible()
    await envColumn(page).getByTitle('Move to trash').click()
    await confirmDialog(page, 'Move to trash')
    await expect(envColumn(page)).toHaveCount(0)

    await openTrash(page)
    await expect(trashRow(page, 'environments')).toHaveCount(1)
    await expect(trashRow(page, 'environments')).toContainText(envSlug)
  })

  test('restoring the environment undeletes it in place', async ({ page }) => {
    await openTrash(page)
    await trashRow(page, 'environments').getByRole('button', { name: 'Restore' }).click()

    await expect(page.getByTestId('trash-note')).toHaveText(`Restored ${envSlug}.`)
    await expect(trashRow(page, 'environments')).toHaveCount(0)

    await page.goto(`/projects/${tree.projectId}`)
    await expect(envColumn(page)).toBeVisible()
  })

  test('destroying the environment removes it permanently', async ({ page }) => {
    await page.goto(`/projects/${tree.projectId}`)
    await envColumn(page).getByTitle('Move to trash').click()
    await confirmDialog(page, 'Move to trash')
    // The column leaving the board is the signal that the DELETE landed and the
    // registry re-hydrated. Navigating before that races the Trash listing.
    await expect(envColumn(page)).toHaveCount(0)

    await openTrash(page)
    const row = trashRow(page, 'environments')
    await expect(row).toHaveCount(1)
    await row.getByRole('button', { name: 'Destroy' }).click()
    await confirmDialog(page, 'Destroy')

    await expect(page.getByTestId('trash-note')).toHaveText(`Destroyed ${envSlug}.`)
    await expect(row).toHaveCount(0)

    await openTrash(page)
    await expect(row).toHaveCount(0)

    const restore = await page.request.post(
      `/v1/projects/${tree.projectId}/environments/${envId}/restore`,
      { data: {} },
    )
    expect(
      restore.status(),
      'the environment could still be restored — destroy did not hard-delete it',
    ).toBe(404)
  })

  /* ── the confirm gate ────────────────────────────────────── */

  test('cancelling the destroy confirmation leaves the project alone', async ({ page }) => {
    await page.goto(`/projects/${tree.projectId}`)
    await headerDelete(page).click()
    await confirmDialog(page, 'Move to trash')
    await expect(page).toHaveURL(/\/projects$/)

    await openTrash(page)
    const row = trashRow(page, 'projects')
    await expect(row).toHaveCount(1)

    await row.getByRole('button', { name: 'Destroy' }).click()
    const modal = page.locator('div.modal[role="dialog"]')
    await expect(modal).toContainText(/cannot be undone/i)
    await modal.getByRole('button', { name: 'Cancel' }).click()
    await expect(modal).toBeHidden()

    // Still there — cancelling must not act — and still destroyable.
    await expect(row).toHaveCount(1)
    await row.getByRole('button', { name: 'Destroy' }).click()
    await confirmDialog(page, 'Destroy')
    await expect(page.getByTestId('trash-note')).toHaveText(`Destroyed ${tree.projectSlug}.`)
    await expect(row).toHaveCount(0)
  })
})
