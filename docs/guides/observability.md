# Observability — metrics, health, and logs

Janus exposes three operator-facing signals: a Prometheus **`/metrics`** scrape
endpoint, an admin **health panel** (backed by `GET /v1/sys/status`), and
structured **logs** whose level and format are configurable. None of them ever
carry a secret value — metrics are counts and gauges, the status endpoint is a
value-free operational snapshot, and the audit-grade "no plaintext in logs" rule
still holds.

This guide covers turning on and scraping metrics, reading the health panel, and
tuning logs. For the deeper "Reads 24h" usage aggregate in the dashboard, see
the web UI guide; for the audit chain and its on-demand verify, see the audit
sections of [members-and-rbac.md](./members-and-rbac.md) and the app itself.

**In a hurry?** A ready-made Grafana dashboard and a set of example alerting
rules ship in [`deploy/grafana/`](../../deploy/grafana/) — import the JSON,
load the rules, done. See [Dashboards and alerts](#dashboards-and-alerts) below.

## Prometheus metrics

### Turning it on

`/metrics` is **disabled by default** — it returns `404` until you set a scrape
token. Set `JANUS_METRICS_TOKEN` to any high-entropy string and restart:

```sh
JANUS_METRICS_TOKEN=$(openssl rand -hex 32)
```

Once set, the endpoint requires that token as a bearer credential (constant-time
compared); a missing or wrong token returns `401`. There is no unauthenticated
mode — treat the token like any other secret and scope network access to the
port as well.

### Scraping

Point Prometheus at the instance with a `bearer_token`:

```yaml
scrape_configs:
  - job_name: janus
    metrics_path: /metrics
    scheme: https              # if TLS terminates in front of Janus
    authorization:
      credentials: <JANUS_METRICS_TOKEN>
    static_configs:
      - targets: ['janus.internal:8200']
```

A quick manual check:

```sh
curl -H "Authorization: Bearer $JANUS_METRICS_TOKEN" http://127.0.0.1:8200/metrics
```

### What's exposed

All series are in the `janus_` namespace. Nothing here is a secret; the HTTP
`route` label is the **route pattern** (e.g. `/v1/projects/{pid}/configs`), never
the concrete path, so ids and values never appear and label cardinality stays
bounded (unmatched routes collapse to `route="unmatched"`).

| Metric | Type | Meaning |
| --- | --- | --- |
| `janus_build_info{version,commit}` | gauge | Build identity (always 1). |
| `janus_start_time_seconds` | gauge | Process start (Unix seconds); subtract for uptime. |
| `janus_sealed` | gauge | 1 while sealed, 0 unsealed. |
| `janus_http_requests_total{method,route,status}` | counter | Request count. |
| `janus_http_request_duration_seconds{method,route}` | histogram | Request latency. |
| `janus_audit_head_seq` | gauge | Sequence number of the audit chain head. |
| `janus_scheduler_last_tick_seconds{engine}` | gauge | Last tick time of each engine loop (`rotation`/`sync`/`dynamic`). |
| `janus_rotation_runs_failed` | gauge | Rotation runs currently in the failed state. |
| `janus_sync_runs_failed` | gauge | Sync runs currently in the failed state. |
| `janus_dynamic_leases_active` | gauge | Active dynamic-credential leases. |
| `janus_db_pool_conns{state}` | gauge | pgx pool connections by `state` (total/idle/acquired/max). |
| `janus_go_goroutines` | gauge | Live goroutines. |
| `janus_go_heap_alloc_bytes` | gauge | Heap in use. |
| `janus_go_gc_pause_seconds_total` | gauge | Cumulative GC pause. |

The engine gauges are backed by a few indexed `COUNT`s evaluated at scrape time,
guarded by a short bounded timeout and a small in-memory cache so a fast scrape
cadence can't hammer Postgres. The audit-chain *verification* (which is O(events))
is deliberately **not** scraped — it stays on the on-demand `/v1/audit/verify`.

### Useful queries

```promql
# request rate by route
sum by (route) (rate(janus_http_requests_total[5m]))

# p95 latency
histogram_quantile(0.95, sum by (le,route) (rate(janus_http_request_duration_seconds_bucket[5m])))

# alert signals
janus_sealed == 1
janus_rotation_runs_failed > 0 or janus_sync_runs_failed > 0
time() - janus_scheduler_last_tick_seconds{engine="rotation"} > 180   # scheduler stalled
```

Careful with metric types when writing your own: `janus_rotation_runs_failed`,
`janus_sync_runs_failed` and `janus_audit_head_seq` are **gauges** (scrape-time
`COUNT`s), so `rate()`/`increase()` do not apply — compare against an `offset`
window instead. Only `janus_http_requests_total` and the histogram's `_bucket`
series are true counters.

### Dashboards and alerts

You don't have to build the board yourself. [`deploy/grafana/`](../../deploy/grafana/)
contains:

| File | What it is |
| --- | --- |
| [`janus-overview.json`](../../deploy/grafana/janus-overview.json) | Grafana dashboard in import format — seal state, HTTP rate/errors/latency, scheduler tick age per engine, rotation/sync failures, dynamic leases, DB pool, audit chain head, Go runtime. Uses a `DS_PROMETHEUS` datasource input and `$job`/`$instance` template variables, so nothing is hardcoded to your hostnames. |
| [`alerts.yaml`](../../deploy/grafana/alerts.yaml) | Example Prometheus alerting rules (13 of them) with justified thresholds, `for:` durations, and an annotation on each saying what to actually do. Loads in Prometheus, the Mimir/Cortex ruler, or Grafana 11+ Alerting. |
| [`README.md`](../../deploy/grafana/README.md) | Import instructions (UI + provisioning), a working `scrape_configs` snippet with bearer auth, a `ServiceMonitor` for the Prometheus Operator, and the metric-type gotchas. |

Import the dashboard with *Dashboards → New → Import → Upload JSON file* and
select your Prometheus datasource; add `alerts.yaml` to your `rule_files:`. The
alert rules assume the scrape job is named `janus` (only the `up`/`absent` rules
depend on that — adjust the selector if yours differs).

## Health panel

**Settings → Health** shows a live, admin-only snapshot from `GET /v1/sys/status`
(gated by the same instance-level read authority as the audit/metrics reads):

- **Instance** — version + commit, uptime, seal state.
- **Database** — reachability, ping latency, and the connection pool
  (`acquired/idle/total`, plus max). A dead database shows a reachable=false
  warning rather than erroring the panel.
- **Audit** — chain head sequence and total event count.
- **Schedulers** — per engine (rotation/sync/dynamic): enabled state, age of the
  last tick, and the configured interval; a scheduler whose last tick is older
  than ~3× its interval is flagged as **stale**.
- **Failures / leases** — failed rotation/sync run counts (flagged when
  non-zero) and the active dynamic-lease count.
- **Outbound policy** — whether private ranges are blocked and which CIDRs are
  exempt (`JANUS_OUTBOUND_ALLOW`), plus a warning if proxying is enabled or an
  allowlist is set with nothing to exempt from. This is process configuration
  with no database row, so the panel is the only place it is visible in the app
  — see [Outbound egress & the SSRF guard](egress-and-ssrf.md). It reports
  configuration, never credentials or targets.

`GET /v1/sys/status` additionally returns a `backup` block (scheduled-S3-backup
state and last run) and, when a destination is configured, an `audit_ship`
block. The panel does not render these yet — read them from the endpoint, or
watch the equivalent metrics.

It is read-only — a fast "is anything on fire?" view. The same numbers are in
`/metrics` for trending and alerting; the panel is the at-a-glance companion.

## Logs

Janus logs via the standard library `slog`. Two environment variables select the
handler at boot:

| Env | Values | Default |
| --- | --- | --- |
| `JANUS_LOG_LEVEL` | `debug`, `info`, `warn`, `error` | `info` |
| `JANUS_LOG_FORMAT` | `text`, `json` | `text` |

Use `JANUS_LOG_FORMAT=json` when shipping logs to a structured store (Loki,
CloudWatch, ELK); use `debug` temporarily when diagnosing. Invalid values warn
once and fall back to the defaults. Request logs record method, route, status,
and duration — never a request body, a secret value, a token, or the metrics
token. See the full variable reference in
[production-deployment.md](./production-deployment.md).
