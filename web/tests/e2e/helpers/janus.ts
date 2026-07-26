import { expect, type APIRequest, type APIRequestContext, type Page } from '@playwright/test'
import { existsSync, mkdirSync, readFileSync, writeFileSync } from 'node:fs'
import {
  BASE_URL,
  COLLEAGUE_CREDENTIALS,
  COLLEAGUE_STATE,
  OWNER_CREDENTIALS,
  OWNER_STATE,
  STATE_DIR,
} from './paths'

/**
 * Shared plumbing for the Janus browser E2E suite.
 *
 * The suite is one stack, one lifetime: the init ceremony runs exactly once per
 * server, and it is `smoke.spec.ts` that runs it. Everything after that has to
 * be handed the credentials rather than making its own. That handoff is what
 * this module is for — plus the small amount of scaffolding (projects,
 * environments, configs) the flow specs need in place before they can exercise
 * the thing they actually test.
 *
 * Scaffolding goes through the API, not the UI. Clicking a project into
 * existence for the fifth time proves nothing that smoke.spec.ts has not
 * already proved, and every extra click is another chance to be flaky. The
 * behaviour under test — deleting, restoring, destroying, minting, revoking,
 * binding, approving — is always driven through the browser.
 */

export interface Credentials {
  email: string
  password: string
}

/** A project → environment → config chain, by id. */
export interface Tree {
  projectId: string
  projectSlug: string
  projectName: string
  environmentId: string
  environmentSlug: string
  configId: string
  configName: string
}

/* ── one-time credential handoff ─────────────────────────────── */

function writeJson(path: string, value: unknown): void {
  mkdirSync(STATE_DIR, { recursive: true })
  writeFileSync(path, JSON.stringify(value, null, 2), 'utf8')
}

function readJson<T>(path: string, what: string): T {
  if (!existsSync(path)) {
    throw new Error(
      `${what} not found at ${path}.\n` +
        'The flow specs run after the init ceremony in smoke.spec.ts, which is what\n' +
        'captures them — they cannot be recovered afterwards. Run the whole suite\n' +
        '(`npx playwright test`) against a FRESH stack; see tests/e2e/README.md.',
    )
  }
  return JSON.parse(readFileSync(path, 'utf8')) as T
}

/** Called by the init ceremony: the password is shown exactly once, ever. */
export function saveOwnerCredentials(c: Credentials): void {
  writeJson(OWNER_CREDENTIALS, c)
}

export function readOwnerCredentials(): Credentials {
  return readJson<Credentials>(OWNER_CREDENTIALS, 'the bootstrapped owner credentials')
}

/**
 * Kept alongside the storage state rather than discarded: when a flow spec fails
 * mid-run, being able to sign in as the second user by hand against the still
 * -running stack is the difference between reproducing it and re-running the
 * whole suite blind.
 */
export function saveColleagueCredentials(c: Credentials): void {
  writeJson(COLLEAGUE_CREDENTIALS, c)
}

/* ── sessions ────────────────────────────────────────────────── */

/**
 * Signs in over the API and writes the resulting session cookie out as a
 * Playwright storage state, for `browser.newContext({ storageState })`.
 *
 * Two deliberate choices here.
 *
 * It does NOT drive the login gate. smoke.spec.ts already proves the gate
 * works — twice, once with a password and twice more with a passkey — and the
 * flow specs only need to *be* somebody.
 *
 * It retries on 429. `/v1/auth/login` is rate limited per IP (10/min sustained,
 * burst 5) and the login SCREEN spends from the very same bucket, because it
 * probes `/v1/auth/oidc/status` on mount and that route is behind the same
 * limiter. The passkey sign-out/sign-in steps at the end of smoke.spec.ts open
 * the gate three times in a few seconds, so by the time this runs the bucket is
 * usually empty. Without the backoff the suite fails here with "Too many
 * attempts", which reads exactly like a wrong password.
 */
export async function mintSession(
  apiRequest: APIRequest,
  c: Credentials,
  statePath: string,
): Promise<void> {
  const context = await apiRequest.newContext({ baseURL: BASE_URL })
  try {
    for (let attempt = 1; ; attempt++) {
      const res = await context.post('/v1/auth/login', {
        data: { email: c.email, password: c.password },
      })
      if (res.ok()) break
      if (res.status() === 429 && attempt < 10) {
        // The bucket refills at 10/min; one token takes 6s.
        await new Promise(resolve => setTimeout(resolve, 6_500))
        continue
      }
      throw new Error(`sign in as ${c.email} failed: ${res.status()} ${await res.text()}`)
    }
    mkdirSync(STATE_DIR, { recursive: true })
    await context.storageState({ path: statePath })
  } finally {
    await context.dispose()
  }
}

