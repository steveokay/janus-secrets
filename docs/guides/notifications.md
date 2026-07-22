# Notifications — alerting on failures

Rotation and sync run on a schedule; dynamic leases expire; promotions wait for
approval. **Notifications** push these events to a webhook or Slack so someone
finds out without watching the UI.

## What you can be alerted on

A channel subscribes to one or more event kinds:

| Event | Fires when |
|---|---|
| `rotation.failed` | a rotation policy fails (after its retry/backoff) |
| `sync.failed` | a sync target fails to reconcile |
| `promotion.pending` | a promotion request is filed and awaits approval |
| `access.denied` | any request is denied by RBAC (a 403) |

Notifications are rendered from the **audit log**, which never contains a secret
value — an alert carries the event kind, the resource **path/name**, the actor,
and a short category detail, never a secret.

## Create a channel

**Web:** Notifications → *New channel*. Pick webhook, Slack, or **email
(SMTP)**, fill in the destination, tick the events, save. Secrets on the
channel — the webhook HMAC key and the SMTP password — are write-only: Janus
stores them encrypted (master-key-wrapped) and never shows them again.

**CLI:**

```bash
# Generic webhook, HMAC-signed, on rotation + sync failures.
janus notifications create --name ops-alerts --type webhook \
  --url https://hooks.example.com/janus \
  --hmac-key "$(openssl rand -hex 32)" \
  --events rotation.failed,sync.failed

# Slack incoming webhook for pending approvals + denials.
janus notifications create --name sec-slack --type slack \
  --url https://hooks.slack.com/services/T000/B000/XXXX \
  --events promotion.pending,access.denied

# Email via SMTP (STARTTLS on 587), authenticated, to two recipients.
janus notifications create --name ops-email --type smtp \
  --smtp-host smtp.example.com --smtp-port 587 \
  --smtp-from janus@example.com \
  --smtp-to oncall@example.com --smtp-to sec@example.com \
  --smtp-username janus --smtp-password "$SMTP_PASSWORD" \
  --smtp-tls starttls \
  --events rotation.failed,sync.failed,access.denied

janus notifications test <id>          # send a synchronous test
janus notifications deliveries <id>    # recent delivery history (value-free)
janus notifications update <id> --disable
```

Managing channels needs the instance **`notification:manage`** action
(admin/owner).

## Webhook format

A webhook receives an HTTP `POST` with a JSON body like:

```json
{
  "event": "rotation.failed",
  "seq": 4211,
  "occurred_at": "2026-07-21T09:15:04Z",
  "action": "rotation.rotate",
  "result": "failure",
  "resource": "configs/…/secrets/DB_PASSWORD",
  "actor": "rotation:…",
  "detail": "apply failed"
}
```

If the channel has an HMAC key, the body is signed with
`X-Janus-Signature: sha256=<hex>` (HMAC-SHA256 over the raw body) so you can
verify it came from your Janus instance. Slack channels instead receive a
compact `{"text": …}` message.

## Email (SMTP)

An SMTP channel sends a plain-text email — a value-free summary of the event
(kind, resource path, actor, result), never a secret value — to one or more
recipients. It carries the same information as the webhook body, formatted for a
human inbox with a `Janus: <event>` subject.

Connection settings:

- **`smtp_tls_mode`** — `starttls` (default; connect on the submission port, e.g.
  587, then upgrade with `STARTTLS`), `implicit` (TLS from the first byte, e.g.
  port 465), or `none` (plaintext — discouraged, and authentication is disabled
  in this mode).
- **Auth** is optional: set `smtp_username`/`smtp_password` for relays that
  require it. The password is sent only over an encrypted connection and is
  stored write-only.
- **Certificate verification is on by default.** For a self-hosted relay with a
  self-signed or private-CA certificate, either add that CA to the host trust
  store, or set **`smtp_insecure_skip_verify`** on the channel — a deliberate
  opt-out that disables certificate checking for that channel. Treat it as a
  footgun: it exposes delivery to a man-in-the-middle and should be limited to a
  trusted internal network.

Use `janus notifications test <id>` to send a synchronous test email and confirm
the host, credentials, and TLS mode before relying on the channel.

## Delivery & reliability

Delivery is decoupled from the event: a dispatcher tails the audit log and
enqueues each matching event into an outbox, then delivers it, retrying with
exponential backoff (1m→1h, up to six attempts) so a brief channel outage does
not lose an alert. Tune the interval with `JANUS_NOTIFY_TICK` (default `30s`;
`0` disables). The dispatcher is idle while the server is sealed.

> Notification channels are **not** part of an instance backup (they are
> operational config, and the delivery cursor is seeded to the audit head).
> Recreate them after a disaster-recovery restore.
