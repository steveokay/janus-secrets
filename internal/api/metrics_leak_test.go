package api

import (
	"context"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/steveokay/janus-secrets/internal/crypto"
	"github.com/steveokay/janus-secrets/internal/secrets"
)

// TestMetricsAndStatusLeak proves that neither GET /metrics nor GET
// /v1/sys/status exposes a secret value, a raw service token, or any per-user
// datum. Secret material is pushed through the stack (a written secret value +
// a minted token whose id-bearing path is scraped), then both surfaces are
// scanned. Route labels must be chi PATTERNS, so an id in the path never
// reaches the exposition.
func TestMetricsAndStatusLeak(t *testing.T) {
	// A metrics-token-enabled full stack: boot via authStackFull's DSN path is
	// not token-aware, so build our own here with a metrics token.
	ts, srv, email, password, cid := authStackFullMetrics(t, "leak-scrape-token")
	adminCookie := login(t, ts.URL, email, password)

	const sentinel = "SENTINEL-METRICS-LEAK-CANARY-7a2b"

	// Write a secret through the wired service, then reveal it via the stack so a
	// real reveal request (with the config id in the path) flows through the HTTP
	// instrumentation.
	if _, err := srv.service.SetSecrets(t.Context(), cid, []secrets.SecretChange{
		{Key: "CANARY", Value: []byte(sentinel)},
	}, "seed", "test"); err != nil {
		t.Fatal(err)
	}
	if code := doAuthed(t, "GET", ts.URL+"/v1/configs/"+cid+"/secrets/CANARY", adminCookie, "", "", nil); code != 200 {
		t.Fatalf("reveal: %d", code)
	}

	// Mint a token; its raw value is a mint-once secret. Hit an id-bearing path.
	var minted struct{ Token, ID string }
	mintBody := fmt.Sprintf(`{"name":"leaktok","scope":{"kind":"config","id":%q},"access":"read"}`, cid)
	if code := doAuthed(t, "POST", ts.URL+"/v1/tokens", adminCookie, "", mintBody, &minted); code != 200 || minted.Token == "" {
		t.Fatalf("mint: %d", code)
	}
	// Exercise a param-route with the config id in the path.
	_ = doAuthed(t, "GET", ts.URL+"/v1/configs/"+cid+"/secrets", adminCookie, "", "", nil)

	// Scrape /metrics with the configured token.
	metCode, metBody := getWithBearer(t, ts.URL+"/metrics", "leak-scrape-token")
	if metCode != 200 {
		t.Fatalf("scrape /metrics: %d", metCode)
	}
	// Fetch /v1/sys/status.
	var statusRaw map[string]any
	if code := doAuthed(t, "GET", ts.URL+"/v1/sys/status", adminCookie, "", "", &statusRaw); code != 200 {
		t.Fatalf("status: %d", code)
	}
	statusStr := fmt.Sprintf("%v", statusRaw)

	forbidden := []string{sentinel, minted.Token, "leak-scrape-token", "janus_svc_"}
	for _, f := range forbidden {
		if strings.Contains(metBody, f) {
			t.Fatalf("/metrics exposed forbidden material %q:\n%s", f, metBody)
		}
		if strings.Contains(statusStr, f) {
			t.Fatalf("/v1/sys/status exposed forbidden material %q:\n%s", f, statusStr)
		}
	}

	// The config id must NOT appear as a raw metric label (route labels are
	// patterns like /v1/configs/{cid}/secrets, never the concrete id).
	if strings.Contains(metBody, `route="/v1/configs/`+cid) {
		t.Fatalf("raw config id leaked into a route label:\n%s", metBody)
	}
	// Sanity: the pattern form IS present (proves instrumentation ran).
	if !strings.Contains(metBody, `{cid}`) {
		t.Errorf("expected chi pattern {cid} in metric labels:\n%s", metBody)
	}
}

// authStackFullMetrics is authStackFull but with a metrics token configured, so
// GET /metrics is enabled. Returns the httptest server, the *Server, admin
// email + password, and a config id.
func authStackFullMetrics(t *testing.T, metricsToken string) (*httptest.Server, *Server, string, string, string) {
	t.Helper()
	dsn := bootPostgres(t)
	ctx := context.Background()
	srv, st, err := Boot(ctx, BootConfig{
		DatabaseURL: dsn, SealType: crypto.SealTypeShamir, MetricsToken: metricsToken,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(st.Close)
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	var ir struct {
		Shares []string                          `json:"shares"`
		Admin  *struct{ Email, Password string } `json:"admin"`
	}
	if code := doJSON(t, "POST", ts.URL+"/v1/sys/init",
		`{"shares":1,"threshold":1,"admin_email":"root@corp.io"}`, &ir); code != 200 {
		t.Fatalf("init: %d", code)
	}
	if code := doJSON(t, "POST", ts.URL+"/v1/sys/unseal",
		fmt.Sprintf(`{"share":%q}`, ir.Shares[0]), nil); code != 200 {
		t.Fatalf("unseal failed")
	}
	p, err := srv.service.CreateProject(ctx, "metricstack", "MetricStack")
	if err != nil {
		t.Fatal(err)
	}
	e, err := srv.service.CreateEnvironment(ctx, p.ID, "prod", "Prod")
	if err != nil {
		t.Fatal(err)
	}
	c, err := srv.service.CreateConfig(ctx, e.ID, "root", nil)
	if err != nil {
		t.Fatal(err)
	}
	return ts, srv, ir.Admin.Email, ir.Admin.Password, c.ID
}
