# Audit shipping — stream the audit log to a SIEM

Janus keeps a tamper-evident, append-only **audit log** (hash-chained; see the
[operations overview](../operations.md)). The **audit shipper** streams that log
to an external SIEM — a **webhook** or a **syslog** collector — as
newline-delimited JSON, so your central logging pipeline holds a durable copy for
alerting, retention, and correlation with the rest of your fleet.

Audit events have **no value field by construction**, so shipping them never
exposes a secret value. The only secret involved — an optional webhook HMAC
signing key — comes from the environment and is never logged or persisted.

## How it works — tailing with a high-water mark

The shipper mirrors the notification dispatcher: on each tick it reads the audit
events **since its own durable high-water mark**, serializes them as JSONL, sends
the batch to the configured destination, and **advances the mark only after a
successful send**.

- The mark is a single-row Postgres value (`audit_ship_state.last_seq`), seeded
  at migration time to the **current audit head**, so enabling the shipper never
  replays the entire history from the beginning of time.
- A **failed send leaves the mark untouched** — the next tick retries the same
  batch from the same seq. This gives **at-least-once** delivery with **no
  gaps**: no event is ever skipped, though after a crash-with-partial-send a few
  events may be re-shipped. Your SIEM should **dedupe on `seq`** (it is
  monotonic and unique per event).
- The shipper keeps its **own** cursor, separate from the notification cursor, so
  the two tail the log independently.

The tick interval is `JANUS_AUDIT_SHIP_TICK` (Go duration, default `30s`; `0`
disables). The shipper is a clean no-op until a destination mode is configured.

## The JSONL event shape

Each event is one compact JSON object per line (one canonical shape a SIEM can
key off):

```json
{"seq":1024,"occurred_at":"2026-07-23T09:14:05.12Z","actor_kind":"user","actor_id":"a1b2","actor_name":"alice@example.test","action":"secret.reveal","resource":"projects/demo/prod/DB_URL","result":"success","ip":"203.0.113.7","prev_hash":"9f...","hash":"c3..."}
```

- `prev_hash` / `hash` are the hex-encoded hash-chain links, so a downstream
  consumer can re-verify chain integrity if it wants to.
- Empty optional fields (`actor_id`, `detail`, `result_code`) are omitted.
- `result` is `success` | `denied` | `error`; denied/error events carry a
  `result_code` and often a non-secret `detail` (e.g. `role=viewer`).

## Destination: webhook

```bash
export JANUS_AUDIT_SHIP_MODE=webhook
export JANUS_AUDIT_SHIP_WEBHOOK_URL=https://siem.example.test/ingest
# optional: sign the body so the receiver can verify origin
export JANUS_AUDIT_SHIP_WEBHOOK_HMAC_KEY=change-me-to-a-long-random-value
```

The shipper POSTs the batch as `Content-Type: application/x-ndjson` (one JSON
object per line, trailing newline). When an HMAC key is set it adds:

```
X-Janus-Signature: sha256=<hex HMAC-SHA256 of the exact body>
```

The receiver recomputes the HMAC over the raw body with the shared key and
compares. TLS certificates are verified; the URL must be an absolute `http(s)`
URL (a non-http scheme is a fatal boot error). A non-2xx response is treated as a
failed send and retried next tick.

## Destination: syslog

```bash
export JANUS_AUDIT_SHIP_MODE=syslog
export JANUS_AUDIT_SHIP_SYSLOG_ADDR=logs.example.test:514
export JANUS_AUDIT_SHIP_SYSLOG_NETWORK=udp   # or tcp (default: udp)
```

The shipper emits one **RFC 5424** message per event:

```
<109>1 2026-07-23T09:14:05.12Z <hostname> janus <seq> audit - {"seq":...}
```

- Priority `109` = facility `13` (log audit) × 8 + severity `5` (notice).
- `PROCID` carries the audit `seq` for traceability; `MSGID` is `audit`; the
  `MSG` field is the JSON event verbatim.
- Over **TCP** messages use RFC 6587 octet-counting framing (`<len> <msg>`) so a
  collector can delimit them on the stream; over **UDP** each message is one
  datagram.

The syslog writer is hand-rolled over `net` (not stdlib `log/syslog`) so it
builds and runs on every platform, including Windows.

## Checking status

`GET /v1/sys/status` (instance admin/owner) includes a value-free `audit_ship`
block when a destination is configured:

```json
"audit_ship": {
  "mode": "webhook",
  "destination": "webhook",
  "high_water_seq": 1024,
  "last_ship_at": "2026-07-23T09:14:35Z",
  "last_ship_count": 12,
  "last_error": ""
}
```

`last_error` is a **sanitized category** (e.g. `connection failed`,
`send timed out`, `destination returned HTTP 500`) — never a URL or host that
might embed a token.

## SIEM ingestion notes

- **Dedupe on `seq`.** At-least-once means the occasional replay after a crash;
  `seq` is monotonic and unique, so it is a safe idempotency key.
- **Order.** Events are shipped in ascending `seq` within a batch and across
  batches; the high-water mark only moves forward.
- **Retention & verification.** Because `prev_hash`/`hash` are shipped, a SIEM
  (or an offline job) can re-walk the chain to detect tampering independently of
  Janus's own `GET /v1/audit/verify`.
- **Backpressure.** If the destination is down, the mark stalls and the audit log
  keeps growing in Postgres (it is never trimmed by the shipper) — events are
  shipped once the destination recovers. Batches are bounded (500 events/tick),
  so a large backlog drains over several ticks.
- **Not a backup.** Shipper config lives in the environment, not in a Janus
  backup; the high-water mark is per-instance state — a restored instance
  re-ships from wherever its restored `audit_ship_state` sits.
