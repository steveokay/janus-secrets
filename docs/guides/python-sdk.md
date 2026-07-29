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

### `dynamic_lease` — the recommended way

Managing a lease by hand means remembering to renew it *and* to revoke it on
every exit path. The `dynamic_lease` context manager does both:

```python
with client.dynamic_lease(role_id) as lease:
    conn = psycopg.connect(user=lease.username, password=lease.password)
    ...  # auto-renew runs for as long as the block does
```

The lease is issued, kept renewed on a background thread, and revoked in the
teardown — on success, on an exception (which propagates unchanged), and on an
early `return` or `break`. Pass `auto_renew=False` to skip renewal and drive it
by hand.

There is no `async with` variant: this SDK's transport is the blocking stdlib
`urllib`, so an async wrapper would only be a thread bridge.

### Background auto-renew

Auto-renew is **opt-in**: nothing in the SDK starts a thread unless you call
`lease.start_auto_renew()` (or `dynamic_lease`, which calls it for you).

```python
renewer = lease.start_auto_renew(on_event=handler)  # events never carry the password
try:
    ...  # use the credentials
finally:
    renewer.stop()   # idempotent; joins the (daemon) thread
```

**Renewal policy.** After each successful renew the loop waits **2/3 of the
remaining TTL**, ± **10% jitter** (so a fleet doesn't stampede the server),
floored at **1s**, then renews again. A failed-but-retryable attempt is retried
at half that fraction, so retries converge on the expiry instead of hot-looping.
Tune with `fraction`, `jitter`, and `min_interval`. A renew already in flight
cannot be cancelled (blocking `urllib`), so `stop(timeout=...)` bounds the wait;
the thread is a daemon and never blocks interpreter exit.

**Error-handling contract.** Nothing is swallowed. Every attempt is reported to
`on_event`, and the loop ends with exactly one terminal event whose `reason` and
`error` are also readable from `renewer.reason` / `renewer.error` after
`renewer.wait()`:

| Outcome | `reason` | `error` |
| --- | --- | --- |
| `stop()` called | `stopped` | `None` |
| server won't extend further | `max_ttl` | `MaxTTLReached` |
| lease gone / not active (404, 409) | `lease_gone` | `NotFound` / `JanusError` |
| token rejected (401 / 403) | `unauthorized` / `forbidden` | `Unauthorized` / `Forbidden` |
| other non-retryable 4xx | `rejected` | `JanusError` |
| TTL ran out while retrying | `expired` | `LeaseExpired` |

Retryable failures — network errors, 5xx (including a sealed server), 408, 429 —
are reported as **non-terminal** events with `error` set and retried while TTL
headroom remains. Renewal is capped server-side at the role's max TTL, so the
loop stops at that ceiling rather than retrying forever: treat `max_ttl` as
"acquire a new lease".

`RenewEvent` is value-free — it carries the lease id and timings, never the
password, and the SDK logs nothing on its own.

**Thread safety.** `id`, `username` and `password` are immutable. The expiry is
not: while a renewer is running, read it with `lease.expiry()` /
`lease.max_expiry()` (both take the lease's lock and return aware `datetime`s)
rather than the raw `expires_at` string the renewer writes.

**Revoke failures.** `dynamic_lease` never replaces your exception. If the block
raised *and* the revoke failed, your exception propagates with the
`RevokeFailed` attached as its `janus_revoke_error` attribute and also reported
to `on_event` with reason `revoke_failed`. If only the revoke failed,
`RevokeFailed` is raised.

## Groups

A [group](groups.md) is a subject a role binding can target instead of a user.
The SDK covers the group **catalog** — enough for a provisioning script such as
an HR-driven joiner/leaver job to keep local groups in step with a directory:

```python
from janus_client import GROUP_KIND_LOCAL

group = client.create_group("Team Payments", GROUP_KIND_LOCAL)

client.add_group_member(group.id, user_id)
client.remove_group_member(group.id, user_id)

groups = client.list_groups()                        # walks every cursor page
members = client.list_group_members(group.id)
client.set_group_project_creation(group.id, True)    # delegated project creation
client.delete_group(group.id)
```

**These need instance-scoped `group:manage` (admin or owner).** The usual
credential for this SDK — a config- or environment-scoped `janus_svc_...` read
token — raises `Forbidden` from all of them. The one exception is
`client.my_groups()`, which is authenticated-only, returns the caller's own
memberships, and answers a service token with an empty list rather than an
error, so it is safe to call unconditionally.

Two things the SDK will not let you get wrong:

- **The two-kinds rule is checked locally.** `create_group` raises `ValueError`
  for an `oidc` group with no `claim_value`, and for a `local` group carrying
  one, before any request — an `oidc` group is defined *by* its claim value, and
  a `local` group's membership is the explicit list. (`ValueError`, not
  `JanusError`: it is client misuse, not an API failure.)
- **`add_group_member` is for `local` groups only.** An `oidc` group's
  membership is a snapshot refreshed at each sign-in and the schema makes a
  hand-added row unrepresentable, so the server answers `409`; check `kind` first
  if you want to fail before the request.

`Group.members_seen` is named that on purpose. For an `oidc` group it counts only
users who have actually signed in — Janus has never seen a token for anyone else
— so it is not the identity provider's membership list and must not be presented
as the size of the team. `list_group_members` has the same caveat.

### Bindings are not in the SDK

Granting a group a role at a scope is deliberately absent. It is a *different*
authority (`member:manage` at that scope, capped by your own bound role, three
route families keyed by scope) and it grants **durable access** — the kind of
change that should be planned, diffed and reviewed rather than made in one line
from application code. Use Terraform's
[`janus_group_binding`](terraform.md#groups-as-code), `janus group bind`, or the
UI.

## Full reference

See [`sdk/python/README.md`](../../sdk/python/README.md) for the complete API
surface, a runnable example, and the caching/security model.
