package api

import (
	"net/http"
	"strings"
)

// securityHeaders sets a small, fixed set of hardening response headers on the
// JSON API surface (every /v1/* route and the root /metrics endpoint). API
// responses are JSON and are never rendered as an HTML document, so a deny-all
// Content-Security-Policy is both correct and safe here.
//
// It deliberately does NOT touch non-API paths: the embedded SPA is served via
// the router's NotFound fallback (internal/web.Handler), which sets its own
// HTML-appropriate CSP + headers. Gating on the API path prefix avoids
// clobbering or double-wrapping those.
//
// Headers set (before the handler runs, so they survive even on early error
// responses):
//   - X-Content-Type-Options: nosniff
//   - X-Frame-Options: DENY
//   - Content-Security-Policy: default-src 'none'; frame-ancestors 'none'
//   - Referrer-Policy: no-referrer
func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if isAPIPath(r.URL.Path) {
			h := w.Header()
			h.Set("X-Content-Type-Options", "nosniff")
			h.Set("X-Frame-Options", "DENY")
			h.Set("Content-Security-Policy", "default-src 'none'; frame-ancestors 'none'")
			h.Set("Referrer-Policy", "no-referrer")
		}
		next.ServeHTTP(w, r)
	})
}

// isAPIPath reports whether p is a JSON API path (the /v1/* surface or the root
// /metrics endpoint) as opposed to an embedded-SPA/static path served by the
// NotFound fallback.
func isAPIPath(p string) bool {
	return strings.HasPrefix(p, "/v1/") || p == "/metrics"
}
