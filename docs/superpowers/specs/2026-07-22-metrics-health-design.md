# Prometheus metrics + health panel — design

**Date:** 2026-07-22
**Status:** approved (all recommendations accepted 2026-07-22)

## Problem

Janus is a black box until it breaks. "Reads 24h" (audit-derived) is the only
metric surfaced today; there is no scrape endpoint for a monitoring stack and no
operator-facing view of engine/DB/audit health. This is the #1 operability gap
for self-hosters.

Two deliverables in one slice, plus a small logging-config win:

1. A Prometheus **`/metrics`** exposition endpoint.
2. A **health panel** in Settings backed by an admin status endpoint.
3. `JANUS_LOG_LEVEL` / `JANUS_LOG_FORMAT` env vars.

## Decisions

- **Exposition: hand-rolled, zero new dependency.** A small `internal/metrics`
  package renders the Prometheus text exposition format directly (counters,
  gauges, a fixed-bucket histogram). No `client_golang`. Go-runtime numbers come
  from `runtime`/`runtime.ReadMemStats`.
- **`/metrics` auth: static bearer token, off by default.** `/metrics` returns
  `404` unless `JANUS_METRICS_TOKEN` is set; when set, the request must carry
  `Authorization: Bearer <token>`, compared in constant time. Maps directly to
  Prometheus `bearer_token`. Nothing is exposed until the operator opts in.
- **Health panel: admin-gated.** `GET /v1/sys/status` requires auth + the same
  instance-level read authority used by the metrics/audit read endpoints. The
  public `/health` and `/ready` probes are unchanged.

## `internal/metrics` package

A tiny registry, safe for concurrent update and scrape:

- `Counter` / `Gauge` — `float64` under a mutex or atomics, with a label set.
- `Histogram` — fixed buckets for request latency
  (`.005,.01,.025,.05,.1,.25,.5,1,2.5,5,10` seconds) + `_sum`/`_count`.
- `Registry.WriteTo(w)` renders `# HELP`/`# TYPE` + samples in text format,
  with correct label escaping. Metric+label names validated at construction.
- A package-level `Default` registry holds the app metrics; collectors that read
  live state at scrape time (DB pool, runtime, seal state) register a callback
  invoked by `WriteTo`.

Label cardinality is bounded deliberately: the HTTP `route` label is the **chi
route pattern** (`RoutePattern()`, e.g. `/v1/projects/{pid}/configs`), never the
raw path, so per-id explosion can't happen.

### Metric set (`janus_` namespace)

| Metric | Type | Labels | Source |
|---|---|---|---|
| `janus_build_info` | gauge (=1) | `version`,`commit` | `internal/version` |
| `janus_start_time_seconds` | gauge | — | process start |
| `janus_sealed` | gauge (1/0) | — | keyring/unsealer |
| `janus_http_requests_total` | counter | `method`,`route`,`status` | request middleware |
| `janus_http_request_duration_seconds` | histogram | `method`,`route` | request middleware |
| `janus_audit_head_seq` | gauge | — | audit head (cheap) |
| `janus_scheduler_last_tick_seconds` | gauge | `engine` | scheduler tick hook |
| `janus_rotation_runs_failed` | gauge | — | `rotation_runs` count(status=failed) |
| `janus_sync_runs_failed` | gauge | — | `sync_runs` count(status=failed) |
| `janus_dynamic_leases_active` | gauge | — | `dynamic_leases` count(status=active) |
| `janus_db_pool_conns` | gauge | `state`=total/idle/acquired/max | `pgxpool.Stat()` |
| `janus_go_goroutines` | gauge | — | `runtime.NumGoroutine` |
| `janus_go_heap_alloc_bytes` | gauge | — | `ReadMemStats` |
| `janus_go_gc_pause_seconds_total` | gauge | — | `ReadMemStats` |

Chain-verify is O(events) and is **not** scraped (kept on the existing on-demand
`/v1/audit/verify`); `/metrics` stays cheap. The DB-derived engine gauges are a
few indexed `COUNT`s run at scrape time with a bounded context timeout and a
short (~5s) in-memory cache so a rapid scrape cadence can't hammer Postgres.

### HTTP instrumentation

