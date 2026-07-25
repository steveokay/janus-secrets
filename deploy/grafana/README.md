# Grafana dashboard + alert rules for Janus

Ready-made monitoring for a Janus instance. `/metrics` already exists — this is
the board and the paging rules so you don't have to build them yourself.

```
deploy/grafana/
  janus-overview.json   Grafana dashboard (import format, DS_PROMETHEUS input)
  alerts.yaml           Example Prometheus alerting rules
```

Everything here is built from metrics the server actually exports; the metric
reference itself lives in
[docs/guides/observability.md](../../docs/guides/observability.md).

---

## 1. Turn on `/metrics`

`/metrics` is **disabled by default and returns `404`** until a scrape token is
configured. Set `JANUS_METRICS_TOKEN` to a high-entropy value and restart:

```sh
# generate once, store it wherever your other service credentials live
JANUS_METRICS_TOKEN=$(openssl rand -hex 32)
```

Once set, the endpoint requires exactly that value as a bearer credential
(constant-time compared). A missing or wrong token returns `401`. There is no
unauthenticated mode — treat the token as a secret, and restrict network access
to the port as well.

| Deployment | How to set it |
| --- | --- |
| Compose / plain Docker | `JANUS_METRICS_TOKEN` in the service environment (from an env-file or Docker secret — **never** committed to `docker-compose.yml`). |
| Helm (`deploy/helm/janus`) | `--set metrics.token=…`, or better `--set metrics.existingSecret=janus-metrics` referencing a Secret you created out-of-band. |
| systemd | `Environment=` / `EnvironmentFile=` in the unit. |

Janus has **no `_FILE` env convention** — the variable itself must hold the
token.

Verify by hand before pointing Prometheus at it:

```sh
curl -H "Authorization: Bearer $JANUS_METRICS_TOKEN" http://127.0.0.1:8200/metrics
```

`404` means the token is unset server-side; `401` means yours doesn't match.

---

## 2. Scrape it with Prometheus

Add a job to `prometheus.yml`. Keep the token out of the config file by using
`authorization.credentials_file`:

```yaml
scrape_configs:
  - job_name: janus                 # the alert rules reference job="janus"
    metrics_path: /metrics
    scheme: https                   # http if TLS terminates elsewhere / same host
    scrape_interval: 30s
    authorization:
      type: Bearer
      # Preferred: a file mode-0600 and owned by the Prometheus user.
      credentials_file: /etc/prometheus/janus-metrics-token
      # Or inline, if your config is itself managed as a secret:
      # credentials: <JANUS_METRICS_TOKEN>
    static_configs:
      - targets: ['janus.internal:8200']
        labels:
          env: prod
```

On Kubernetes with the Prometheus Operator, the same thing as a `ServiceMonitor`:

```yaml
apiVersion: monitoring.coreos.com/v1
kind: ServiceMonitor
metadata:
  name: janus
  namespace: janus
spec:
  selector:
    matchLabels: { app.kubernetes.io/name: janus }
  endpoints:
    - port: http                    # the Service port name from the Helm chart
      path: /metrics
      interval: 30s
      authorization:
        type: Bearer
        credentials:
          name: janus-metrics       # Secret in the ServiceMonitor's namespace
          key: JANUS_METRICS_TOKEN
```

`scrape_interval: 30s` is a good default: the DB-derived gauges (audit head,
failed runs, active leases) are recomputed at most every 5s behind a small
in-memory cache, so scraping faster buys nothing and just adds load.

Janus is **single-node by design** — expect exactly one instance per job.

---

## 3. Import the dashboard

**Via the UI** — *Dashboards → New → Import → Upload JSON file*, pick
`janus-overview.json`, then choose your Prometheus datasource for the
`DS_PROMETHEUS` input. The dashboard UID is `janus-overview`.

**Via provisioning** — drop the JSON on disk and point a provider at it:

```yaml
# /etc/grafana/provisioning/dashboards/janus.yaml
apiVersion: 1
providers:
  - name: janus
    orgId: 1
    folder: Janus
    type: file
    disableDeletion: false
    updateIntervalSeconds: 60
    allowUiUpdates: false
    options:
      path: /var/lib/grafana/dashboards/janus
      foldersFromFilesStructure: false
```

Provisioned dashboards are loaded verbatim and do **not** run the import wizard,
so `${DS_PROMETHEUS}` has to resolve on its own. Either name your provisioned
datasource's UID `DS_PROMETHEUS`:

```yaml
# /etc/grafana/provisioning/datasources/prometheus.yaml
apiVersion: 1
datasources:
  - name: Prometheus
    uid: DS_PROMETHEUS
    type: prometheus
    access: proxy
    url: http://prometheus:9090
    isDefault: true
```

…or substitute your own UID into the file when you copy it:

```sh
sed 's/${DS_PROMETHEUS}/my-prom-uid/g' janus-overview.json \
  > /var/lib/grafana/dashboards/janus/janus-overview.json
```

