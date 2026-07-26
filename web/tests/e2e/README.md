# Janus browser E2E suite

Playwright specs that drive the embedded Atrium SPA against a **running** Janus
stack (the Go binary serving the API + SPA, backed by Postgres). Nothing is
stubbed — every step hits the real `/v1` API.

The suite is in three parts, and they run in that order because the later parts
literally cannot exist without the earlier ones:

| Playwright project | Files | What it is |
| --- | --- | --- |
| `ceremony` | `smoke.spec.ts` | The flagship path. Needs an **uninitialized** server. |
| `setup` | `auth.setup.ts` | Turns the one-time credentials into reusable sessions. |
| `flows` | `flows/*.spec.ts` | The destructive and security-control surfaces. |

Ordering is expressed with Playwright project `dependencies`, not left to file
discovery order. `npx playwright test` runs the whole thing, in order, in one
command.

## ⚠️ Never point this at an instance you care about

The suite runs the one-shot init ceremony, **permanently destroys** configs,
environments and projects, mints and revokes service tokens, changes role
bindings, and elevates privileges via break-glass. It is a wrecking ball. If you
run it against your dev instance on `:8210` you will lose that instance's data —
and, because init runs exactly once per server lifetime, the very first spec
will fail anyway.

Bring up a throwaway stack instead. `docker-compose.e2e.yml` in this directory
is an override for the repo-root `docker-compose.yml` that moves the server to
`:8231`, leaves Postgres unpublished, and — because it is brought up under its
own compose **project name** — gets its own volume, so `down -v` throws away
only this stack's data.

## Run it

From the repo root:

```bash
# 1. Bring up an isolated stack. --build matters: the SPA is compiled into the
#    Go binary via go:embed, so a change under web/src/ is invisible until the
#    image is rebuilt.
docker compose -p janus-e2e \
  -f docker-compose.yml -f web/tests/e2e/docker-compose.e2e.yml \
  up -d --build

# 2. Wait for the server to answer. (It boots SEALED, so the compose
#    healthcheck on /v1/sys/ready stays red until the suite unseals it —
#    expected. Poll seal-status instead.)
curl -sf --retry 30 --retry-all-errors --retry-delay 2 \
  http://localhost:8231/v1/sys/seal-status

# 3. Install the browser once (first run only).
cd web && npm ci && npx playwright install --with-deps chromium

# 4. Run everything.
JANUS_E2E_BASE_URL=http://localhost:8231 npx playwright test

# 5. Tear it down, volume and all.
cd .. && docker compose -p janus-e2e \
  -f docker-compose.yml -f web/tests/e2e/docker-compose.e2e.yml down -v
```

Unsealing is handled by the suite itself — it reads the Shamir shares straight
off the init ceremony screen and presents the quorum. No manual `dev-unseal`.

**Every run needs a fresh stack.** `down -v && up -d` between runs, and delete
`tests/e2e/.state/` with them. Re-running against an already-initialized server
fails on the first spec, with a message saying so.

If a spec needs WebAuthn, `JANUS_WEBAUTHN_ORIGINS` must match the port actually
served. The override file sets it to `http://localhost:8231`; change one without
the other and the passkey ceremonies fail on a browser-side origin mismatch that
reads exactly like an application bug.

### Useful variants

```bash
npx playwright test --list                 # compile-check, run nothing
npx playwright test flows/trash.spec.ts    # one spec (deps still run first)
npx playwright test --ui                   # interactive
npx playwright show-report                 # the last HTML report
```

Selecting a single flow spec still runs `ceremony` and `setup` first — they are
declared dependencies, and without them there is no initialized server and no
session to run as.

## What it covers

### `smoke.spec.ts` — the ceremony

The first-run path and the flagship secrets loop, in order: init (Shamir 5/3 +
first registrar) → unseal with a 3-of-5 quorum → log in as the bootstrapped
owner → create a project → add an environment and a config → save a secret as
one immutable config version → audited reveal → confirm the `secret.reveal`
event reached the ledger and the hash chain verifies → the paste-based import
wizard (Doppler export → auto-detect → preview → stage → commit as one version).

It then covers the passkey (WebAuthn) path using Chrome's CDP **virtual
authenticator**, which is the only way to exercise the browser half of the
ceremony: enrol a passkey and assert the credential is *resident*, sign in with
it, sign in **passwordlessly** with nothing typed at all, and confirm the login
was audited. That last step is the one Go tests genuinely cannot stand in for —
a discoverable ceremony only works if Chrome itself can locate a resident
credential for this RP with an empty `allowCredentials`.

Because it runs init, it is also the only place the owner's one-time password
ever exists. It writes that password to `.state/` for `auth.setup.ts`.

### `auth.setup.ts` — the bridge

Signs in over the API, saves two storage states, and mints a second user.

