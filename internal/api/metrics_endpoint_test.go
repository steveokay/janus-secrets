package api

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/steveokay/janus-secrets/internal/crypto"
)

// newMetricsTestServer builds a lightweight Shamir unit server with an optional
// metrics token, returning the *Server and its httptest server.
func newMetricsTestServer(t *testing.T, token string) (*Server, *httptest.Server) {
	t.Helper()
	seals := &memSealStore{}
	kr := crypto.NewKeyring()
	u := crypto.NewShamirUnsealer(seals, 0, 0)
	srv := New(Config{SealType: crypto.SealTypeShamir, MetricsToken: token}, kr, u, seals,
		nil, nil, nil, nil, nil, nil, nil, nil, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return srv, ts
}

func getWithBearer(t *testing.T, url, bearer string) (int, string) {
	t.Helper()
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		t.Fatal(err)
	}
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

// TestMetricsNoTokenIs404 asserts /metrics returns 404 when no token configured.
func TestMetricsNoTokenIs404(t *testing.T) {
	_, ts := newMetricsTestServer(t, "")
	code, _ := getWithBearer(t, ts.URL+"/metrics", "")
	if code != http.StatusNotFound {
		t.Fatalf("no-token /metrics: want 404, got %d", code)
	}
	// Even with a bearer, an unconfigured endpoint stays 404.
	code, _ = getWithBearer(t, ts.URL+"/metrics", "anything")
	if code != http.StatusNotFound {
		t.Fatalf("no-token /metrics with bearer: want 404, got %d", code)
	}
}

// TestMetricsWrongTokenIs401 asserts 401 on a missing/mismatched token.
func TestMetricsWrongTokenIs401(t *testing.T) {
	_, ts := newMetricsTestServer(t, "s3cret")
	if code, _ := getWithBearer(t, ts.URL+"/metrics", ""); code != http.StatusUnauthorized {
		t.Fatalf("missing token: want 401, got %d", code)
	}
	if code, _ := getWithBearer(t, ts.URL+"/metrics", "wrong"); code != http.StatusUnauthorized {
		t.Fatalf("wrong token: want 401, got %d", code)
	}
}

// TestMetricsValidTokenExposesSeries asserts 200 + expected series with the
// right token, and that the request counter increments across scrapes.
func TestMetricsValidTokenExposesSeries(t *testing.T) {
	srv, ts := newMetricsTestServer(t, "s3cret")

	// Drive some traffic so the request counter has samples. /v1/sys/live is
	// unauthenticated and always mounted.
	for i := 0; i < 3; i++ {
		if code, _ := getWithBearer(t, ts.URL+"/v1/sys/live", ""); code != 200 {
			t.Fatalf("live probe: %d", code)
		}
	}

	code, body := getWithBearer(t, ts.URL+"/metrics", "s3cret")
	if code != http.StatusOK {
		t.Fatalf("valid token: want 200, got %d\n%s", code, body)
	}
	wantSeries := []string{
		"# TYPE janus_build_info gauge",
		"janus_build_info{",
		"janus_start_time_seconds ",
		"janus_sealed ",
		"# TYPE janus_http_requests_total counter",
		"# TYPE janus_http_request_duration_seconds histogram",
		"janus_go_goroutines ",
		"janus_go_heap_alloc_bytes ",
	}
	for _, s := range wantSeries {
		if !strings.Contains(body, s) {
			t.Errorf("metrics output missing %q:\n%s", s, body)
		}
	}
	// The live-probe requests must be recorded under the chi route PATTERN, not
	// the raw path (they're identical here, but the pattern form is asserted).
	if !strings.Contains(body, `route="/v1/sys/live"`) {
		t.Errorf("expected route pattern label for /v1/sys/live:\n%s", body)
	}

	// Scrape again after more traffic: the counter must have increased.
	countBefore := countRequestsForRoute(body, "/v1/sys/live")
	for i := 0; i < 2; i++ {
		_, _ = getWithBearer(t, ts.URL+"/v1/sys/live", "")
	}
	_, body2 := getWithBearer(t, ts.URL+"/metrics", "s3cret")
	countAfter := countRequestsForRoute(body2, "/v1/sys/live")
	if countAfter <= countBefore {
		t.Errorf("request counter did not increment: before=%s after=%s", countBefore, countAfter)
	}

	// /metrics itself must be excluded from instrumentation (no self-series).
	if strings.Contains(body2, `route="/metrics"`) {
		t.Errorf("/metrics should be excluded from instrumentation:\n%s", body2)
	}
	_ = srv
}

// countRequestsForRoute extracts the counter value for the given route from the
// exposition text (returns the value string, or "" if not found).
func countRequestsForRoute(body, route string) string {
	for _, line := range strings.Split(body, "\n") {
		if strings.HasPrefix(line, "janus_http_requests_total{") && strings.Contains(line, `route="`+route+`"`) {
			if i := strings.LastIndexByte(line, ' '); i >= 0 {
				return line[i+1:]
			}
		}
	}
	return ""
}

// TestMetricsExcludesRawPathCardinality confirms an id-bearing raw path is
// recorded under its chi pattern (bounded cardinality), never the raw id.
func TestMetricsRouteIsPatternNotRawPath(t *testing.T) {
	_, ts := newMetricsTestServer(t, "tok")
	// /v1/sys/live matches a literal route; to exercise a param pattern we hit a
	// 404 (unmatched) path — it must collapse into "unmatched", not the raw path.
	if code, _ := getWithBearer(t, ts.URL+"/v1/does/not/exist/12345", ""); code == 0 {
		t.Fatal("request failed")
	}
	_, body := getWithBearer(t, ts.URL+"/metrics", "tok")
	if strings.Contains(body, "12345") {
		t.Errorf("raw path id leaked into metrics labels:\n%s", body)
	}
}
