import { defineConfig, devices } from '@playwright/test'

/**
 * Playwright config for the Janus browser E2E smoke suite.
 *
 * The suite runs against a *running* Janus stack (the Go binary serving both the
 * API and the embedded SPA, backed by Postgres). It does NOT start the server —
 * bring the stack up first (see tests/e2e/README.md), then point the suite at it:
 *
 *   JANUS_E2E_BASE_URL=http://localhost:8210 npm run test:e2e
 *
 * Default base URL is http://localhost:8210 — the host port docker-compose.yml
 * maps the server's internal :8200 to. Override for other deployments.
 */
const baseURL = process.env.JANUS_E2E_BASE_URL ?? 'http://localhost:8210'

export default defineConfig({
  testDir: './tests/e2e',
  // The smoke flow is a single ordered ceremony (init → unseal → … → reveal);
  // it must run serially and only once.
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
    baseURL,
    trace: 'on-first-retry',
    screenshot: 'only-on-failure',
    video: 'retain-on-failure',
  },
  projects: [
    {
      name: 'chromium',
      use: { ...devices['Desktop Chrome'] },
    },
  ],
})
