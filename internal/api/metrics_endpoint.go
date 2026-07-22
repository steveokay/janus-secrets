package api

import (
	"crypto/subtle"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
)

// handleMetrics renders the Prometheus exposition. Auth is enforced by the
// metricsAuth middleware wrapping this handler.
func (s *Server) handleMetrics(w http.ResponseWriter, r *http.Request) {
	if s.metrics == nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	_, _ = s.metrics.Registry().WriteTo(w)
}

// metricsAuth gates /metrics with a static bearer token. When no token is
// configured the endpoint returns 404 (nothing is exposed until the operator
// opts in). When a token is set, the request must carry a matching
// `Authorization: Bearer <token>` (constant-time compare), else 401.
func (s *Server) metricsAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.metricsToken == "" {
			http.NotFound(w, r)
			return
		}
		const prefix = "Bearer "
		h := r.Header.Get("Authorization")
		if !strings.HasPrefix(h, prefix) {
			writeError(w, http.StatusUnauthorized, CodeUnauthenticated, "metrics token required")
			return
		}
		got := strings.TrimPrefix(h, prefix)
		if subtle.ConstantTimeCompare([]byte(got), []byte(s.metricsToken)) != 1 {
			writeError(w, http.StatusUnauthorized, CodeUnauthenticated, "invalid metrics token")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// instrument records request count + duration on the app metrics, keyed by the
// chi route pattern (not the raw path) so cardinality stays bounded. It skips
// /metrics itself. Mounted alongside requestLogger. No-op when metrics are not
// wired (unit-test servers).
func (s *Server) instrument(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.metrics == nil || r.URL.Path == "/metrics" {
			next.ServeHTTP(w, r)
			return
		}
		start := time.Now()
		sw := &statusWriter{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(sw, r)
		route := chi.RouteContext(r.Context()).RoutePattern()
		if route == "" {
			// Unmatched routes (404) share one bucket rather than exploding the
			// label space with arbitrary raw paths.
			route = "unmatched"
		}
		s.metrics.observeHTTP(r.Method, route, sw.status, time.Since(start))
	})
}
