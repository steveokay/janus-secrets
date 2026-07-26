# Reading secrets from TypeScript / Node (the TypeScript SDK)

Janus ships a typed TypeScript client SDK for apps that want to read secrets
**programmatically, in-process** — as an alternative to injecting them with
`janus run`. It talks to the Janus `/v1` REST API over HTTP using a scoped
service token, with an in-memory TTL cache so repeated reads don't re-hit the
server.

It mirrors the [Go SDK](go-sdk.md): the same reads, cache model, typed-error
taxonomy, and dynamic-lease support, expressed idiomatically for TypeScript.

The SDK lives in [`sdk/ts/`](../../sdk/ts/) as its own standalone npm package
(`janus-client`), with **zero runtime dependencies** — it uses the built-in
global `fetch`, so it targets **Node 18+** and any modern runtime that ships
`fetch`. It ships as ESM with bundled type definitions.

## Install

```
npm install janus-client
```

## Authenticate

Mint a scoped **service token** (see [Service tokens](service-tokens.md)) with
read access to the config you want to read, then pass it via `token`:

```ts
import { JanusClient } from "janus-client";

const client = new JanusClient({
  baseUrl: "https://janus.example.com",
  token: process.env.JANUS_TOKEN,
  cacheTtlMs: 30_000, // default; 0 disables caching
});
```

## Read secrets

```ts
const configId = "cfg-00000000-0000-0000-0000-000000000001";

// All resolved secrets for a config.
const secrets = await client.getSecrets(configId); // Record<string, string>

// A single key.
const apiKey = await client.getSecret(configId, "API_KEY");
```

Reads go through the **audited reveal path** — each cache miss is recorded
server-side as a `secret.reveal` event (visible in the [audit
log](../operations.md)). That is expected.

## Caching

- Config reads are cached **in memory only** for the TTL; the cache is never
  written to disk and is lost on process exit.
- Within the TTL, repeated reads are served from memory. On expiry (or after
  `client.refresh(configId)`), the next read re-fetches. The returned object is
  always a copy, so mutating it never affects the cache.
- Pass `cacheTtlMs: 0` to disable caching (every read hits the server).

## Error handling

Non-2xx responses become a `JanusError` with `status`, `code`, and `message`.
Common statuses map to specific subclasses, each with a matching type guard:

```ts
import { isUnauthorized, isForbidden, isNotFound, isSealed } from "janus-client";

// isUnauthorized(err) -> 401
// isForbidden(err)    -> 403
// isNotFound(err)     -> 404
// isSealed(err)       -> 503 / code "sealed" — retry after unseal
```

A `JanusError` never carries a secret value — the server's error envelope is
value-free by design.

## Dynamic credentials

If the token can issue [dynamic Postgres credentials](../openapi.yaml),
`client.issueDynamic(roleId)` returns a `Lease` (one-time password held in
memory only) with `renew()` / `revoke()` methods. `roleId` identifies a dynamic
**role**, not a config.

### `withDynamic` — the recommended way

Managing a lease by hand means remembering to renew it *and* to revoke it on
every exit path. `withDynamic` does both:

```ts
const rows = await client.withDynamic(roleId, async (lease, signal) => {
  const pool = new Pool({ user: lease.username, password: lease.password });
  try {
    return await query(pool, signal); // auto-renew runs for as long as this does
  } finally {
    await pool.end();
  }
});
```

The lease is issued, kept renewed in the background, and revoked in a `finally`
on the way out — on success, on a thrown error (async or synchronous), and on
an early return. The `signal` handed to your callback is aborted when auto-renew
terminates — max TTL reached, lease revoked out from under you, token lost
access — so long-running work can wind down *before* the credentials stop
working.

### Background auto-renew

Auto-renew is **opt-in**: nothing in the SDK schedules a timer unless you call
`lease.startAutoRenew()` (or `withDynamic`, which calls it for you).

```ts
const renewer = lease.startAutoRenew({
  onEvent: (e) => { /* never contains the password */ },
  signal,           // optional: aborting it stops the loop
});
try {
  // ... use the credentials ...
} finally {
  await renewer.stop(); // idempotent; resolves once the loop has exited
}
```

**Renewal policy.** After each successful renew the loop waits **2/3 of the
remaining TTL**, ± **10% jitter** (so a fleet doesn't stampede the server),
floored at **1s**, then renews again. A failed-but-retryable attempt is retried
at half that fraction, so retries converge on the expiry instead of hot-looping.
Tune with `fraction`, `jitter`, and `minIntervalMs`. The default timer is
`unref`'d, so an active renewer never keeps a Node process alive on its own.

**Error-handling contract.** Nothing is swallowed. Every attempt is reported to
`onEvent`, and the loop ends with exactly one terminal event whose `reason` and
`error` are also readable from `renewer.reason` / `renewer.error` after
`await renewer.done`:

| Outcome | `reason` | `error` |
| --- | --- | --- |
| `stop()` called | `stopped` | `undefined` |
| supplied `signal` aborted | `aborted` | `undefined` |
| server won't extend further | `max_ttl` | `JanusMaxTtlReachedError` |
| lease gone / not active (404, 409) | `lease_gone` | `JanusNotFoundError` / `JanusError` |
| token rejected (401 / 403) | `unauthorized` / `forbidden` | `JanusError` |
| other non-retryable 4xx | `rejected` | `JanusError` |
| TTL ran out while retrying | `expired` | `JanusLeaseExpiredError` |

Retryable failures — network errors, 5xx (including a sealed server), 408, 429 —
are reported as **non-terminal** events with `error` set and retried while TTL
headroom remains. Renewal is capped server-side at the role's max TTL, so the
loop stops at that ceiling rather than retrying forever: treat `max_ttl` as
"acquire a new lease".

`RenewEvent` is value-free — it carries the lease ID and timings, never the
password, and the SDK logs nothing on its own.

**Revoke failures.** `withDynamic` never replaces your error. If your callback
threw *and* the revoke failed, your error is re-thrown with the
`JanusRevokeError` attached as a non-enumerable `revokeError` property and also
reported to `onEvent` with reason `revoke_failed`. If only the revoke failed, a
`JanusRevokeError` is thrown.

## Full reference

See [`sdk/ts/README.md`](../../sdk/ts/README.md) for the complete API surface, a
runnable example, and the caching/security model.
