# How-to: run rotation, sync, and dynamic credentials from the UI

**Operations** manages the three Phase-3 engines end to end — including
creation, which previously required the CLI. Deep behavior references:
[rotation](../ops/rotation.md), [sync](../ops/sync.md),
[dynamic](../ops/dynamic.md).

A shared rule: **credential fields are write-only**. Admin DSNs, PATs,
service-account tokens, CA certs, and HMAC keys are encrypted at rest and
never shown again — the UI never renders them from fetched data.

## Static rotation

**+ New policy** → pick the config, the secret key to rotate, the rotator:

- **postgres** — admin DSN + the DB role whose password gets rotated
  (`ALTER ROLE`), then written back to the secret as a new config version
- **webhook** — your endpoint URL + HMAC key; Janus generates the value and
  notifies the hook

Set the cadence in days. Per policy: **Rotate now**, **Pause/Resume**,
**Runs** (timing, attempts, sanitized errors, resulting version — never a
value), and delete. A failing policy surfaces in the Overview in-tray.

## Sync targets

**+ New target** → source config + provider:

- **GitHub Actions** — owner, repo, optional environment, and a PAT. Secrets
  are pushed with NaCl sealed-box encryption.
- **Kubernetes** — API server URL, namespace, Secret name, service-account
  token, and the cluster **CA certificate (required — TLS is verified
  against it, by design)**.

Push cadence in minutes; keyed-HMAC change detection means unchanged configs
don't re-push. Per target: **Push now**, **Pause/Resume**, **Runs**, delete
(already-pushed secrets remain at the destination — Janus only stops
updating them).

## Dynamic Postgres credentials

**+ New role** → config scope, role name, TTL/max-TTL, admin DSN, and the
creation/revocation SQL templates (placeholders: name, password, expiration
in double braces).

Then **Issue credentials**: Janus executes the creation SQL and shows the
generated username/password **exactly once** — copy them or lose them; they
are never persisted or audited. Each issuance is a **lease** with a TTL bar:
**Renew** extends it (monotonic, capped at max-TTL), **Revoke** drops the DB
role immediately; expiry revokes automatically, and a startup sweep revokes
leases orphaned by a crash.

## Dev-environment gotcha

If the server runs on your host and Postgres in Docker, DSNs must be
host-reachable (`127.0.0.1:5434` in the dev compose), not the compose-internal
hostname (`postgres:5432`) — the engines connect from the server process.
