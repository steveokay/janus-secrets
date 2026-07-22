package metrics

import (
	"bytes"
	"strings"
	"sync"
	"testing"
)

func render(t *testing.T, r *Registry) string {
	t.Helper()
	var b bytes.Buffer
	if _, err := r.WriteTo(&b); err != nil {
		t.Fatalf("WriteTo: %v", err)
	}
	return b.String()
}

func TestCounterMath(t *testing.T) {
	r := NewRegistry()
	c := r.NewCounter("janus_test_total", "help", "method")
	c.Inc("GET")
	c.Inc("GET")
	c.Add(3, "POST")
	out := render(t, r)
	wantLines := []string{
		`# HELP janus_test_total help`,
		`# TYPE janus_test_total counter`,
		`janus_test_total{method="GET"} 2`,
		`janus_test_total{method="POST"} 3`,
	}
	for _, w := range wantLines {
		if !strings.Contains(out, w) {
			t.Errorf("missing line %q in:\n%s", w, out)
		}
	}
}

func TestGaugeSetAndAdd(t *testing.T) {
	r := NewRegistry()
	g := r.NewGauge("janus_test_gauge", "g")
	g.Set(5)
	g.Add(-2)
	out := render(t, r)
	if !strings.Contains(out, "janus_test_gauge 3") {
		t.Errorf("gauge value wrong:\n%s", out)
	}
	if !strings.Contains(out, "# TYPE janus_test_gauge gauge") {
		t.Errorf("gauge type header missing:\n%s", out)
	}
}

func TestGaugeFloatFormatting(t *testing.T) {
	r := NewRegistry()
	g := r.NewGauge("janus_frac", "f")
	g.Set(2.5)
	out := render(t, r)
	if !strings.Contains(out, "janus_frac 2.5") {
		t.Errorf("fractional gauge wrong:\n%s", out)
	}
}

// TestHistogramBucketBoundaries verifies cumulative le-bucket counts,
// including exact-boundary observations landing in their own bucket (le is
// "less than or equal").
func TestHistogramBucketBoundaries(t *testing.T) {
	r := NewRegistry()
	h := r.NewHistogram("janus_dur_seconds", "d", []float64{0.1, 0.5, 1}, "route")
	// Observations: 0.05 (<=0.1), 0.1 (==0.1, <=0.1), 0.3 (<=0.5), 2 (only +Inf).
	for _, v := range []float64{0.05, 0.1, 0.3, 2} {
		h.Observe(v, "/x")
	}
	out := render(t, r)
	want := []string{
		`janus_dur_seconds_bucket{route="/x",le="0.1"} 2`,
		`janus_dur_seconds_bucket{route="/x",le="0.5"} 3`,
		`janus_dur_seconds_bucket{route="/x",le="1"} 3`,
		`janus_dur_seconds_bucket{route="/x",le="+Inf"} 4`,
		`janus_dur_seconds_sum{route="/x"} 2.45`,
		`janus_dur_seconds_count{route="/x"} 4`,
	}
	for _, w := range want {
		if !strings.Contains(out, w) {
			t.Errorf("missing %q in:\n%s", w, out)
		}
	}
}

func TestLabelEscaping(t *testing.T) {
	r := NewRegistry()
	g := r.NewGauge("janus_esc", "h", "path")
	g.Set(1, "a\"b\\c\nd")
	out := render(t, r)
	if !strings.Contains(out, `janus_esc{path="a\"b\\c\nd"} 1`) {
		t.Errorf("escaping wrong:\n%s", out)
	}
}

// TestGoldenExposition asserts the full stable text output of a small registry.
func TestGoldenExposition(t *testing.T) {
	r := NewRegistry()
	bi := r.NewGauge("janus_build_info", "Build info.", "version")
	bi.Set(1, "0.1.0")
	c := r.NewCounter("janus_http_requests_total", "HTTP requests.", "method", "status")
	c.Add(4, "GET", "200")
	want := `# HELP janus_build_info Build info.
# TYPE janus_build_info gauge
janus_build_info{version="0.1.0"} 1
# HELP janus_http_requests_total HTTP requests.
# TYPE janus_http_requests_total counter
janus_http_requests_total{method="GET",status="200"} 4
`
	if got := render(t, r); got != want {
		t.Errorf("golden mismatch:\ngot:\n%s\nwant:\n%s", got, want)
	}
}

