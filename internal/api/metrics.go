package api

import (
	"context"
	"runtime"
	"strconv"
	"sync"
	"time"

	"github.com/steveokay/janus-secrets/internal/crypto"
	"github.com/steveokay/janus-secrets/internal/metrics"
	"github.com/steveokay/janus-secrets/internal/store"
	"github.com/steveokay/janus-secrets/internal/version"
)

// dbGaugeTTL bounds how often the scrape-time DB-derived engine gauges hit
// Postgres. Rapid Prometheus scrapes reuse the last value within this window.
const dbGaugeTTL = 5 * time.Second

// dbGaugeTimeout caps each scrape-time COUNT so a slow DB can't stall a scrape.
const dbGaugeTimeout = 2 * time.Second

// AppMetrics owns the janus_ metric set plus the HTTP instrumentation handles.
// It registers scrape-time collectors (runtime, seal, DB pool + engine gauges)
// on its Registry, so WriteTo yields a live snapshot.
type AppMetrics struct {
	reg *metrics.Registry

	httpReqs *metrics.Counter
	httpDur  *metrics.Histogram

	sealed       *metrics.Gauge
	auditHead    *metrics.Gauge
	lastTick     *metrics.Gauge
	rotationFail *metrics.Gauge
	syncFail     *metrics.Gauge
	leasesActive *metrics.Gauge
	dbPool       *metrics.Gauge
	goGoroutines *metrics.Gauge
	goHeapAlloc  *metrics.Gauge
	goGCPause    *metrics.Gauge

	// dependencies read at scrape time
	keyring *crypto.Keyring
	st      *store.Store
	ticks   *metrics.TickTracker

	// engineGaugeCache memoizes the DB-derived engine gauges for dbGaugeTTL.
	cacheMu   sync.Mutex
	cacheAt   time.Time
	cRotation float64
	cSync     float64
	cLeases   float64
	cAuditSeq float64
}

// NewAppMetrics builds the metric set on its own registry and registers all
// scrape-time collectors. keyring/st may be nil in unit-test servers; the
// collectors degrade gracefully (skip the DB reads, report sealed=1 when the
// keyring is nil).
func NewAppMetrics(keyring *crypto.Keyring, st *store.Store, ticks *metrics.TickTracker, startTime time.Time) *AppMetrics {
	reg := metrics.NewRegistry()
	m := &AppMetrics{reg: reg, keyring: keyring, st: st, ticks: ticks}

	// Static-ish gauges.
	build := reg.NewGauge("janus_build_info", "Build metadata as labels; value is always 1.", "version", "commit")
	build.Set(1, version.Version, version.Commit)
	startGauge := reg.NewGauge("janus_start_time_seconds", "Process start time in unix seconds.")
	startGauge.Set(float64(startTime.Unix()))

	m.httpReqs = reg.NewCounter("janus_http_requests_total", "Total HTTP requests by method, chi route pattern, and status.", "method", "route", "status")
	m.httpDur = reg.NewHistogram("janus_http_request_duration_seconds", "HTTP request duration in seconds by method and chi route pattern.", nil, "method", "route")

	m.sealed = reg.NewGauge("janus_sealed", "1 if the server is sealed, 0 if unsealed.")
	m.auditHead = reg.NewGauge("janus_audit_head_seq", "Sequence number of the audit-log chain head.")
	m.lastTick = reg.NewGauge("janus_scheduler_last_tick_seconds", "Unix time of the last scheduler tick, per engine.", "engine")
	m.rotationFail = reg.NewGauge("janus_rotation_runs_failed", "Count of recorded rotation runs with status=failure.")
	m.syncFail = reg.NewGauge("janus_sync_runs_failed", "Count of recorded sync runs with status=failure.")
	m.leasesActive = reg.NewGauge("janus_dynamic_leases_active", "Count of active dynamic leases.")
	m.dbPool = reg.NewGauge("janus_db_pool_conns", "Postgres pool connection counts by state.", "state")
	m.goGoroutines = reg.NewGauge("janus_go_goroutines", "Number of goroutines.")
	m.goHeapAlloc = reg.NewGauge("janus_go_heap_alloc_bytes", "Bytes of allocated heap objects.")
	m.goGCPause = reg.NewGauge("janus_go_gc_pause_seconds_total", "Cumulative GC stop-the-world pause time in seconds.")

	reg.AddCollector(m.collectRuntime)
	reg.AddCollector(m.collectSeal)
	reg.AddCollector(m.collectTicks)
	reg.AddCollector(m.collectDB)
	return m
}

