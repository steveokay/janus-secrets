package api

import (
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/steveokay/janus-secrets/internal/authz"
)

func (s *Server) handleVersionList(w http.ResponseWriter, r *http.Request) {
	res, cid, err := s.configResource(r)
	if err != nil {
		s.writeServiceError(w, err)
		return
	}
	if err := s.can(r, authz.ConfigRead, res); err != nil {
		s.writeAuthzError(w, err)
		return
	}
	vs, err := s.service.ListVersions(r.Context(), cid)
	if err != nil {
		s.writeServiceError(w, err)
		return
	}
	out := make([]map[string]any, 0, len(vs))
	for _, v := range vs {
		out = append(out, map[string]any{
			"version": v.Version, "message": v.Message,
			"created_by": v.CreatedBy, "created_at": v.CreatedAt.UTC().Format(time.RFC3339),
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"versions": out})
}

func (s *Server) handleVersionDiff(w http.ResponseWriter, r *http.Request) {
	res, cid, err := s.configResource(r)
	if err != nil {
		s.writeServiceError(w, err)
		return
	}
	if err := s.can(r, authz.ConfigRead, res); err != nil {
		s.writeAuthzError(w, err)
		return
	}
	a, aErr := strconv.Atoi(r.URL.Query().Get("a"))
	b, bErr := strconv.Atoi(r.URL.Query().Get("b"))
	if aErr != nil || bErr != nil || a < 1 || b < 1 {
		writeError(w, http.StatusBadRequest, CodeValidation, "a and b must be positive integers")
		return
	}
	d, err := s.service.DiffVersions(r.Context(), cid, a, b)
	if err != nil {
		s.writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"a": a, "b": b, "added": d.Added, "changed": d.Changed, "removed": d.Removed,
	})
}

type rollbackRequest struct {
	TargetVersion int    `json:"target_version"`
	Message       string `json:"message"`
}

func (s *Server) handleRollback(w http.ResponseWriter, r *http.Request) {
	res, cid, err := s.configResource(r)
	if err != nil {
		s.writeServiceError(w, err)
		return
	}
	if !s.authorize(w, r, authz.SecretWrite, res, "config.rollback", "configs/"+cid) {
		return
	}
	var req rollbackRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.TargetVersion < 1 {
		writeError(w, http.StatusBadRequest, CodeValidation, "target_version must be a positive integer")
		return
	}
	cv, err := s.service.Rollback(r.Context(), cid, req.TargetVersion, req.Message, actorOf(r))
	if err != nil {
		s.writeServiceError(w, err)
		return
	}
	if err := s.record(r, "config.rollback", "configs/"+cid, "success", "", "target="+strconv.Itoa(req.TargetVersion)); err != nil {
		writeError(w, http.StatusInternalServerError, CodeInternal, "internal error")
		return
	}
	writeJSON(w, http.StatusOK, versionResponse{
		Version: cv.Version, ID: cv.ID, CreatedAt: cv.CreatedAt.UTC().Format(time.RFC3339),
	})
}
