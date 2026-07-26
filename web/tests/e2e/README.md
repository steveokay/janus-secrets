# Janus browser E2E smoke suite

A Playwright suite that drives the flagship path end-to-end through the embedded
Atrium SPA against a **running** Janus stack (the Go binary serving the API + SPA,
backed by Postgres). Nothing is stubbed — every step hits the real `/v1` API.

## What it covers

`smoke.spec.ts` runs the first-run ceremony and the core secrets path, in order:

1. **Init** — Shamir 5/3 + first registrar (admin email), and captures the
   one-time key shares + admin password the UI surfaces exactly once.
2. **Unseal** — presents the 3-of-5 quorum of shares to reconstruct the master key.
3. **Login** — signs in as the bootstrapped owner with the one-time password.
4. **Create project** — new dossier in the registry.
5. **Environment + config** — adds a `dev` environment and a `default` config.
6. **Save a secret** — stages a draft key/value and commits it as one immutable
   config version ("Committed — vN").
7. **Audited reveal** — reveals the masked value; asserts the audit toast.
8. **Audit event** — navigates to the ledger and confirms a `secret.reveal`
   event for the key exists, and that the hash chain verifies.

It then covers the passkey (WebAuthn) path using Chrome's CDP **virtual
authenticator**, which is the only way to exercise the browser half of the
ceremony:

9. **Register a passkey** — the real `navigator.credentials.create()` ceremony;
   asserts the created credential is *resident* (Janus enrols with
   `residentKey: "required"`) and that Settings reports it as usable
   passwordlessly.
10. **Sign in with the passkey** — signs out, types the address, and asserts.
11. **Sign in passwordlessly** — signs out and clicks *A passkey — no address
    needed* with the address field left empty. Chrome locates the resident
    credential itself; the server resolves the account from the credential id
    alone. This is the step Go tests cannot stand in for.
12. **Audit event** — confirms the passkey login reached the ledger.

> **The server must be freshly initialized.** The init ceremony runs exactly once
> per server lifetime. Always run against a clean stack.

## Run it

The suite does **not** start the server — bring the stack up first, then run it.

```bash
# 1. From the repo root: bring up a clean stack (server + Postgres).
#    -v drops any prior volume so init is available again.
docker compose down -v
docker compose up -d --build

# 2. Wait for health (compose has a healthcheck on /v1/sys/ready):
docker compose ps        # janus should become "healthy"

# 3. Install browsers once (first run only):
cd web
npm ci
npx playwright install --with-deps chromium

# 4. Run the suite. Default base URL is http://localhost:8210
#    (the host port docker-compose maps the server's internal :8200 to).
JANUS_E2E_BASE_URL=http://localhost:8210 npm run test:e2e
```

Unsealing is handled by the suite itself — it reads the shares straight off the
init ceremony screen and presents the quorum. No manual `dev-unseal` step needed.

### Useful variants

```bash
npm run test:e2e:ui         # Playwright UI mode (interactive)
npx playwright test --list  # list specs without running (compile check)
npx playwright show-report  # open the last HTML report
```

## Configuration

- `JANUS_E2E_BASE_URL` — base URL of the running stack (default
  `http://localhost:8210`).
- See `web/playwright.config.ts` for timeouts, retries, trace-on-first-retry.

## CI

This suite is **opt-in**, not part of the per-PR `ci.yml` gate (docker + unseal is
too heavy/flaky to run on every PR). It runs via
`.github/workflows/e2e.yml` on `workflow_dispatch` (Actions → "e2e (opt-in)" →
Run workflow) or when a PR carries the `e2e` label.
