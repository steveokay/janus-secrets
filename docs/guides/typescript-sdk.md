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

## Full reference

See [`sdk/ts/README.md`](../../sdk/ts/README.md) for the complete API surface, a
runnable example, and the caching/security model.
