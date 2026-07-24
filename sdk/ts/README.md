# Janus TypeScript SDK (`janus-client`)

A typed TypeScript/JavaScript client for the [Janus](../../README.md) secrets
manager. It lets Node apps (and other modern runtimes) read secrets from Janus
**programmatically, in-process** — with an in-memory TTL cache and optional
dynamic-credential lease management — instead of hand-rolling HTTP calls.

It mirrors the [Go SDK](../go/README.md): same reads, same cache model, same
typed-error taxonomy, same dynamic-lease support.

For subprocess injection (`env`-var style), use `janus run`. This SDK is for
apps that want **programmatic reads** in-process.

## Install

```
npm install janus-client
```

- **Zero runtime dependencies.** It uses the built-in global `fetch`, so it
  targets **Node 18+** and any modern runtime that ships `fetch` (Bun, Deno,
  Cloudflare Workers, browsers). On older runtimes, pass your own `fetch`.
- Ships as **ESM** with bundled TypeScript type definitions (`dist/`).

## Quick start

```ts
import { JanusClient } from "janus-client";

const client = new JanusClient({
  baseUrl: "https://janus.example.com",
  token: process.env.JANUS_TOKEN, // a janus_svc_... service token
  cacheTtlMs: 30_000,             // default; 0 disables caching
});

const configId = "cfg-00000000-0000-0000-0000-000000000001";

// All resolved secrets for a config (references resolved server-side).
const secrets = await client.getSecrets(configId); // Record<string, string>
const db = secrets.DATABASE_URL; // use it — never log the value

// Or a single key (rejects with JanusNotFoundError if absent).
const apiKey = await client.getSecret(configId, "API_KEY");
```

## API surface

| Method | Description |
| --- | --- |
| `new JanusClient({ baseUrl, token?, cacheTtlMs?, fetch?, now? })` | Construct a client. `fetch`/`now` are injectable for tests. |
| `getSecrets(configId, { signal? }?)` | Resolved `Record<string,string>` for a config (audited reveal on cache miss). |
| `getSecret(configId, key, { signal? }?)` | Single resolved value; `JanusNotFoundError` if the key is absent. |
| `refresh(configId?)` | Evict a config's cache; `refresh()` with no argument clears all. |
| `issueDynamic(roleId, { signal? }?)` | Issue a dynamic DB credential `Lease`; the password is returned once. |
| `lease.renew({ signal? }?)` / `lease.revoke({ signal? }?)` | Extend or immediately drop a dynamic lease. |

Every method returns a `Promise` and accepts an optional `AbortSignal`.

### Options

- `baseUrl` (required) — e.g. `https://janus.example.com`. The `/v1` prefix is
  added automatically.
- `token` — a `janus_svc_` service token, sent as `Authorization: Bearer <token>`.
- `cacheTtlMs` — cache TTL for config reads, in milliseconds. Default `30000`
  (`DEFAULT_CACHE_TTL_MS`); pass `0` to disable caching entirely.
- `fetch` — a custom `fetch` (bring your own transport/TLS, or inject a fake in
  tests). Defaults to the global `fetch`.
- `now` — a clock returning epoch milliseconds, overridable in tests to make TTL
  behaviour deterministic. Defaults to `Date.now`.

## Caching model

`getSecrets` (and `getSecret`, which reads through it) caches each config's
resolved secrets **in memory only** for the configured TTL:

- Within the TTL, repeated reads of the same config are served from the cache
  and do **not** hit the server.
- On a cache miss (first read, or after TTL expiry), the SDK calls the audited
  reveal endpoint (recorded server-side as a `secret.reveal` event) and
  re-populates the cache.
- The object returned to you is always a **copy** — mutating it never affects
  the cache.
- `refresh(configId)` busts one entry so the next read re-fetches;
  `refresh()` clears the whole cache.

**Secrets are never written to disk.** The cache lives in process memory and is
lost when the process exits. No SDK method logs secret values, so the client is
safe to use alongside your application's own logger.

## Errors

Non-2xx responses are parsed from the server's `{"error":{code,message}}`
envelope into a `JanusError` carrying `status`, `code`, and `message`. Common
statuses map to specific subclasses, each with a matching type guard:

```ts
import {
  JanusError,
  isUnauthorized, // 401 -> JanusUnauthorizedError
  isForbidden,    // 403 -> JanusForbiddenError
  isNotFound,     // 404 -> JanusNotFoundError
  isSealed,       // 503 / code "sealed" -> JanusSealedError (retry after unseal)
} from "janus-client";

try {
  const secrets = await client.getSecrets(configId);
} catch (err) {
  if (isSealed(err)) {
    // server sealed — back off and retry after it's unsealed
  } else if (isForbidden(err)) {
    // token lacks access to this config
  } else if (err instanceof JanusError) {
    console.error(err.status, err.code); // never carries a secret value
  }
}
```

A `JanusError` never carries a secret value — the server's error envelope is
value-free by design.

## Dynamic credentials (leases)

If your token can issue dynamic Postgres credentials, `issueDynamic` returns a
`Lease` whose one-time password is held in memory only:

```ts
const lease = await client.issueDynamic(roleId);
// lease.id, lease.username, lease.password (returned once), lease.expiresAt

try {
  // ... use the credentials ...
  await lease.renew(); // extend before expiry (capped at the role's max TTL)
} finally {
  await lease.revoke(); // drop the underlying DB role
}
```

`roleId` identifies a dynamic **role** (authored via the admin API), not a
config.

## Development

From `sdk/ts/`:

```
npm install      # install dev deps (typescript, tsx); lockfile committed
npm run build    # tsc -> dist/ (ESM + .d.ts)
npm test         # node:test suite with an injected fake fetch (no network)
npm run typecheck # tsc --noEmit
```

The package is self-contained: it imports nothing from the repo's `web/` app or
anywhere outside `sdk/ts/`.

## License

Apache-2.0, same as the parent project.
