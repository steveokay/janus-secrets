package web

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHandlerServesShellForDeepLink(t *testing.T) {
	rr := httptest.NewRecorder()
	Handler().ServeHTTP(rr, httptest.NewRequest("GET", "/projects/abc/configs/def", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("deep-link status = %d, want 200", rr.Code)
	}
	if ct := rr.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Fatalf("content-type = %q, want text/html", ct)
	}
	if !strings.Contains(rr.Body.String(), `id="root"`) {
		t.Fatal("shell body missing #root")
	}
	if csp := rr.Header().Get("Content-Security-Policy"); !strings.Contains(csp, "default-src 'self'") {
		t.Fatalf("missing/weak CSP: %q", csp)
	}
}

func TestHandlerServesRealAsset(t *testing.T) {
	rr := httptest.NewRecorder()
	Handler().ServeHTTP(rr, httptest.NewRequest("GET", "/index.html", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("index.html status = %d, want 200", rr.Code)
	}
}
