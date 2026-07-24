# Janus Python SDK (`janus-client`)

A typed Python client for the [Janus](../../README.md) secrets manager. It lets
Python applications read secrets from Janus programmatically — with an
in-process TTL cache and optional dynamic-credential lease management — instead
of hand-rolling HTTP calls.

This SDK **mirrors the [Go SDK](../go/README.md)** so the two feel consistent
(same reveal endpoints, same caching model, same error taxonomy).

For subprocess injection (`env`-var style), use `janus run`. This SDK is for
apps that want **programmatic reads** in-process.

## Install

Standard-library only at runtime — installing it pulls in **no third-party
dependencies**. Supports Python 3.9+.

```
pip install janus-client        # once published
# or, from a checkout:
pip install ./sdk/python
```

```python
from janus_client import Client
```

## Quick start

```python
import os
from janus_client import Client, NotFound

client = Client(
    "https://janus.example.com",
    token=os.environ["JANUS_TOKEN"],  # a janus_svc_... service token
    cache_ttl=30.0,                   # default; 0 disables caching
)

config_id = "cfg-00000000-0000-0000-0000-000000000001"

# Fetch all resolved secrets for a config (dict[str, str]).
secrets = client.get_secrets(config_id)
db_url = secrets["DATABASE_URL"]      # use it — never log the value

# Or fetch a single key.
try:
    api_key = client.get_secret(config_id, "API_KEY")
except NotFound:
    api_key = None

print("loaded", len(secrets), "secrets")
```

## API surface

| Method | Description |
| --- | --- |
| `Client(base_url, token=None, cache_ttl=30.0, timeout=30.0, opener=None, transport=None, clock=None)` | Construct a client. |
| `get_secrets(config_id) -> dict[str, str]` | Resolved key/value map for a config (audited reveal on cache miss). |
| `get_secret(config_id, key) -> str` | Single resolved value; raises `NotFound` if the key is absent. |
| `refresh(config_id=None)` | Evict a config's cache (or `refresh()` / `refresh(None)` to clear all). |
| `issue_dynamic(role_id) -> Lease` | Issue a dynamic DB credential lease; the password is returned once. |
| `Lease.renew()` / `Lease.revoke()` | Extend or immediately drop a dynamic lease. |

This mirrors the Go SDK's `NewClient`/`WithToken`/`WithCacheTTL`,
`GetSecrets`/`GetSecret`/`Refresh`, and `IssueDynamic`/`Lease.Renew`/`Revoke`.

### Constructor arguments

- `base_url` — e.g. `"https://janus.example.com"`; the `/v1` prefix is added
  automatically.
- `token` — a `janus_svc_` service token, sent as `Authorization: Bearer …`.
- `cache_ttl` — cache TTL in seconds for config reads. Default `30.0`
  (`janus_client.DEFAULT_CACHE_TTL`); pass `0` to disable caching entirely.
- `timeout` — per-request timeout in seconds for the default transport.
- `opener` — an optional `urllib.request.OpenerDirector` for the default
  transport (TLS / proxies / redirects).
- `transport` — an optional injectable HTTP transport (used by tests to avoid a
  live network); takes precedence over `opener`.
- `clock` — an optional `() -> float` clock for deterministic cache expiry in
  tests (defaults to `time.monotonic`).

## Caching model

`get_secrets` (and `get_secret`, which reads through it) caches each config's
resolved secrets **in memory only** for the configured TTL:

- Within the TTL, repeated reads of the same config are served from the cache
  and do **not** hit the server.
- On a cache miss (first read, or after TTL expiry), the SDK calls the audited
  reveal endpoint (recorded server-side as a `secret.reveal` event) and
  re-populates the cache.
- The cache is **thread-safe** (guarded by a lock) and the dict returned to you
  is always a **copy** — mutating it never affects the cache.
- `refresh(config_id)` busts the entry so the next read re-fetches;
  `refresh()` clears the whole cache.

**Secrets are never written to disk.** The cache lives in process memory and is
lost when the process exits. No SDK method logs secret values (the `Lease`
`repr` deliberately omits the password), so the client is safe to use alongside
your application's own logger.

## Errors

Non-2xx responses are parsed from the server's `{"error":{code,message}}`
envelope into a typed exception. `JanusError` is the base; common statuses raise
a dedicated subclass:

```python
from janus_client import JanusError, Unauthorized, Forbidden, NotFound, Sealed

try:
    secrets = client.get_secrets(config_id)
except Unauthorized:   # 401
    ...
except Forbidden:      # 403
    ...
except NotFound:       # 404
    ...
except Sealed:         # 503 — server sealed, retry after unseal
    ...
except JanusError as err:
    print(err.status, err.code, err.message)
```

A `JanusError` never carries a secret value — the server's error envelope is
value-free by design.

## Dynamic credentials (leases)

If your token can issue dynamic Postgres credentials, `issue_dynamic` returns a
`Lease` whose one-time password is held in memory only:

```python
lease = client.issue_dynamic(role_id)
# lease.id, lease.username, lease.password (returned once), lease.expires_at
try:
    ...  # use the credentials
finally:
    lease.revoke()

# ... or, extend before expiry (capped at the role's max TTL):
lease.renew()
```

## Development

```
cd sdk/python
python -m pip install -e '.[dev]'   # installs mypy for type checking
python -m unittest                  # run the test suite (no network)
python -m mypy .                    # strict type check
```

Tests use an injected fake transport (and a loopback `http.server` for the
end-to-end transport test), so they never touch a live network.

## License

Apache-2.0, same as the parent project.