The dashboard has two template variables, `$job` and `$instance`, both populated
from `janus_build_info` — nothing is hardcoded to a hostname.

### What's on it

| Row | Panels |
| --- | --- |
| **Availability** | Seal state, scrape up, uptime, build version/commit |
| **HTTP** | Request rate by status, latency p50/p95/p99, rate by route, 5xx ratio, 401/403 denials, p95 by route |
| **Schedulers & engines** | Scheduler tick age per engine, failed rotation/sync runs, active dynamic leases |
| **Storage & audit** | DB pool by state, pool saturation, audit chain head + slope |
| **Go runtime** | Goroutines, heap in use, GC pause rate |

Every panel carries a description explaining the metric and how to read it —
hover the ⓘ in the panel header.

Two things worth knowing when reading the board:

- **Seal state is the headline.** A sealed server answers `503` to every secret
  operation, so it also inflates the 5xx panel. Always read those two together.
- **`route` is the chi route *pattern*** (`/v1/projects/{pid}/configs`), never a
  concrete path, so no project ids, config names, or secret keys ever reach
  Prometheus. Unmatched paths collapse to `route="unmatched"`.

---

## 4. Load the alert rules

`alerts.yaml` is a plain **Prometheus rule file** (`groups → rules` with
`alert`/`expr`/`for`/`labels`/`annotations`). That format loads unchanged in:

- **Prometheus** 2.x / 3.x — `rule_files:` in `prometheus.yml`
- **Grafana Mimir / Cortex** ruler — `mimirtool rules load alerts.yaml`
- **Grafana 11+ Alerting** — *Alert rules → Import → Prometheus-style rules*

It is deliberately not Grafana's own provisioning schema (`apiVersion: 1` +
`grafana_alert`), which only works in Grafana and is far harder to read.

```yaml
# prometheus.yml
rule_files:
  - /etc/prometheus/rules/janus-alerts.yaml
```

Validate before reloading:

```sh
promtool check rules alerts.yaml
```

### The rules

| Alert | Severity | Fires when | `for` |
| --- | --- | --- | --- |
| `JanusSealed` | critical | `janus_sealed == 1` | 5m |
| `JanusDown` | critical | `up == 0` | 2m |
| `JanusTargetAbsent` | critical | `absent(janus_build_info)` | 10m |
| `JanusRestartLoop` | warning | >2 restarts in 15m | 0m |
| `JanusHighErrorRate` | warning | 5xx ratio > 5% | 10m |
| `JanusLatencyHigh` | warning | p95 > 1s | 15m |
| `JanusSchedulerStalled` | warning | tick age > 300s | 5m |
| `JanusRotationFailuresIncreasing` | warning | failed rotation runs grew in 1h | 15m |
| `JanusSyncFailuresIncreasing` | warning | failed sync runs grew in 1h | 15m |
| `JanusDynamicLeasesUnreapable` | critical | live leases + stalled `dynamic` engine | 5m |
| `JanusDBPoolExhausted` | warning | acquired/max > 90% | 10m |
| `JanusAuditHeadNotAdvancing` | warning | chain head flat under real traffic | 15m |
| `JanusGoroutineLeak` | warning | goroutines > 2000 | 30m |

Each rule's rationale is a comment directly above it, and each annotation says
what an operator should actually *do*. Tune the thresholds — they are defensible
defaults for a small single-node deployment, not a policy.

Two rules — `JanusDown` and `JanusTargetAbsent` — hardcode `job="janus"`,
because there is no `janus_*` series to select when Janus is gone. Change that
selector if your scrape job has a different name.

---

## 5. Gotchas that will otherwise bite you

- **Several "counter-looking" series are gauges.**
  `janus_rotation_runs_failed`, `janus_sync_runs_failed` and
  `janus_audit_head_seq` are scrape-time `COUNT`s exported as gauges, so
  `rate()` / `increase()` are wrong on them. The rules compare against an
  `offset` window instead; the dashboard uses `deriv()` for the audit slope.
  `janus_go_gc_pause_seconds_total` is also typed gauge despite the `_total`
  suffix, but it *is* cumulative and monotonic within a process, so `rate()` is
  both safe and preferable there (it handles the reset on restart).
- **A scheduler series only exists after that engine's first tick.** Disabled
  engines (`sync_verify` and `backup` are off unless you set
  `JANUS_SYNC_VERIFY_TICK` / `JANUS_BACKUP_TICK`) are simply absent, not stale —
  `JanusSchedulerStalled` cannot fire for them.
- **Sealed servers don't run schedulers.** Expect tick-age and seal alerts
  together after an un-unsealed restart; fix the seal first.
- **`janus_http_requests_total{status}` is the full numeric code**, not a class.
  Use `status=~"5.."` for classes.
- **The audit chain's cryptographic verification is not scraped.** It is
  O(events) and stays on the on-demand `GET /v1/audit/verify`; the metric only
  exposes the head sequence number.
- **No secret values, ids, or paths are exposed here.** Metrics are counts,
  gauges, and bounded labels only.