func TestSeriesOrderStable(t *testing.T) {
	r := NewRegistry()
	c := r.NewCounter("janus_x_total", "x", "k")
	c.Inc("z")
	c.Inc("a")
	c.Inc("m")
	out := render(t, r)
	ia := strings.Index(out, `k="a"`)
	im := strings.Index(out, `k="m"`)
	iz := strings.Index(out, `k="z"`)
	if !(ia < im && im < iz) {
		t.Errorf("series not sorted by label value:\n%s", out)
	}
}

func TestNameValidationPanics(t *testing.T) {
	cases := []struct {
		name    string
		fn      func(*Registry)
		wantErr bool
	}{
		{"good name", func(r *Registry) { r.NewGauge("janus_ok", "h") }, false},
		{"bad metric name", func(r *Registry) { r.NewGauge("9bad", "h") }, true},
		{"empty metric name", func(r *Registry) { r.NewGauge("", "h") }, true},
		{"bad label", func(r *Registry) { r.NewGauge("janus_ok", "h", "bad-label") }, true},
		{"reserved le label", func(r *Registry) { r.NewGauge("janus_ok", "h", "le") }, true},
		{"dup label", func(r *Registry) { r.NewGauge("janus_ok", "h", "a", "a") }, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			defer func() {
				rec := recover()
				if tc.wantErr && rec == nil {
					t.Errorf("expected panic, got none")
				}
				if !tc.wantErr && rec != nil {
					t.Errorf("unexpected panic: %v", rec)
				}
			}()
			tc.fn(NewRegistry())
		})
	}
}

func TestDuplicateMetricPanics(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Error("expected panic on duplicate metric name")
		}
	}()
	r := NewRegistry()
	r.NewGauge("janus_dup", "h")
	r.NewCounter("janus_dup", "h")
}

func TestWrongLabelArityPanics(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Error("expected panic on wrong label arity")
		}
	}()
	r := NewRegistry()
	c := r.NewCounter("janus_arity_total", "h", "a", "b")
	c.Inc("only-one")
}

func TestCollectorRunsAtScrape(t *testing.T) {
	r := NewRegistry()
	g := r.NewGauge("janus_live", "l")
	calls := 0
	r.AddCollector(func() {
		calls++
		g.Set(float64(calls * 10))
	})
	out1 := render(t, r)
	out2 := render(t, r)
	if !strings.Contains(out1, "janus_live 10") {
		t.Errorf("first scrape wrong:\n%s", out1)
	}
	if !strings.Contains(out2, "janus_live 20") {
		t.Errorf("second scrape wrong:\n%s", out2)
	}
}

// TestConcurrentUpdateAndScrape drives -race: many goroutines update while
// others scrape.
func TestConcurrentUpdateAndScrape(t *testing.T) {
	r := NewRegistry()
	c := r.NewCounter("janus_conc_total", "c", "k")
	h := r.NewHistogram("janus_conc_seconds", "h", DefaultBuckets, "k")
	g := r.NewGauge("janus_conc_gauge", "g")
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			for j := 0; j < 500; j++ {
				c.Inc("a")
				h.Observe(0.02, "a")
				g.Set(float64(j))
			}
		}(i)
	}
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			var b bytes.Buffer
			for j := 0; j < 200; j++ {
				b.Reset()
				_, _ = r.WriteTo(&b)
			}
		}()
	}
	wg.Wait()
	out := render(t, r)
	if !strings.Contains(out, `janus_conc_total{k="a"} 4000`) {
		t.Errorf("counter total wrong after concurrent updates:\n%s", out)
	}
}

// failWriter errors after n successful bytes, to exercise WriteTo's error path.
type failWriter struct{ remaining int }

func (f *failWriter) Write(p []byte) (int, error) {
	if f.remaining <= 0 {
		return 0, errWrite
	}
	if len(p) > f.remaining {
		n := f.remaining
		f.remaining = 0
		return n, errWrite
	}
	f.remaining -= len(p)
	return len(p), nil
}

var errWrite = &writeErr{}

type writeErr struct{}

func (*writeErr) Error() string { return "write failed" }

func TestWriteToPropagatesError(t *testing.T) {
	r := NewRegistry()
	r.NewGauge("janus_a", "h").Set(1)
	r.NewGauge("janus_b", "h").Set(2)
	fw := &failWriter{remaining: 5}
	if _, err := r.WriteTo(fw); err == nil {
		t.Error("expected WriteTo to propagate writer error")
	}
}
