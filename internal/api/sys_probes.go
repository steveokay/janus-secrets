package api

import (
	"context"
	"net/http"
	"time"
)

// handleLive reports process liveness only. It touches nothing (no DB, no
// keyring) so orchestrators can distinguish "process wedged" from "not ready".
func (s *Server) handleLive(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "live"})
}

// handleReady reports whether the instance can serve secret operations:
// database reachable AND seal initialized AND unsealed. Each failure mode has
// its own error code so probes and operators can tell them apart. Probes are
// deliberately not audited (they fire every few seconds and touch no secrets).
func (s *Server) handleReady(w http.ResponseWriter, r *http.Request) {
	// Unit-test servers are wired without a store; production always has one.
	if s.st != nil {
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		if err := s.st.Ping(ctx); err != nil {
			writeError(w, http.StatusServiceUnavailable, CodeDBUnavailable, "database unreachable")
			return
		}
	}
	initialized, _, err := s.sealConfig(r)
	if err != nil {
		writeError(w, http.StatusInternalServerError, CodeInternal, "internal error")
		return
	}
	if !initialized {
		writeError(w, http.StatusServiceUnavailable, CodeNotInitialized, "seal is not initialized")
		return
	}
	if s.keyring.Sealed() {
		writeError(w, http.StatusServiceUnavailable, CodeSealed, "server is sealed; unseal via /v1/sys/unseal")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ready"})
}