Extend the existing `requestLogger`/`statusWriter` path in
`internal/api/middleware.go` (or a sibling middleware mounted next to it) to
`Inc` the requests counter and `Observe` the duration histogram, keyed by
method + `chi.RouteContext(r).RoutePattern()` + status. `/metrics` itself is
excluded from instrumentation.

### `/metrics` endpoint + middleware

Mounted at root `GET /metrics` (outside `/v1`). A `metricsAuth` middleware:
`404` when no token configured; otherwise `401` unless the bearer token matches
(`crypto/subtle.ConstantTimeCompare`). Wired from a new `BootConfig`/server
field populated from `JANUS_METRICS_TOKEN`.

## Health panel

### `GET /v1/sys/status` (admin)

Returns a value-free operational snapshot (no secrets, no per-user data):

```json
{
  "version": "0.1.0", "commit": "abc1234",
  "uptime_seconds": 8123, "sealed": false, "seal_type": "shamir",
  "db": { "reachable": true, "latency_ms": 2,
          "pool": { "total": 4, "idle": 3, "acquired": 1, "max": 10 } },
  "audit": { "head_seq": 10432, "event_count": 10432 },
  "schedulers": {
    "rotation": { "enabled": true, "last_tick_age_seconds": 12, "interval_seconds": 60 },
    "sync":     { "enabled": true, "last_tick_age_seconds": 12, "interval_seconds": 60 },
    "dynamic":  { "enabled": true, "last_tick_age_seconds": 12, "interval_seconds": 60 }
  },
  "runs": { "rotation_failed": 0, "sync_failed": 0 },
  "leases": { "active": 3 }
}
```

`db.latency_ms` = time of a `pool.Ping` with a short timeout (`reachable:false`
on error, never 500 the panel). Scheduler tick ages come from a shared in-memory
"last tick" timestamp each scheduler stamps at the top of every `RunDue` loop
(a small `metrics`/health hook the three schedulers call); `interval_seconds`
from the configured `JANUS_*_TICK`. `enabled:false` when its tick is `0`.

### Scheduler tick tracking

The rotation/sync/dynamic scheduler loops each call a tiny
`health.MarkTick("rotation")`-style hook per tick (updates an atomic timestamp
in the shared registry). Same source feeds both `janus_scheduler_last_tick_seconds`
and the status endpoint. This is the only change to the engine packages.

### Settings panel

A new "Health" `op-section` in `web/src/screens/Settings.svelte` (Atrium tokens,
both themes): a compact grid of DB latency + pool, seal state, uptime/version,
audit head seq + event count, and a per-engine row (tick age + failed-run count,
with a warning accent when a scheduler's tick age exceeds ~3× its interval or a
failed-run count is non-zero). Loads on mount via `api.sysStatus()`; a manual
Refresh; `errorMessage` on failure. Read-only — no actions.

## Logging config

`JANUS_LOG_LEVEL` (`debug|info|warn|error`, default `info`) and
`JANUS_LOG_FORMAT` (`text|json`, default `text`) select the `slog.Handler`
level + format at boot (`cmd/janus/server.go`, following the existing `JANUS_*`
parsing). Invalid values warn and fall back.

## Testing

- **metrics package** — counter/gauge/histogram math, bucket boundaries,
  exposition text output (golden), label escaping, name validation, concurrent
  update + scrape race.
- **api** — `/metrics` 404 without token, 401 wrong token, 200 + valid text with
  token (assert a few expected series incl. the request counter incrementing);
  `metricsAuth` constant-time path; `/v1/sys/status` authz (admin only), shape,
  and DB-unreachable degradation (reachable:false, still 200); HTTP middleware
  records under the route pattern not the raw path.
- **leak** — `/metrics` and `/v1/sys/status` never expose a secret value, token,
  or per-user datum (route labels are patterns; assert no `janus_svc_`/secret
  material in output).
- **openapi** — `/v1/sys/status` documented (drift test). `/metrics` is outside
  `/v1` and not part of the OpenAPI surface — confirm the drift walker ignores
  non-`/v1` routes (it already does; verify).

## Non-goals

- No `client_golang` / OpenTelemetry / push gateway.
- No chain-verify on every scrape (stays on-demand).
- No historical storage — `/metrics` is instantaneous; history lives in the
  operator's Prometheus.
- No per-endpoint SLO/alerting rules shipped (operator's concern).
