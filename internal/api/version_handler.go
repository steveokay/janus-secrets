package api

import (
	"net/http"

	"github.com/steveokay/janus-secrets/internal/version"
)

// handleVersion returns build metadata (version/commit/date). It is mounted
// under RequireAuth in production so build details are not exposed anonymously.
func (s *Server) handleVersion(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{
		"version": version.Version,
		"commit":  version.Commit,
		"date":    version.Date,
	})
}
