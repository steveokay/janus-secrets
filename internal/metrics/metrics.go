// Package metrics is a tiny, dependency-free Prometheus text-exposition layer.
//
// It renders counters, labeled gauges, and a fixed-bucket histogram in the
// Prometheus text format directly — no client_golang. It is deliberately small:
// a Registry holds named metric families plus scrape-time callbacks that read
// live state (DB pool, runtime, seal) only when WriteTo runs. Everything is
// safe for concurrent update and scrape.
package metrics

import (
	"errors"
	"fmt"
	"io"
	"math"
	"sort"
	"strconv"
	"sync"
)

// DefaultBuckets are the request-latency histogram buckets, in seconds. They
// match the spec and the client_golang defaults so operator dashboards behave
// as expected.
var DefaultBuckets = []float64{.005, .01, .025, .05, .1, .25, .5, 1, 2.5, 5, 10}

// metricType is the Prometheus # TYPE line value.
type metricType string

const (
	typeCounter   metricType = "counter"
	typeGauge     metricType = "gauge"
	typeHistogram metricType = "histogram"
)

// labelValues is an ordered list of label values matching a metric's declared
// label names (index-aligned). Used as a map key via its stringified form.
type seriesKey string

func makeSeriesKey(vals []string) seriesKey {
	// Length-prefixed join so {"a","bc"} and {"ab","c"} never collide.
	if len(vals) == 0 {
		return ""
	}
	var b []byte
	for _, v := range vals {
		b = strconv.AppendInt(b, int64(len(v)), 10)
		b = append(b, ':')
		b = append(b, v...)
	}
	return seriesKey(b)
}

// Counter is a monotonically increasing labeled float64.
type Counter struct {
	m *labeledMetric
}

// Inc adds 1 to the series identified by labelValues (index-aligned with the
// metric's label names).
func (c *Counter) Inc(labelValues ...string) { c.Add(1, labelValues...) }

// Add adds delta (>=0 by convention, not enforced) to the series.
func (c *Counter) Add(delta float64, labelValues ...string) {
	c.m.add(delta, labelValues)
}

// Gauge is a labeled float64 that can go up or down.
type Gauge struct {
	m *labeledMetric
}

// Set sets the series value.
func (g *Gauge) Set(v float64, labelValues ...string) { g.m.set(v, labelValues) }

// Add adds delta to the series value.
func (g *Gauge) Add(delta float64, labelValues ...string) { g.m.add(delta, labelValues) }

// labeledMetric backs Counter and Gauge: a set of series keyed by label values.
type labeledMetric struct {
	name   string
	help   string
	typ    metricType
	labels []string

	mu     sync.Mutex
	series map[seriesKey]*seriesSample
}

type seriesSample struct {
	labelValues []string
	value       float64
}

func (m *labeledMetric) lookup(vals []string) *seriesSample {
	key := makeSeriesKey(vals)
	s := m.series[key]
	if s == nil {
		lv := make([]string, len(vals))
		copy(lv, vals)
		s = &seriesSample{labelValues: lv}
		m.series[key] = s
	}
	return s
}

func (m *labeledMetric) add(delta float64, vals []string) {
	if len(vals) != len(m.labels) {
		panic(fmt.Sprintf("metrics: %s expects %d label values, got %d", m.name, len(m.labels), len(vals)))
	}
	m.mu.Lock()
	m.lookup(vals).value += delta
	m.mu.Unlock()
}

func (m *labeledMetric) set(v float64, vals []string) {
	if len(vals) != len(m.labels) {
		panic(fmt.Sprintf("metrics: %s expects %d label values, got %d", m.name, len(m.labels), len(vals)))
	}
	m.mu.Lock()
	m.lookup(vals).value = v
	m.mu.Unlock()
}

// Histogram is a labeled fixed-bucket histogram with _sum and _count.
type Histogram struct {
	name    string
	help    string
	labels  []string
	buckets []float64

	mu     sync.Mutex
	series map[seriesKey]*histSample
}

type histSample struct {
	labelValues []string
	counts      []uint64 // one per bucket (cumulative computed at render)
	sum         float64
	count       uint64
}