/** The Projects tab in the shell nav — present iff we are inside the shell. */
export function shellNav(page: Page) {
  return page.locator('nav').getByRole('link', { name: 'Projects' })
}

/* ── API contexts ────────────────────────────────────────────── */

/**
 * A cookie-authenticated API context for the bootstrapped owner.
 *
 * `beforeAll` cannot reach the test-scoped `page`/`request` fixtures, and
 * scaffolding belongs there rather than inside the test under examination —
 * hence building one by hand from the saved session. Pass `playwright.request`.
 */
export function ownerRequest(apiRequest: APIRequest): Promise<APIRequestContext> {
  return apiRequest.newContext({ baseURL: BASE_URL, storageState: OWNER_STATE })
}

/** The same, for the second user. */
export function colleagueRequest(apiRequest: APIRequest): Promise<APIRequestContext> {
  return apiRequest.newContext({ baseURL: BASE_URL, storageState: COLLEAGUE_STATE })
}

/**
 * An API context with NO session cookie, authenticating with a bearer token.
 * The point is that it carries nothing else: a request that succeeds here
 * succeeded on the strength of the token alone.
 */
export function tokenRequest(apiRequest: APIRequest, token: string): Promise<APIRequestContext> {
  return apiRequest.newContext({
    baseURL: BASE_URL,
    extraHTTPHeaders: { Authorization: `Bearer ${token}` },
  })
}

/* ── API scaffolding ─────────────────────────────────────────── */

/**
 * Every scaffolding call asserts on the status itself. A silent 403 here would
 * surface later as an unrelated empty table, which is the single most confusing
 * way for an E2E suite to fail.
 */
async function apiJson<T>(
  request: APIRequestContext,
  method: 'get' | 'post' | 'put' | 'delete',
  path: string,
  body?: unknown,
): Promise<T> {
  const res = await request[method](path, body === undefined ? undefined : { data: body })
  expect(res.ok(), `${method.toUpperCase()} ${path} → ${res.status()}: ${await res.text()}`).toBe(true)
  return (await res.json()) as T
}

/** A short, collision-resistant suffix for names created by one spec run. */
export function stamp(prefix: string): string {
  return `${prefix}-${Date.now().toString(36)}-${Math.floor(Math.random() * 1e4).toString(36)}`
}

/** Creates a project → environment → config chain via the API. */
export async function createTree(
  request: APIRequestContext,
  opts: { slug: string; envSlug?: string; configName?: string },
): Promise<Tree> {
  const envSlug = opts.envSlug ?? 'dev'
  const configName = opts.configName ?? 'default'
  const project = await apiJson<{ id: string; slug: string; name: string }>(
    request, 'post', '/v1/projects', { slug: opts.slug, name: opts.slug },
  )
  const env = await createEnvironment(request, project.id, envSlug)
  const config = await apiJson<{ id: string; name: string }>(
    request, 'post', `/v1/projects/${project.id}/environments/${env.id}/configs`, { name: configName },
  )
  return {
    projectId: project.id,
    projectSlug: project.slug,
    projectName: project.name,
    environmentId: env.id,
    environmentSlug: envSlug,
    configId: config.id,
    configName: config.name,
  }
}

export async function createEnvironment(
  request: APIRequestContext,
  projectId: string,
  slug: string,
): Promise<{ id: string; slug: string; name: string }> {
  return apiJson(request, 'post', `/v1/projects/${projectId}/environments`, { slug, name: slug })
}

/** Creates a user and returns its one-time password. */
export async function createUser(
  request: APIRequestContext,
  email: string,
): Promise<{ id: string; email: string; password: string }> {
  return apiJson(request, 'post', '/v1/users', { email })
}

/** Binds a role at instance scope. */
export async function bindInstanceRole(
  request: APIRequestContext,
  userId: string,
  role: 'viewer' | 'developer' | 'admin' | 'owner',
): Promise<void> {
  const res = await request.put(`/v1/instance/members/${userId}`, { data: { role } })
  expect(res.ok(), `bind ${role} at instance scope → ${res.status()}`).toBe(true)
}

/* ── dialogs ─────────────────────────────────────────────────── */

/**
 * Confirms the app's own modal (Janus never uses a native browser dialog — see
 * `web/src/lib/dialog.svelte.ts`).
 *
 * The label matters: the confirm button is titled per action ("Destroy",
 * "Revoke", "Move to trash", "Remove binding", "End now"), and matching loosely
 * risks hitting the wrong control on a page that has several. Pass the exact
 * label the component uses.
 */
export async function confirmDialog(page: Page, label: string | RegExp): Promise<void> {
  const modal = page.locator('div.modal[role="dialog"]')
  await expect(modal).toBeVisible()
  await modal.getByRole('button', { name: label }).click()
  await expect(modal).toBeHidden()
}
