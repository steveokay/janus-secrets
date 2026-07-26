import { test, expect, type CDPSession, type Page } from '@playwright/test'

/**
 * Flagship end-to-end smoke suite for Janus.
 *
 * Drives the first-run ceremony and the core secrets path entirely through the
 * embedded Atrium SPA, against a *running* stack (server + Postgres). Nothing is
 * stubbed — every step exercises the real /v1 API behind the UI:
 *
 *   init (Shamir 5/3 + first registrar)
 *     → capture the one-time shares + admin password the UI surfaces exactly once
 *     → unseal by presenting the quorum of shares
 *     → log in as the bootstrapped owner
 *     → create a project
 *     → add an environment + a config
 *     → save a secret (batched dirty-state commit → one immutable config version)
 *     → audited reveal of the masked value
 *     → confirm the reveal produced a `secret.reveal` event in the audit ledger
 *
 * This suite REQUIRES a fresh, uninitialized server: the init ceremony runs
 * exactly once per server lifetime. The opt-in CI job (.github/workflows/e2e.yml)
 * brings up a clean docker-compose stack so init is available. Re-running against
 * an already-initialized server will skip the init assertions and fail — bring up
 * a fresh stack (docker compose down -v && docker compose up -d --build).
 */

const SHARES = 5
const THRESHOLD = 3

// Unique-ish names so a re-run against the same (freshly re-created) stack, or a
// human poking at the instance, is unlikely to collide.
const stamp = Date.now().toString(36)
const ADMIN_EMAIL = `owner-${stamp}@janus.test`
const PROJECT_SLUG = `smoke-${stamp}`
const PROJECT_NAME = `Smoke ${stamp}`
const ENV_SLUG = 'dev'
const CONFIG_NAME = 'default'
const SECRET_KEY = 'API_TOKEN'
const SECRET_VALUE = `s3cr3t-${stamp}`

