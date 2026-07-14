package api

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/steveokay/janus-secrets/internal/authz"
)

// handleKEKRotate installs a fresh project KEK as the next version. Owner-only
// (authz.KEKManage). The response carries only the new version integer — never
// key material. Sealed → 503, missing project → 404 (via writeServiceError).
func (s *Server) handleKEKRotate(w http.ResponseWriter, r *http.Request) {
	pid := chi.URLParam(r, "pid")
	if !s.authorize(w, r, authz.KEKManage, authz.Resource{ProjectID: pid}, "project.kek.rotate", "projects/"+pid+"/kek") {
		return
	}
	version, err := s.projectKeys.Rotate(r.Context(), pid)
	if err != nil {
		s.writeServiceError(w, err)
		return
	}
	if err := s.record(r, "project.kek.rotate", "projects/"+pid+"/kek", "success", "", ""); err != nil {
		writeError(w, http.StatusInternalServerError, CodeInternal, "internal error")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"kek_version": version})
}

// handleKEKRewrap lazily re-wraps every DEK under the latest KEK and retires
// superseded KEK versions. Owner-only. The response carries only counts and
// retired version numbers — never key material.
func (s *Server) handleKEKRewrap(w http.ResponseWriter, r *http.Request) {
	pid := chi.URLParam(r, "pid")
	if !s.authorize(w, r, authz.KEKManage, authz.Resource{ProjectID: pid}, "project.kek.rewrap", "projects/"+pid+"/kek") {
		return
	}
	res, err := s.projectKeys.Rewrap(r.Context(), pid)
	if err != nil {
		s.writeServiceError(w, err)
		return
	}
	if err := s.record(r, "project.kek.rewrap", "projects/"+pid+"/kek", "success", "", ""); err != nil {
		writeError(w, http.StatusInternalServerError, CodeInternal, "internal error")
		return
	}
	retired := res.Retired
	if retired == nil {
		retired = []int{}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"rewrapped":        res.Rewrapped,
		"retired_versions": retired,
		"remaining":        0,
	})
}

// handleKEKStatus reports the current KEK version and any superseded versions
// still awaiting re-wrap. A plain read gated on kek:manage (owner-only); no
// audit event, mirroring the sibling project read handlers.
func (s *Server) handleKEKStatus(w http.ResponseWriter, r *http.Request) {
	pid := chi.URLParam(r, "pid")
	if err := s.can(r, authz.KEKManage, authz.Resource{ProjectID: pid}); err != nil {
		s.writeAuthzError(w, err)
		return
	}
	st, err := s.projectKeys.StatusFor(r.Context(), pid)
	if err != nil {
		s.writeServiceError(w, err)
		return
	}
	pending := make([]map[string]any, 0, len(st.Pending))
	for _, p := range st.Pending {
		pending = append(pending, map[string]any{"version": p.Version, "dek_count": p.DEKCount})
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"current_version": st.CurrentVersion,
		"pending":         pending,
	})
}
