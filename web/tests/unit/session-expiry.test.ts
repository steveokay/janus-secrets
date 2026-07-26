/**
 * Unit tests for the 401 → session-expiry hook in src/lib/api.ts.
 *
 * Zero dependencies: Node's built-in test runner, with Node's native TypeScript
 * type stripping handling the `.ts` import (Node >= 22.18 / 24).
 *
 * The rule under test is narrow but load-bearing in both directions:
 *   - miss a real expiry  → the shell stays "logged in" and every action fails
 *     with a feature-level message that points nowhere near the cause
 *   - fire on a local failure → the user is thrown out mid-task, losing work
 *
 * The second is what actually shipped: a failed passkey ENROLMENT returns
 * `401 webauthn_verification` while the caller is still perfectly signed in,
 * and the original status-only check signed them out.
 */
import test from 'node:test'
import assert from 'node:assert/strict'

import { api, setUnauthenticatedHandler } from '../../src/lib/api.ts'

type FetchStub = { status: number; body: unknown }

/** Runs one API call against a stubbed fetch; returns whether the hook fired. */
async function fired(stub: FetchStub, call: () => Promise<unknown>): Promise<boolean> {
  let didFire = false
  setUnauthenticatedHandler(() => { didFire = true })

  const original = globalThis.fetch
  globalThis.fetch = (async () =>
    new Response(stub.body === undefined ? '' : JSON.stringify(stub.body), {
      status: stub.status,
      headers: { 'Content-Type': 'application/json' },
    })) as typeof fetch
  try {
    await call().catch(() => {})
  } finally {
    globalThis.fetch = original
    setUnauthenticatedHandler(() => {})
  }
  return didFire
}

const err = (code: string) => ({ error: { code, message: 'x' } })

test('a genuinely expired session drops to the login gate', async () => {
  assert.equal(
    await fired({ status: 401, body: err('session_expired') }, () => api.listProjects()),
    true,
  )
})

test('an unauthenticated request drops to the login gate', async () => {
  assert.equal(
    await fired({ status: 401, body: err('unauthenticated') }, () => api.listProjects()),
    true,
  )
})

// The regression. Enrolment is performed BY AN AUTHENTICATED USER; a ceremony
// that fails to verify (wrong RP origin, cancelled prompt, cloned authenticator)
// must surface an error, not destroy the session.
test('a failed passkey ENROLMENT does not sign the user out', async () => {
  assert.equal(
    await fired({ status: 401, body: err('webauthn_verification') }, () =>
      api.webauthnRegisterFinish('{}', 'my key'),
    ),
    false,
  )
})

test('a cloned-authenticator rejection does not sign the user out', async () => {
  assert.equal(
    await fired({ status: 401, body: err('invalid_credentials') }, () =>
      api.webauthnRegisterFinish('{}', 'my key'),
    ),
    false,
  )
})

// Unknown codes default to leaving the session alone: a spurious logout destroys
// work, whereas a missed one is corrected by the next ordinary request.
test('an unrecognised 401 code leaves the session alone', async () => {
  assert.equal(
    await fired({ status: 401, body: err('some_future_code') }, () => api.listProjects()),
    false,
  )
})

test('a non-401 failure never touches the session', async () => {
  assert.equal(
    await fired({ status: 403, body: err('forbidden') }, () => api.listProjects()),
    false,
  )
  assert.equal(
    await fired({ status: 503, body: err('sealed') }, () => api.listProjects()),
    false,
  )
})

// Firing on the me() probe would recurse: the handler re-runs that very probe.
test('the me() probe never fires the hook', async () => {
  assert.equal(
    await fired({ status: 401, body: err('unauthenticated') }, () => api.me()),
    false,
  )
})

test('a wrong password is a local failure, not an expiry', async () => {
  assert.equal(
    await fired({ status: 401, body: err('invalid_credentials') }, () =>
      api.login('a@b.c', 'wrong'),
    ),
    false,
  )
})