test.describe.serial('Janus flagship smoke', () => {
  let page: Page
  let shares: string[] = []
  let adminPassword = ''
  let cdp: CDPSession
  let authenticatorId = ''

  test.beforeAll(async ({ browser }) => {
    page = await browser.newPage()
  })

  test.afterAll(async () => {
    await page.close()
  })

  test('init ceremony surfaces one-time shares + admin password', async () => {
    await page.goto('/')

    // A fresh server lands on the init ceremony. If we instead see the unseal or
    // login gate, the server was already initialized — fail loudly with guidance.
    const initHeading = page.getByRole('heading', { name: /Found the registry/i })
    await expect(
      initHeading,
      'Expected a FRESH, uninitialized server. Bring up a clean stack: ' +
        'docker compose down -v && docker compose up -d --build',
    ).toBeVisible()

    await page.locator('#shares').fill(String(SHARES))
    await page.locator('#threshold').fill(String(THRESHOLD))
    await page.locator('#email').fill(ADMIN_EMAIL)
    await page.getByRole('button', { name: /Initialize the vault/i }).click()

    // The ceremony reveals the material exactly once.
    await expect(page.getByTestId('init-shares')).toBeVisible()
    shares = await page.getByTestId('init-share').allInnerTexts()
    expect(shares.length).toBe(SHARES)
    for (const s of shares) expect(s.trim().length).toBeGreaterThan(0)

    adminPassword = (await page.getByTestId('init-admin-password').innerText()).trim()
    expect(adminPassword.length).toBeGreaterThan(0)
    await expect(page.getByTestId('init-admin-email')).toHaveText(ADMIN_EMAIL)

    // Acknowledge and proceed to the unseal gate.
    await page.getByRole('checkbox').check()
    await page.getByRole('button', { name: /Proceed to unseal/i }).click()
  })

  test('unseal by presenting the Shamir quorum', async () => {
    await expect(page.getByRole('heading', { name: /The vault is sealed/i })).toBeVisible()

    // Present THRESHOLD shares, one at a time. Each submission clears the input.
    for (let i = 0; i < THRESHOLD; i++) {
      const input = page.locator('#share')
      await expect(input).toBeVisible()
      await input.fill(shares[i])
      await page.getByRole('button', { name: /Present share/i }).click()
      // After each accepted share the input is cleared; after the last one the
      // form is replaced by the "Unsealed" stamp.
      if (i < THRESHOLD - 1) {
        await expect(input).toHaveValue('')
      }
    }

    await expect(page.getByText(/Unsealed — master key reconstructed/i)).toBeVisible()
    // The store transitions to the login gate ~1.4s after the quorum is reached.
    await expect(page.getByRole('heading', { name: /Enter the\s*atrium/i })).toBeVisible({
      timeout: 15_000,
    })
  })

  test('log in as the bootstrapped owner', async () => {
    await page.locator('#email').fill(ADMIN_EMAIL)
    await page.locator('#pw').fill(adminPassword)
    await page.getByRole('button', { name: /Sign the register/i }).click()

    // Landing in the shell: the nav (with a Projects tab) replaces the login gate.
    const projectsNav = page.locator('nav').getByRole('link', { name: 'Projects' })
    await expect(projectsNav).toBeVisible()
    await projectsNav.click()
    await expect(page.getByRole('heading', { name: /^Projects$/ })).toBeVisible()
  })

  test('create a project', async () => {
    await page.getByRole('button', { name: /New project/i }).click()
    await page.locator('#np-slug').fill(PROJECT_SLUG)
    await page.locator('#np-name').fill(PROJECT_NAME)
    await page.getByRole('button', { name: /Open dossier/i }).click()

    // The new dossier appears in the registry; open it.
    const dossier = page.getByRole('link', { name: new RegExp(PROJECT_NAME, 'i') })
    await expect(dossier).toBeVisible()
    await dossier.click()

    await expect(page.getByRole('heading', { name: new RegExp(PROJECT_NAME, 'i') })).toBeVisible()
  })

  test('add an environment and a config', async () => {
    // + Environment → slug → Create
    await page.getByRole('button', { name: /\+ Environment/i }).click()
    await page.locator('#env-slug').fill(ENV_SLUG)
    await page.getByRole('button', { name: /^Create$/ }).click()

    // The env column appears with a "+ config" affordance.
    const addConfig = page.getByRole('button', { name: /\+ config/i }).first()
    await expect(addConfig).toBeVisible()
    await addConfig.click()

    // The inline config form: name input + Add.
    const cfgInput = page.locator('form.cfg-form input')
    await expect(cfgInput).toBeVisible()
    await cfgInput.fill(CONFIG_NAME)
    await page.getByRole('button', { name: /^Add$/ }).click()

    // The config card is now a link into the secret editor.
    const cfgCard = page.getByRole('link', { name: new RegExp(CONFIG_NAME, 'i') })
    await expect(cfgCard).toBeVisible()
    await cfgCard.click()

    await expect(page.getByRole('heading', { name: new RegExp(CONFIG_NAME, 'i') })).toBeVisible()
  })

  test('save a secret as a config version', async () => {
    // + Add secret stages a new draft row.
    await page.getByRole('button', { name: /\+ Add secret/i }).click()

    await page.getByTestId('new-key-input').fill(SECRET_KEY)
    await page.getByTestId('value-input').fill(SECRET_VALUE)
    // Commit the value edit out of the textarea (blur) so the row is dirty.
    await page.getByTestId('new-key-input').click()

    // Save the batched draft as one immutable version.
    const save = page.getByTestId('save-secrets')
    await expect(save).toBeEnabled()
    await save.click()

    // "Committed — vN" stamp confirms the version was written.
    await expect(page.getByText(/Committed — v\d+/i)).toBeVisible()
  })

  test('audited reveal of the masked value', async () => {
    // After save the row reloads masked. Reveal it.
    const reveal = page.getByTestId('reveal-btn').first()
    await expect(reveal).toBeVisible()
    await reveal.click()

    // The plaintext is now shown, and a toast confirms the reveal was audited.
    await expect(page.getByText(SECRET_VALUE, { exact: false })).toBeVisible()
    await expect(page.getByTestId('reveal-toast')).toContainText(/recorded in the audit ledger/i)
  })

  test('the reveal produced a secret.reveal audit event', async () => {
    await page.goto('/audit')
    await expect(page.getByRole('heading', { name: /Audit ledger/i })).toBeVisible()

    // Filter the ledger to reveal events touching our key, then assert one exists.
    await page.locator('input.search').fill('secret.reveal')

    const revealRow = page
      .locator('table.ledger tbody tr', { hasText: 'secret.reveal' })
      .filter({ hasText: SECRET_KEY })
    await expect(revealRow.first()).toBeVisible()

    // The chain-verified badge should be present (the ledger is intact).
    await expect(page.getByText(/Chain verified/i)).toBeVisible()
  })
  // ---------------------------------------------------------------------------
  // Passkeys (WebAuthn).
  //
  // These steps exercise the ONE part of the passkey feature that Go tests
  // cannot reach: the browser half. The server side is covered by unit/e2e tests
  // driving a software authenticator, but nothing there proves the real
  // navigator.credentials ceremony works — that the RP ID matches, that the
  // origin check passes, that the browser accepts the options object Janus
  // emits, or that our client code round-trips the PublicKeyCredential.
  //
  // Chrome's CDP virtual authenticator provides a real WebAuthn implementation
  // with a scriptable authenticator, so the ceremony below is genuine: Chrome
  // performs the RP-ID/origin checks itself and would refuse a mismatch.
  //
  // Requires the server to be configured with JANUS_WEBAUTHN_RP_ID/ORIGINS (the
  // dev compose stack sets them); when it is not, the UI hides the passkey
  // controls and these tests fail with a clear message rather than silently
  // passing.
  //
  // A note on `automaticPresenceSimulation`. The login screen also starts a
  // CONDITIONAL (autofill) ceremony in the background. A real browser never
  // completes one without the user picking a passkey from the autofill list —
  // but the virtual authenticator approves every presence check instantly, so
  // with simulation left on it would win every race and no explicit button
  // would ever be exercised. So presence simulation is switched OFF while the
  // login screen is up, leaving the background ceremony pending exactly as it
  // would be in front of a real user, and switched on only to answer the
  // ceremony a click has just started.
  // ---------------------------------------------------------------------------

  /** Simulates (or withholds) the user's touch on the virtual authenticator. */
  async function setPresence(enabled: boolean) {
    await cdp.send('WebAuthn.setAutomaticPresenceSimulation', { authenticatorId, enabled })
  }

  /** Clicks a button that starts a passkey ceremony, then answers the prompt. */
  async function clickAndApprove(button: ReturnType<Page['getByRole']>) {
    await button.click()
    await setPresence(true)
    // Leave the authenticator inert again so the next login screen's background
    // conditional ceremony cannot resolve itself.
    await expect(page.locator('nav').getByRole('link', { name: 'Projects' })).toBeVisible()
    await setPresence(false)
  }

  test('register a passkey via the real browser ceremony', async () => {
    cdp = await page.context().newCDPSession(page)
    await cdp.send('WebAuthn.enable')
    const created = await cdp.send('WebAuthn.addVirtualAuthenticator', {
      options: {
        protocol: 'ctap2',
        transport: 'internal',
        hasResidentKey: true,
        // Janus sends userVerification:"required" on every ceremony, so an
        // authenticator that cannot verify the user would be rejected outright.
        hasUserVerification: true,
        isUserVerified: true,
        automaticPresenceSimulation: true,
      },
    })
    authenticatorId = created.authenticatorId

    await page.goto('/settings')
    const registerBtn = page.getByRole('button', { name: /Register a passkey/i })
    await expect(
      registerBtn,
      'passkey controls are hidden — is JANUS_WEBAUTHN_RP_ID/ORIGINS set on the server?',
    ).toBeVisible()
    await registerBtn.click()

    // The app's own prompt (never a native dialog) asks for a device nickname.
    const nickname = page.getByLabel(/Device name/i)
    await expect(nickname).toBeVisible()
    await nickname.fill('E2E virtual key')
    await page.getByRole('button', { name: /^Continue$/ }).click()

    // The credential is listed by nickname, and the authenticator really holds it.
    await expect(page.getByText('E2E virtual key')).toBeVisible()
    const { credentials } = await cdp.send('WebAuthn.getCredentials', { authenticatorId })
    expect(
      credentials.length,
      'the virtual authenticator holds no credential — the ceremony did not complete',
    ).toBe(1)
    // Janus enrols with residentKey:"required", so the credential MUST be
    // discoverable — that is the whole precondition for passwordless sign-in.
    expect(
      credentials[0].isResidentCredential,
      'the credential is not resident — residentKey:"required" did not take effect',
    ).toBe(true)

    // ...and the UI says so, from the server's own credProps record, so a user
    // is never left guessing why the passwordless button ignores their passkey.
    const row = page.locator('table.sessions tbody tr', { hasText: 'E2E virtual key' })
    await expect(
      row.getByText(/^Yes$/),
      'Settings does not report the passkey as usable passwordlessly',
    ).toBeVisible()

    // From here on the login screen is in play, so the authenticator goes inert
    // (see the note above) and each ceremony is approved explicitly.
    await setPresence(false)
  })

  test('sign in with the passkey after signing out', async () => {
    // Sign out via the shell's user chip. NOTE: do not match on /Sign out/i —
    // Settings also has a "Sign out all other sessions" button, which revokes
    // OTHER sessions and would leave us logged in.
    await page.goto('/')
    await page.locator('button.user-chip').click()
    await expect(page.locator('#email')).toBeVisible()

    await page.locator('#email').fill(ADMIN_EMAIL)
    const passkeyBtn = page.getByRole('button', { name: /^A passkey$/ })
    await expect(
      passkeyBtn,
      'the passkey sign-in button is absent — the server reported passkeys unavailable',
    ).toBeVisible()
    // A successful assertion lands us in the shell with no password typed.
    await clickAndApprove(passkeyBtn)
  })

  // The passwordless (client-side discoverable) ceremony: NOTHING is typed —
  // no address, no password, no code. The browser finds the resident credential
  // on its own and the server resolves the account from it.
  //
  // This is the step Go tests genuinely cannot stand in for: a discoverable
  // ceremony only works if Chrome itself can locate a resident credential for
  // this RP with an empty allowCredentials list. A software authenticator in Go
  // is always handed the credential id by the test.
  test('sign in passwordlessly with no address typed', async () => {
    await page.goto('/')
    await page.locator('button.user-chip').click()
    await expect(page.locator('#email')).toBeVisible()

    // The address field must be left completely untouched.
    await expect(page.locator('#email')).toHaveValue('')

    const passwordlessBtn = page.getByRole('button', { name: /no address needed/i })
    await expect(
      passwordlessBtn,
      'the passwordless sign-in button is absent — the server reported passkeys unavailable',
    ).toBeVisible()
    // It must NOT be gated on the address field, unlike the identified button.
    await expect(passwordlessBtn).toBeEnabled()
    await clickAndApprove(passwordlessBtn)

    // The session really is the same owner account — the server resolved it
    // from the credential alone, with nothing supplied by the page.
    await expect(
      page.locator('button.user-chip'),
      'the passwordless session resolved to the wrong account',
    ).toContainText(ADMIN_EMAIL)
  })

  test('the passkey login is recorded in the audit ledger', async () => {
    await page.goto('/audit')
    await expect(page.getByRole('heading', { name: /Audit ledger/i })).toBeVisible()
    await page.locator('input.search').fill('webauthn')
    await expect(
      page.locator('table.ledger tbody tr', { hasText: /webauthn/i }).first(),
      'no webauthn audit event — the passkey login was not audited',
    ).toBeVisible()
  })
})
