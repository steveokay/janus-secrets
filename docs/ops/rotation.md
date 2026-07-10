# Secret rotation

A rotation policy rotates one secret key's value on a fixed interval, without
an operator manually running `janus secrets set`. Two rotator types:

- **`postgres`** — single-role reset. Janus connects with an operator-supplied
  admin DSN and runs `ALTER ROLE <role> WITH PASSWORD <new-random>`, then
  stores the new password as the secret's new config version. No coordination
  with the application is needed beyond it re-reading the secret.
- **`webhook`** — Janus generates a new random value itself and POSTs it,
  HMAC-signed, to an operator-supplied URL. The operator's endpoint is
  responsible for applying the new value (rotating a third-party API key,
  updating an external system, etc.); Janus commits the new value as a config
  version only after the endpoint answers with a 2xx.

Either policy type may also carry an optional **notify webhook**, which fires
a separate, value-free event after a rotation succeeds — useful for alerting
or triggering a downstream redeploy without handling the secret itself.

## Crash-safe design

Rotation is built to survive a crash at any point mid-rotation:

1. The new value is generated and its ciphertext is **persisted (encrypted)
   as a pending value BEFORE it is applied anywhere**.
2. The rotator applies it (`ALTER ROLE`, or the webhook POST).
3. Only after a successful apply does Janus commit the pending value as a new
   config version and clear the pending marker.

If the server crashes between steps 1 and 3, the next scheduler tick resumes
the policy and **re-applies the same pending value** — it does not generate a
new one. This is why rotation is idempotent by design, and why **webhook
receivers must also be idempotent**: a retried rotation (after a crash, or a
timeout that Janus treats as a failure) sends the identical `new_value` it
sent before.

## Webhook receiver contract

For `webhook`-type policies, the operator's endpoint must implement:

- **Signature header:** every request carries `X-Janus-Signature:
  sha256=<hex>`, the HMAC-SHA256 of the raw request body under the policy's
  configured `hmac_key`. Verify it with a constant-time comparison
  (`hmac.Equal` in Go, or equivalent) before trusting the body — never a
  plain `==` string compare.
- **Idempotency:** Janus may resend the same body more than once (retry after
  a network error, or resumption after a crash). Applying the same
  `new_value` twice must be a no-op the second time.
- **2xx only after durable apply:** return 2xx only once the new value has
  been durably applied on your end. Janus treats any non-2xx (or a timeout)
  as a failure and will retry with backoff (see below) — it does **not**
  commit the config version until it sees a 2xx.

Request bodies are JSON:

- Rotator POST (to the policy's `url`): `{"policy_id","secret_key","new_value"}`
- Notify POST (to the policy's optional `notify_url`, value-free):
  `{"policy_id","project_id","config_id","secret_key","new_version","rotated_at"}`

## Postgres admin least privilege

The `admin_dsn` given to a `postgres`-type policy only needs enough privilege
to run `ALTER ROLE <target> WITH PASSWORD ...` — it does **not** need to be a
superuser. Grant the connecting role `CREATEROLE`, or make it an owner/admin
of the specific target role, and nothing more. Recommend provisioning a
dedicated, narrowly-scoped Postgres role for rotation rather than reusing an
application or superuser DSN.

## Sealed behavior

Rotation pauses entirely while the server is sealed: the scheduler checks
seal status each tick and skips all rotations until the server is unsealed.
A sealed window is **not** treated as a rotation failure — policies are never
marked `failed`, and `last_error`/failure counts are untouched, purely
because the server happened to be sealed when a rotation came due. Once the
server is unsealed, any policy whose `next_rotation_at` has already passed
rotates on the next tick.

## Failure handling & backoff

A failed rotation attempt (rotator error, non-2xx webhook response, timeout)
is retried with exponential backoff: base delay **1 minute**, doubling on
each consecutive failure, capped at **1 hour**. `last_error` stores a
value-free failure category (never the secret value, DSN, or HMAC key).

After **5 consecutive failures**, the policy's status is set to `failed` and
the scheduler stops auto-retrying it — it will not rotate again until an
operator intervenes. Resume a failed policy one of two ways:

```sh
janus rotation rotate <id>                       # manual rotate: clears `failed`, retries immediately
janus rotation update <id> --status active       # reset status without forcing an immediate attempt
```

## Scheduler

The rotation scheduler runs in-process alongside the server (no separate
worker/cron process to deploy). It is controlled by one environment
variable:

| Variable | Default | Meaning |
|---|---|---|
| `JANUS_ROTATION_TICK` | `60s` | Go duration between scheduler passes. Set `0` to disable the scheduler on this instance (policies still exist and can be rotated manually via `janus rotation rotate`, but nothing rotates automatically) |

The scheduler stops on graceful shutdown (SIGTERM) along with the rest of
the server; there is nothing extra to drain.

## CLI usage

```sh
# Postgres rotator: reset app's DB role password every 30 days.
janus rotation create --config $CONFIG --key DB_PASSWORD \
  --type postgres --interval-seconds 2592000 \
  --admin-dsn postgres://rotator@db:5432/app --role app

# Webhook rotator: rotate an API key weekly via your own endpoint.
janus rotation create --config $CONFIG --key API_KEY \
  --type webhook --interval-seconds 604800 \
  --url https://internal.example.com/rotate-hook --hmac-key $HMAC_KEY

# List, inspect, update, rotate, delete.
janus rotation list --project $PROJECT
janus rotation get <id>
janus rotation update <id> --status active
janus rotation rotate <id>
janus rotation delete <id>
```

Both `create` variants also accept `--notify-url`/`--notify-hmac-key` for the
optional post-rotation notify webhook, and `--password-len` (postgres type,
default 32) to size the generated password.

## RBAC & audit

Creating, listing, updating, deleting, or manually rotating a policy requires
the `rotation:manage` permission, granted to the project **admin** and
**owner** roles. Every create/update/delete/rotate call is audited, and every
rotation attempt — scheduled or manual, success or failure — emits a
`rotation.rotate` audit event.

Secret values, admin DSNs, and HMAC keys never appear in logs, audit entries,
`last_error`, or API responses — `GET`/`list` responses mask this
configuration the same way secret values are masked elsewhere in the API.

## Backup & restore

`rotation_policies` rows are included in `janus backup` / `janus restore`
like any other table: the encrypted rotator configuration (admin DSN, HMAC
key, generated-password length, etc.) travels with the rest of the
key-preserving dump. A restored instance — once unsealed with the original
unseal material — keeps its rotation policies and resumes scheduling them on
the next tick. See [backup-restore.md](backup-restore.md) for the general
backup/restore procedure.
