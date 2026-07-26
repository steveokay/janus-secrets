import { fileURLToPath } from 'node:url'

/**
 * Filesystem anchors shared by the Playwright config and the specs.
 *
 * Kept in their own module (with no `@playwright/test` import) so
 * `playwright.config.ts` can pull the storage-state paths in without dragging
 * the test-only helpers along with it.
 *
 * Everything lives under `tests/e2e/.state/`, which is gitignored: it holds a
 * live admin password and two signed-in session cookies. It is scratch for one
 * run against one throwaway stack — never commit it, never reuse it.
 */
const HERE = new URL('.', import.meta.url)

/**
 * Base URL of the stack under test.
 *
 * Defined here rather than only in `playwright.config.ts` so that API contexts
 * a spec builds by hand (for scaffolding, or to speak as a service token
 * outside the browser's cookie jar) target the same server the browser does.
 * A spec that scaffolds against one stack and asserts against another fails in
 * a spectacularly misleading way.
 */
export const BASE_URL = process.env.JANUS_E2E_BASE_URL ?? 'http://localhost:8210'

/** `tests/e2e/.state/` — scratch handed from the ceremony to the flow specs. */
export const STATE_DIR = fileURLToPath(new URL('../.state/', HERE))

/** One-time owner credentials captured by the init ceremony in smoke.spec.ts. */
export const OWNER_CREDENTIALS = fileURLToPath(new URL('../.state/owner-credentials.json', HERE))

/** Signed-in browser storage state for the bootstrapped instance owner. */
export const OWNER_STATE = fileURLToPath(new URL('../.state/owner-session.json', HERE))

/** One-time credentials for the second user the flow specs need (four-eyes). */
export const COLLEAGUE_CREDENTIALS = fileURLToPath(new URL('../.state/colleague-credentials.json', HERE))

/** Signed-in browser storage state for that second user. */
export const COLLEAGUE_STATE = fileURLToPath(new URL('../.state/colleague-session.json', HERE))