// Registry exposes the underlying registry (for /metrics rendering).
func (m *AppMetrics) Registry() *metrics.Registry { return m.reg }

// observeHTTP records one request. route is the chi route pattern (bounded
// cardinality); callers must never pass the raw path.
func (m *AppMetrics) observeHTTP(method, route string, status int, dur time.Duration) {
	statusStr := statusClass(status)
	m.httpReqs.Inc(method, route, statusStr)
	m.httpDur.Observe(dur.Seconds(), method, route)
}

func (m *AppMetrics) collectRuntime() {
	m.goGoroutines.Set(float64(runtime.NumGoroutine()))
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	m.goHeapAlloc.Set(float64(ms.HeapAlloc))
	m.goGCPause.Set(float64(ms.PauseTotalNs) / 1e9)
}

func (m *AppMetrics) collectSeal() {
	if m.keyring == nil || m.keyring.Sealed() {
		m.sealed.Set(1)
		return
	}
	m.sealed.Set(0)
}

func (m *AppMetrics) collectTicks() {
	for engine, ts := range m.ticks.Engines() {
		m.lastTick.Set(float64(ts.Unix()), engine)
	}
}

// collectDB refreshes the DB-derived gauges from the ~5s cache, running the
// bounded COUNTs only when the cache is stale. On any DB error the previous
// cached values are retained (a scrape never fails on a transient DB blip).
func (m *AppMetrics) collectDB() {
	if m.st == nil {
		return
	}
	seq, rot, syn, lease := m.engineCounts()
	m.auditHead.Set(seq)
	m.rotationFail.Set(rot)
	m.syncFail.Set(syn)
	m.leasesActive.Set(lease)

	stat := m.st.PoolStat()
	if stat != nil {
		m.dbPool.Set(float64(stat.TotalConns()), "total")
		m.dbPool.Set(float64(stat.IdleConns()), "idle")
		m.dbPool.Set(float64(stat.AcquiredConns()), "acquired")
		m.dbPool.Set(float64(stat.MaxConns()), "max")
	}
}

// engineCounts returns the cached (audit head, rotation failed, sync failed,
// active leases), refreshing from the DB when the cache is older than
// dbGaugeTTL. Each refresh runs under a bounded context.
func (m *AppMetrics) engineCounts() (auditSeq, rotation, sync, leases float64) {
	m.cacheMu.Lock()
	defer m.cacheMu.Unlock()
	if !m.cacheAt.IsZero() && time.Since(m.cacheAt) < dbGaugeTTL {
		return m.cAuditSeq, m.cRotation, m.cSync, m.cLeases
	}
	ctx, cancel := context.WithTimeout(context.Background(), dbGaugeTimeout)
	defer cancel()
	h := store.NewHealthRepo(m.st)
	if v, err := h.AuditHeadSeq(ctx); err == nil {
		m.cAuditSeq = float64(v)
	}
	if v, err := h.RotationRunsFailed(ctx); err == nil {
		m.cRotation = float64(v)
	}
	if v, err := h.SyncRunsFailed(ctx); err == nil {
		m.cSync = float64(v)
	}
	if v, err := h.DynamicLeasesActive(ctx); err == nil {
		m.cLeases = float64(v)
	}
	m.cacheAt = time.Now()
	return m.cAuditSeq, m.cRotation, m.cSync, m.cLeases
}

// statusClass renders a numeric HTTP status as its string form for the label.
// The set of statuses the handlers emit is small and closed, so full-code
// resolution keeps cardinality bounded.
func statusClass(status int) string {
	if status <= 0 {
		return "0"
	}
	return strconv.Itoa(status)
}