Two things drive the design. `/v1/auth/login` is rate limited per IP (10/min
sustained, burst 5) **and the login screen spends from the same bucket** — it
probes `/v1/auth/oidc/status` on mount, and that route sits behind the same
limiter — so a suite that signs in per spec throttles itself. Every flow context
is therefore handed a cookie instead, and the two logins that do happen retry on
429.

The second user exists because four-eyes approval is not testable with one
account (the whole control is "somebody else has to say yes"), and because
break-glass refuses to elevate anyone who does not already hold a strictly lower
role on the scope — which the bootstrapped instance owner never does. It is
bound `developer` at instance scope.

### `flows/*.spec.ts` — the destructive and security surfaces

Each file is independent: it scaffolds its own project tree through the API in
`beforeAll` and asserts only on material it created. They share a server, not
state. Within a file the tests are `describe.serial` — they are a lifecycle.

| Spec | What it pins |
| --- | --- |
| `trash.spec.ts` | soft-delete → restore → soft-delete → **destroy**, for a config, an environment and a project; and that cancelling a destroy confirmation does nothing. |
| `tokens.spec.ts` | minting a scoped token; the value shown exactly once and unreachable afterwards; the token working as a bearer credential; revocation withdrawing access immediately. |
| `members.spec.ts` | inviting a user; binding a role at project scope; the binding staying confined to that scope; removal taking the role away. |
| `approvals.spec.ts` | protecting a config; a save becoming a pending request rather than a commit; **self-approval and self-rejection refused**; a different reviewer's approval committing. |
| `breakglass.spec.ts` | a mandatory reason; refusal to elevate sideways; a time-boxed grant that really grants; the activation landing in the audit chain with its reason; ending the grant early. |

`trash.spec.ts` is why this directory grew past the smoke suite. `destroy`
returned 404 for every config and environment in the Trash — both delete
handlers resolved the authorization scope through a live-only repo read, and
everything reachable from Trash is by definition already soft-deleted — so a
documented button could never work, for anyone, including an owner. It shipped
with neither an API test nor an E2E test (fixed in PR #191). Its assertions are
written accordingly: the row must be *gone and stay gone across a reload*, and
restoring afterwards must 404, because a row you can still restore was never
destroyed. Asserting that a button was clicked would have passed against the
broken build.

## Adding a spec

Put it in `flows/` and it is picked up automatically. The house style:

1. **Scaffold through the API, assert through the UI.** `helpers/janus.ts` has
   `ownerRequest()`, `createTree()`, `createUser()`, `bindInstanceRole()`.
   Re-clicking a project into existence proves nothing `smoke.spec.ts` has not
   already proved, and every extra click is another way to be flaky.
2. **Name things uniquely.** `stamp('trash')` gives a collision-resistant slug.
   Every spec shares one server; filter rows by your own name, never by
   position.
3. **Assert the outcome, not the click.** "The row disappeared and did not come
   back" catches a regression; "the button was clicked" does not. Where the UI
   deliberately swallows detail — validation errors are genericised to "Please
   check your input." so internals never leak — pin the precise behaviour with a
   direct API assertion alongside the UI one.
4. **Be specific with locators.** A previous session was bitten by `/Sign out/i`
   also matching "Sign out all other sessions", which revokes *other* sessions
   and leaves you signed in. Prefer `data-testid`, exact names, or a row scoped
   by its own text. Adding a `data-testid` to a component is fine — additive
   attributes only, never restructured markup.
5. **Wait for the mutation to land before navigating.** `page.goto()` while a
   DELETE is still in flight races the list you are about to assert on. Wait for
   the visible consequence first — the row gone, the redirect done.
6. **Reach the secret editor through the dossier, not by deep link.** The editor
   reads the config's protected (four-eyes) flag out of the registry store once,
   at load, without waiting for the registry to hydrate; deep-linking loses that
   race and renders a protected config as unprotected. The server is unaffected
   — the save still comes back as a pending request — but the screen misleads.
7. **Need the second account?** Open a context with `COLLEAGUE_STATE` from
   `helpers/paths.ts`; do not sign in again.

## Configuration

- `JANUS_E2E_BASE_URL` — base URL of the running stack. Defaults to
  `http://localhost:8210` (what the repo-root compose file and the CI job use);
  set it to `http://localhost:8231` for the isolated stack above.
- `tests/e2e/.state/` — gitignored scratch holding a live admin password and two
  session cookies, handed from the ceremony to the flow specs. Delete it between
  runs; never commit it.
- See `web/playwright.config.ts` for timeouts, retries, and the project graph.

## CI

This suite is **opt-in**, not part of the per-PR `ci.yml` gate (docker + unseal
is too heavy to run on every PR). It runs via `.github/workflows/e2e.yml` on
`workflow_dispatch` (Actions → "e2e (opt-in)" → Run workflow) or when a PR
carries the `e2e` label. That job stands up its own ephemeral stack on `:8210`,
which is safe there because the runner has no other instance to lose.
