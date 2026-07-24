package api

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/steveokay/janus-secrets/internal/web"
)

// TestSecurityHeadersOnAPI asserts the hardening middleware stamps the fixed
// header set on a representative /v1/* response (and on /metrics), and that it
// leaves non-API/SPA paths for the SPA handler to header itself.
func TestSecurityHeadersOnAPI(t *testing.T) {
	probe := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	h := securityHeaders(probe)

	// A representative /v1/... response carries the hardening headers.
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/v1/projects", nil))
	if got := rec.Header().Get("X-Content-Type-Options"); got != "nosniff" {
		t.Errorf("/v1 X-Content-Type-Options = %q, want nosniff", got)
	}
	if got := rec.Header().Get("X-Frame-Options"); got != "DENY" {
		t.Errorf("/v1 X-Frame-Options = %q, want DENY", got)
	}
	if got := rec.Header().Get("Content-Security-Policy"); got != "default-src 'none'; frame-ancestors 'none'" {
		t.Errorf("/v1 Content-Security-Policy = %q, want deny-all", got)
	}
	if got := rec.Header().Get("Referrer-Policy"); got != "no-referrer" {
		t.Errorf("/v1 Referrer-Policy = %q, want no-referrer", got)
	}

	// /metrics (root, outside /v1) is also part of the API surface.
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/metrics", nil))
	if got := rec.Header().Get("X-Frame-Options"); got != "DENY" {
		t.Errorf("/metrics X-Frame-Options = %q, want DENY", got)
	}

	// A non-API/SPA path is NOT stamped by this middleware — the SPA handler
	// owns its (HTML-appropriate) headers.
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/projects/x/configs/y", nil))
	if got := rec.Header().Get("X-Frame-Options"); got != "" {
		t.Errorf("SPA path X-Frame-Options = %q, want empty (SPA handler sets its own)", got)
	}
	if got := rec.Header().Get("Content-Security-Policy"); got != "" {
		t.Errorf("SPA path CSP = %q, want empty from this middleware", got)
	}
}

// TestSPAIndexKeepsOwnCSP guards against a regression where the API hardening
// middleware would override or break the embedded SPA's own (self-scoped) CSP,
// which must allow 'self' assets — not the deny-all API policy.
func TestSPAIndexKeepsOwnCSP(t *testing.T) {
	rec := httptest.NewRecorder()
	// The SPA index path (falls through to the shell).
	web.Handler().ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))

	csp := rec.Header().Get("Content-Security-Policy")
	if csp == "" {
		t.Fatalf("SPA index has no Content-Security-Policy header")
	}
	// The SPA CSP must be the self-scoped HTML policy, not the deny-all API one.
	if csp == "default-src 'none'; frame-ancestors 'none'" {
		t.Fatalf("SPA index got the deny-all API CSP; the SPA handler's own CSP was clobbered")
	}
	// Sanity: it must allow same-origin document/assets.
	if got := rec.Header().Get("X-Content-Type-Options"); got != "nosniff" {
		t.Errorf("SPA X-Content-Type-Options = %q, want nosniff", got)
	}
	if got := rec.Header().Get("X-Frame-Options"); got != "DENY" {
		t.Errorf("SPA X-Frame-Options = %q, want DENY", got)
	}
}
