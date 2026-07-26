import { test as setup, expect } from '@playwright/test'
import {
  bindInstanceRole,
  colleagueRequest,
  createUser,
  mintSession,
  ownerRequest,
  readOwnerCredentials,
  saveColleagueCredentials,
  shellNav,
} from './helpers/janus'
import { COLLEAGUE_STATE, OWNER_STATE } from './helpers/paths'

/**
 * Bridges the first-run ceremony to the flow specs.
 *
 * Runs once, after `smoke.spec.ts` has initialized and unsealed the server (the
 * `setup` project depends on `ceremony`) and before anything in
 * `tests/e2e/flows/` (which depends on this one). It produces two signed-in
 * storage states, so that every later browser context is handed a session
 * cookie instead of typing a password into a rate-limited login gate.
 *
 * It also mints the SECOND account. Four-eyes approval is not testable with one
 * user — the whole control is "somebody else has to say yes" — and break-glass
 * refuses to elevate anyone who does not already hold a strictly lower role on
 * the scope, which the bootstrapped instance owner never does.
 */

setup('turn the one-time owner password into a reusable session', async ({ playwright, browser }) => {
  await mintSession(playwright.request, readOwnerCredentials(), OWNER_STATE)

  // Prove the state is actually usable before anything depends on it: a stale
  // or malformed one would otherwise surface as every flow spec landing on the
  // login gate and timing out on an unrelated locator.
  const context = await browser.newContext({ storageState: OWNER_STATE })
  const page = await context.newPage()
  await page.goto('/')
  await expect(shellNav(page), 'the saved owner session did not sign us in').toBeVisible()
  await context.close()
})

setup('create the second user, bind it, and save its session', async ({ playwright }) => {
  // Act as the owner we just saved, so the user-create and role-bind calls are
  // authorized without spending another login.
  const asOwner = await ownerRequest(playwright.request)

  const email = `colleague-${Date.now().toString(36)}@janus.test`
  const created = await createUser(asOwner, email)
  expect(created.password, 'the one-time password was not returned').toBeTruthy()

  // Developer at instance scope. Two properties matter downstream:
  //   • secret:write everywhere, so this account can file — and then be refused
  //     approval of — an edit request on a protected config;
  //   • strictly below owner, so break-glass has something to elevate FROM.
  //     Activation is denied outright for anyone who does not already hold a
  //     lower role on the scope, which is why the owner cannot stand in here.
  await bindInstanceRole(asOwner, created.id, 'developer')
  await asOwner.dispose()

  saveColleagueCredentials({ email: created.email, password: created.password })
  await mintSession(playwright.request, { email: created.email, password: created.password }, COLLEAGUE_STATE)

  const asColleague = await colleagueRequest(playwright.request)
  const me = await asColleague.get('/v1/auth/me')
  expect(me.ok(), 'the saved second-user session is not authenticated').toBe(true)
  await asColleague.dispose()
})
