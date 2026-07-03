package api

import (
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/steveokay/janus-secrets/internal/crypto"
)

// RequireUnsealed returns 503 {"error":{"code":"sealed"}} for every route
// except /v1/sys/* while the keyring is sealed. Sys routes stay reachable so
// the operator can initialize and unseal.
func RequireUnsealed(kr *crypto.Keyring) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if strings.HasPrefix(r.URL.Path, "/v1/sys/") {
				next.ServeHTTP(w, r)
				return
			}
			if kr.Sealed() {
				writeError(w, http.StatusServiceUnavailable, CodeSealed,
					"server is sealed; unseal via /v1/sys/unseal")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// requestLogger logs method, path, status, and duration ONLY. Request and
// response bodies are never logged: unseal shares and (later) secret values
// transit them.
func requestLogger(logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			sw := &statusWriter{ResponseWriter: w, status: http.StatusOK}
			next.ServeHTTP(sw, r)
			logger.Info("http",
				"method", r.Method,
				"path", r.URL.Path,
				"status", sw.status,
				"dur_ms", time.Since(start).Milliseconds())
		})
	}
}

// statusWriter captures the response status for logging.
type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(code int) {
	w.status = code
	w.ResponseWriter.WriteHeader(code)
}
