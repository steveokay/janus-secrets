import { defineConfig, devices } from '@playwright/test'
import { BASE_URL, OWNER_STATE } from './tests/e2e/helpers/paths'

/**
 * Playwright config for the Janus browser E2E suite.
 *
 * The suite runs against a *running* Janus stack (the Go binary serving both the
 * API and the embedded SPA, backed by Postgres). It does NOT start the server —
 * bring an ISOLATED stack up first (see tests/e2e/README.md), then point the
 * suite at it:
 *
 *   JANUS_E2E_BASE_URL=http://localhost:8231 npm run test:e2e
 *
 * Never point it at an instance whose data matters. The suite runs the one-shot
 * init ceremony, soft-deletes and permanently destroys material, mints and
 * revokes credentials, and elevates roles. tests/e2e/docker-compose.e2e.yml
 * brings up a throwaway stack on :8231 with its own volume for exactly this.
 *
 * The default base URL stays http://localhost:8210 — the host port the repo-root
 * docker-compose.yml uses — because the opt-in CI job stands up its own
 * ephemeral stack there.
 */
const chrome = devices['Desktop Chrome']

export default defineConfig({
  testDir: './tests/e2e',
  // Every project below drives one shared server whose init ceremony runs
  // exactly once per lifetime. Nothing here is safe to parallelise.
  fullyParallel: false,
  workers: 1,
  forbidOnly: !!process.env.CI,
  retries: process.env.CI ? 1 : 0,
  // A fresh, uninitialized server can take a moment to answer; ceremonies chain
  // several round-trips. Keep per-test generous but bounded.
  timeout: 60_000,
  expect: { timeout: 10_000 },
  reporter: process.env.CI ? [['github'], ['list']] : [['list']],
  use: {
    baseURL: BASE_URL,
    trace: 'on-first-retry',
    screenshot: 'only-on-failure',
    video: 'retain-on-failure',
  },
  // Ordering is expressed with project dependencies rather than left to file
  // discovery order: the ceremony MUST run first (it is the only thing that can
  // initialize the server), and the flow specs cannot sign in until it has.
  projects: [
    // 1. The first-run ceremony: init → unseal → login → the flagship secrets
    //    path → import wizard → passkeys. Requires an uninitialized server, so
    //    it runs first and exactly once. It also captures the one-time owner
    //    password that everything downstream depends on.
    {
      name: 'ceremony',
      testDir: './tests/e2e',
      testMatch: '**/smoke.spec.ts',
      use: { ...chrome },
    },
    // 2. Turns those one-time credentials into reusable signed-in sessions, and
    //    mints the second account the four-eyes and break-glass flows need.
    {
      name: 'setup',
      testDir: './tests/e2e',
      testMatch: '**/auth.setup.ts',
      dependencies: ['ceremony'],
      use: { ...chrome },
    },
    // 3. The flow specs — destructive and security-control surfaces, each one
    //    independent of the others. They start already signed in as the owner;
    //    a spec that needs the second account opens its own context with that
    //    account's saved state.
    {
      name: 'flows',
      testDir: './tests/e2e/flows',
      testMatch: '**/*.spec.ts',
      dependencies: ['setup'],
      use: { ...chrome, storageState: OWNER_STATE },
    },
  ],
})
