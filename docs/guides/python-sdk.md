# Reading secrets from Python (the Python SDK)

Janus ships a typed Python client SDK for apps that want to read secrets
**programmatically, in-process** — as an alternative to injecting them with
`janus run`. It talks to the Janus `/v1` REST API over HTTP using a scoped
service token, with an in-process TTL cache so repeated reads don't re-hit the
server.

It **mirrors the [Go SDK](go-sdk.md)** (same reveal endpoints, caching model,
and error taxonomy), so the two feel consistent.

The SDK lives in [`sdk/python/`](../../sdk/python/) as the `janus_client`
package. It is **standard-library only** at runtime (built on
`urllib.request`), so installing it pulls in no third-party dependencies. It
supports Python 3.9+ and ships type hints with a `py.typed` marker.

## Install

```
pip install janus-client        # once published
# or, from a checkout:
pip install ./sdk/python
```

## Authenticate

Mint a scoped **service token** (see [Service tokens](service-tokens.md)) with
read access to the config you want, then pass it via `token`:

```python
import os
from janus_client import Client

client = Client(
    "https://janus.example.com",
    token=os.environ["JANUS_TOKEN"],
    cache_ttl=30.0,  # default; 0 disables caching
)
```

## Read secrets

```python
# All resolved secrets for a config (dict[str, str]).
secrets = client.get_secrets(config_id)

# A single key.
api_key = client.get_secret(config_id, "API_KEY")
```

Reads go through the **audited reveal path** — each cache miss is recorded
server-side as a `secret.reveal` event (visible in the [audit
log](../operations.md)). That is expected.

## Caching

- Config reads are cached **in memory only** for the TTL; the cache is never
  written to disk and is lost on process exit.
- Within the TTL, repeated reads are served from memory. On expiry (or after
  `client.refresh(config_id)`), the next read re-fetches.
- The cache is thread-safe and returns a copy of the map on each read.
- Pass `cache_ttl=0` to disable caching (every read hits the server).

## Error handling

Non-2xx responses become a typed exception: `JanusError` is the base, with
`Unauthorized` (401), `Forbidden` (403), `NotFound` (404), and `Sealed` (503)
subclasses. Each carries `.status`, `.code`, and `.message`.

```python
from janus_client import Unauthorized, Forbidden, NotFound, Sealed

try:
    secrets = client.get_secrets(config_id)
except Sealed:
    ...  # 503 — retry after unseal
```

## Dynamic credentials

If the token can issue [dynamic Postgres credentials](../openapi.yaml),
`client.issue_dynamic(role_id)` returns a `Lease` (one-time password held in
memory only) with `renew()` / `revoke()` methods.

## Full reference

See [`sdk/python/README.md`](../../sdk/python/README.md) for the complete API
surface, a runnable example, and the caching/security model.