// Observe records v (seconds) into the series identified by labelValues.
func (h *Histogram) Observe(v float64, labelValues ...string) {
	if len(labelValues) != len(h.labels) {
		panic(fmt.Sprintf("metrics: %s expects %d label values, got %d", h.name, len(h.labels), len(labelValues)))
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	key := makeSeriesKey(labelValues)
	s := h.series[key]
	if s == nil {
		lv := make([]string, len(labelValues))
		copy(lv, labelValues)
		s = &histSample{labelValues: lv, counts: make([]uint64, len(h.buckets))}
		h.series[key] = s
	}
	// Bucket counts stored non-cumulatively; cumulative le-counts computed at
	// render time.
	idx := sort.SearchFloat64s(h.buckets, v)
	if idx < len(h.buckets) {
		s.counts[idx]++
	}
	s.sum += v
	s.count++
}

// Collector is a scrape-time callback: it reads live state and updates gauges
// (or sets values) on the registry immediately before rendering.
type Collector func()

// Registry owns metric families and scrape-time collectors.
type Registry struct {
	mu         sync.Mutex
	order      []string // family names, in registration order (stable output)
	counters   map[string]*labeledMetric
	gauges     map[string]*labeledMetric
	histograms map[string]*Histogram
	collectors []Collector
}

// NewRegistry returns an empty registry.
func NewRegistry() *Registry {
	return &Registry{
		counters:   map[string]*labeledMetric{},
		gauges:     map[string]*labeledMetric{},
		histograms: map[string]*Histogram{},
	}
}

// ErrDuplicate is returned when a metric name is registered twice.
var ErrDuplicate = errors.New("metrics: duplicate metric name")

func (r *Registry) claim(name string) {
	if _, ok := r.counters[name]; ok {
		panic(ErrDuplicate)
	}
	if _, ok := r.gauges[name]; ok {
		panic(ErrDuplicate)
	}
	if _, ok := r.histograms[name]; ok {
		panic(ErrDuplicate)
	}
	r.order = append(r.order, name)
}

// NewCounter registers and returns a counter. Panics on an invalid metric or
// label name, or a duplicate name (construction-time programmer error).
func (r *Registry) NewCounter(name, help string, labels ...string) *Counter {
	mustValidName(name)
	mustValidLabels(labels)
	r.mu.Lock()
	defer r.mu.Unlock()
	r.claim(name)
	m := &labeledMetric{name: name, help: help, typ: typeCounter, labels: labels, series: map[seriesKey]*seriesSample{}}
	r.counters[name] = m
	return &Counter{m: m}
}

// NewGauge registers and returns a labeled gauge.
func (r *Registry) NewGauge(name, help string, labels ...string) *Gauge {
	mustValidName(name)
	mustValidLabels(labels)
	r.mu.Lock()
	defer r.mu.Unlock()
	r.claim(name)
	m := &labeledMetric{name: name, help: help, typ: typeGauge, labels: labels, series: map[seriesKey]*seriesSample{}}
	r.gauges[name] = m
	return &Gauge{m: m}
}

// NewHistogram registers and returns a histogram with the given buckets (sorted
// ascending; DefaultBuckets if empty).
func (r *Registry) NewHistogram(name, help string, buckets []float64, labels ...string) *Histogram {
	mustValidName(name)
	mustValidLabels(labels)
	if len(buckets) == 0 {
		buckets = DefaultBuckets
	}
	bs := make([]float64, len(buckets))
	copy(bs, buckets)
	sort.Float64s(bs)
	r.mu.Lock()
	defer r.mu.Unlock()
	r.claim(name)
	h := &Histogram{name: name, help: help, labels: labels, buckets: bs, series: map[seriesKey]*histSample{}}
	r.histograms[name] = h
	return h
}

// AddCollector registers a scrape-time callback, invoked by WriteTo before
// rendering (in registration order).
func (r *Registry) AddCollector(c Collector) {
	r.mu.Lock()
	r.collectors = append(r.collectors, c)
	r.mu.Unlock()
}

// WriteTo renders the registry in Prometheus text-exposition format. Collectors
// run first (to refresh live gauges), then families render in registration
// order with # HELP / # TYPE headers.
func (r *Registry) WriteTo(w io.Writer) (int64, error) {
	// Snapshot collectors under lock, then run them without holding it (a
	// collector may take the same lock to Set a gauge).
	r.mu.Lock()
	cols := make([]Collector, len(r.collectors))
	copy(cols, r.collectors)
	r.mu.Unlock()
	for _, c := range cols {
		c()
	}

	cw := &countingWriter{w: w}
	r.mu.Lock()
	order := make([]string, len(r.order))
	copy(order, r.order)
	r.mu.Unlock()

	for _, name := range order {
		r.mu.Lock()
		cm := r.counters[name]
		gm := r.gauges[name]
		hm := r.histograms[name]
		r.mu.Unlock()
		switch {
		case cm != nil:
			renderLabeled(cw, cm)
		case gm != nil:
			renderLabeled(cw, gm)
		case hm != nil:
			renderHistogram(cw, hm)
		}
		if cw.err != nil {
			return cw.n, cw.err
		}
	}
	return cw.n, cw.err
}

func renderLabeled(w *countingWriter, m *labeledMetric) {
	fmt.Fprintf(w, "# HELP %s %s\n", m.name, escapeHelp(m.help))
	fmt.Fprintf(w, "# TYPE %s %s\n", m.name, m.typ)
	// Snapshot values (not pointers) under lock so concurrent updates don't race
	// the render read.
	m.mu.Lock()
	samples := make([]seriesSample, 0, len(m.series))
	for _, s := range m.series {
		samples = append(samples, seriesSample{labelValues: s.labelValues, value: s.value})
	}
	m.mu.Unlock()
	sort.SliceStable(samples, func(i, j int) bool { return lessStrings(samples[i].labelValues, samples[j].labelValues) })
	for i := range samples {
		fmt.Fprintf(w, "%s%s %s\n", m.name, renderLabels(m.labels, samples[i].labelValues), formatFloat(samples[i].value))
	}
}

func renderHistogram(w *countingWriter, h *Histogram) {
	fmt.Fprintf(w, "# HELP %s %s\n", h.name, escapeHelp(h.help))
	fmt.Fprintf(w, "# TYPE %s %s\n", h.name, typeHistogram)
	h.mu.Lock()
	samples := make([]histSample, 0, len(h.series))
	for _, s := range h.series {
		countsCopy := make([]uint64, len(s.counts))
		copy(countsCopy, s.counts)
		samples = append(samples, histSample{labelValues: s.labelValues, counts: countsCopy, sum: s.sum, count: s.count})
	}
	h.mu.Unlock()
	sort.SliceStable(samples, func(i, j int) bool { return lessStrings(samples[i].labelValues, samples[j].labelValues) })
	for _, s := range samples {
		var cumulative uint64
		for i, ub := range h.buckets {
			cumulative += s.counts[i]
			lbls := appendLabel(h.labels, s.labelValues, "le", formatFloat(ub))
			fmt.Fprintf(w, "%s_bucket%s %s\n", h.name, lbls, formatUint(cumulative))
		}
		infLbls := appendLabel(h.labels, s.labelValues, "le", "+Inf")
		fmt.Fprintf(w, "%s_bucket%s %s\n", h.name, infLbls, formatUint(s.count))
		fmt.Fprintf(w, "%s_sum%s %s\n", h.name, renderLabels(h.labels, s.labelValues), formatFloat(s.sum))
		fmt.Fprintf(w, "%s_count%s %s\n", h.name, renderLabels(h.labels, s.labelValues), formatUint(s.count))
	}
}

// formatFloat renders a float in Prometheus style (integers without a decimal,
// +Inf/-Inf/NaN spelled out).
func formatFloat(v float64) string {
	switch {
	case math.IsInf(v, 1):
		return "+Inf"
	case math.IsInf(v, -1):
		return "-Inf"
	case math.IsNaN(v):
		return "NaN"
	}
	if v == math.Trunc(v) && math.Abs(v) < 1e15 {
		return strconv.FormatInt(int64(v), 10)
	}
	return strconv.FormatFloat(v, 'g', -1, 64)
}

func formatUint(v uint64) string { return strconv.FormatUint(v, 10) }

// renderLabels renders `{name="val",...}` with Prometheus escaping, or "" when
// there are no labels.
func renderLabels(names, values []string) string {
	if len(names) == 0 {
		return ""
	}
	var b []byte
	b = append(b, '{')
	for i, n := range names {
		if i > 0 {
			b = append(b, ',')
		}
		b = append(b, n...)
		b = append(b, '=', '"')
		b = appendEscaped(b, values[i])
		b = append(b, '"')
	}
	b = append(b, '}')
	return string(b)
}

// appendLabel renders the base labels plus one extra (name=extraVal) — used for
// the histogram `le` label.
func appendLabel(names, values []string, extraName, extraVal string) string {
	allN := make([]string, 0, len(names)+1)
	allV := make([]string, 0, len(values)+1)
	allN = append(allN, names...)
	allV = append(allV, values...)
	allN = append(allN, extraName)
	allV = append(allV, extraVal)
	return renderLabels(allN, allV)
}

// appendEscaped escapes a label value per the text format: backslash, double
// quote, and newline.
func appendEscaped(b []byte, s string) []byte {
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '\\':
			b = append(b, '\\', '\\')
		case '"':
			b = append(b, '\\', '"')
		case '\n':
			b = append(b, '\\', 'n')
		default:
			b = append(b, s[i])
		}
	}
	return b
}

// escapeHelp escapes a HELP string (backslash and newline only, per spec).
func escapeHelp(s string) string {
	var b []byte
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '\\':
			b = append(b, '\\', '\\')
		case '\n':
			b = append(b, '\\', 'n')
		default:
			b = append(b, s[i])
		}
	}
	return string(b)
}

func lessStrings(a, b []string) bool {
	for i := 0; i < len(a) && i < len(b); i++ {
		if a[i] != b[i] {
			return a[i] < b[i]
		}
	}
	return len(a) < len(b)
}

// countingWriter tracks bytes written and the first error.
type countingWriter struct {
	w   io.Writer
	n   int64
	err error
}

func (c *countingWriter) Write(p []byte) (int, error) {
	if c.err != nil {
		return 0, c.err
	}
	n, err := c.w.Write(p)
	c.n += int64(n)
	c.err = err
	return n, err
}
