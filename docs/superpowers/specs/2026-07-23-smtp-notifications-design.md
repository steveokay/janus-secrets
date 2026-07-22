# SMTP notification channel — design

**Date:** 2026-07-23
**Status:** approved (all recommendations accepted 2026-07-23)

## Problem

Notifications (PR #100) deliver rotation/sync failures, denials, and pending
approvals to **webhook** and **Slack** channels. Many teams run alerting through
email; there is no email channel today. Add `smtp` as a third channel type,
reusing the existing dispatcher, delivery outbox, value-free rendering, and
master-key-wrapped credential model.

## Decisions

- **Transport: stdlib `net/smtp` (no new dependency).** `tls_mode` selects the
  connection: `starttls` (default, e.g. port 587 — dial plaintext then upgrade
  with `STARTTLS`), `implicit` (e.g. port 465 — `tls.Dial` first), `none`
  (plaintext, discouraged, no auth allowed). Certificate verification is ON;
  a per-channel `insecure_skip_verify` flag (off unless explicitly set,
  documented as a footgun) supports self-hosted relays with self-signed / private
  CA certs.
- **Auth optional.** When `username` is set, authenticate with
  `smtp.PlainAuth`; the stdlib refuses PLAIN over an unencrypted connection, so
  auth is effectively TLS-only (correct). Empty `username` ⇒ no auth.
- **Multiple recipients.** `to` is one or more addresses; `from` is single.
- **Value-free, same as the other channels.** The email body carries only event
  kind / resource path / category — never a secret value. The SMTP `password`
  is write-only: master-key-wrapped, never returned by the API, re-wrapped by
  master-key rotation, excluded from backups (like the webhook HMAC key and OIDC
  client secret).

## Data model

**Migration 000027** alters the `notification_channels.type` CHECK constraint
from `IN ('webhook','slack')` to `IN ('webhook','slack','smtp')`. Down reverts
to the two-value set (fails if any `smtp` row exists — acceptable for a down
migration). No other schema change: the SMTP settings live inside the existing
opaque `config_ct` ciphertext.

## Components

### `channelConfig` (internal/notification/service.go)

Extend the wrapped config struct with SMTP fields (all `omitempty`):

```
Host, From          string
Port                int
Username, Password  string   // password write-only
To                  []string
TLSMode             string   // "starttls" | "implicit" | "none"
InsecureSkipVerify  bool
```

`URL`/`HMACKey` remain for webhook/slack. wrap/unwrap are unchanged (the struct
is JSON-marshalled then encrypted under the channel-config AAD).

### Validation (service.go)

- `validateType` accepts `webhook | slack | smtp`.
- For `smtp`: require `host`, `port` (1–65535), a valid `from`, ≥1 `to`;
  `tls_mode` in the allowed set (default `starttls` when empty); reject
  `hmac_key` (webhook-only) and ignore `url`. `insecure_skip_verify` only
  meaningful for `starttls`/`implicit`.
- Password/username optional; if `username` set, `password` may be set.

### Sender (internal/notification/providers.go)

- `send(...)` gains `case "smtp": return s.sendSMTP(ctx, cfg, p)`.
- **`buildMessage(cfg, p) []byte`** — PURE, unit-testable: RFC 5322 message with
  `From`, `To` (all recipients), `Subject: Janus: <humanKind(event)>`, `Date`,
  `MIME-Version`, `Content-Type: text/plain; charset=utf-8`, and a body reusing
  the existing value-free renderer (factor `slackText`'s core into a shared
  plain-text renderer, or add `emailBody(p)`). CRLF line endings; header
  injection guarded (reject/strip CR/LF in `from`/`to`/subject inputs — the
  event kind is from a fixed set, but be defensive).
- **`sendSMTP(ctx, cfg, p)`** — dial per `tls_mode`, `STARTTLS`-upgrade when
  `starttls`, build `tls.Config{ServerName: host, InsecureSkipVerify: cfg.InsecureSkipVerify}`,
  `PlainAuth` when `username` set, `MAIL FROM`/`RCPT TO`(each recipient)/`DATA`.
  Honour `ctx` (dial deadline). Any protocol/transport error returns an error so
  the outbox retries with backoff (existing behaviour).

### API / CLI / web

- **API** (`internal/api/notification_handlers.go`): the create/update request
  gains the SMTP fields; map them into the service input. Response never includes
  the password (write-only). Document in `docs/openapi.yaml`.
- **CLI** (`janus notifications create`): add `--type smtp` with
  `--smtp-host/--smtp-port/--smtp-from/--smtp-to (repeatable or comma)/--smtp-username/--smtp-password/--smtp-tls/--smtp-insecure-skip-verify`.
  Password read from flag or stdin (never echoed), consistent with other
  credential-bearing CLI flows.
- **Web** (`web/src/screens/Notifications.svelte` + `lib/api.ts`): the create
  form gains an SMTP type with host/port/from/to/username/password/tls-mode/
  skip-verify fields; the password is write-only (send-only, never displayed),
  mirroring the existing webhook-HMAC / Slack credential handling. Atrium tokens,
  both themes.

## Testing

- **service** — `buildMessage`: value-free (a sentinel secret value never appears),
  correct headers, all recipients in `To`/`RCPT`, CRLF, header-injection guard;
  SMTP validation table (missing host/port/from/to, bad tls_mode, hmac rejected);
  wrap/unwrap round-trip preserves SMTP fields; `sendSMTP` against a tiny
  in-process SMTP stub (a `net.Listen` server speaking minimal SMTP) covering the
  no-auth and PlainAuth paths and a `none`/`starttls` mode; an error from the
  server surfaces as a delivery error.
- **store** — migration up allows an `smtp` row; down/up idempotent.
- **api** — create an smtp channel; the password is not echoed in create/get/list
  responses; validation errors surface as 4xx.
- **leak** — the SMTP password and any secret value never appear in the outbox
  payload, logs, or API responses (extend the existing notification leak test).

## Non-goals (YAGNI)

- No HTML email, templates, or attachments — plain text only.
- No OAuth2 / XOAUTH2 SMTP auth (PLAIN over TLS only).
- No per-recipient routing rules — a channel fans one event to all its `to`.
- No connection pooling / keep-alive — one connection per delivery (the outbox
  cadence is low).
