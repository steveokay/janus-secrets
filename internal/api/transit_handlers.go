package api

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/steveokay/janus-secrets/internal/authz"
	"github.com/steveokay/janus-secrets/internal/transit"
)

// transitKeyMeta is the engine's non-secret key view, re-exported for handlers.
type transitKeyMeta = transit.KeyMeta

// transitMeta renders a KeyMeta as the JSON response body. It carries only
// non-secret metadata — never key material.
func transitMeta(m transitKeyMeta) map[string]any {
	return map[string]any{
		"name": m.Name, "type": m.Type, "latest_version": m.LatestVersion,
		"min_decryption_version": m.MinDecryptionVersion, "deletion_allowed": m.DeletionAllowed,
		"versions": m.Versions,
	}
}

func (s *Server) handleTransitCreate(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name string `json:"name"`
		Type string `json:"type"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Name == "" {
		writeError(w, http.StatusBadRequest, CodeValidation, "name and type are required")
		return
	}
	res := authz.Resource{TransitKey: req.Name}
	if !s.authorize(w, r, authz.TransitManage, res, "transit.key.create", "transit/keys/"+req.Name) {
		return
	}
	m, err := s.transit.CreateKey(r.Context(), req.Name, req.Type)
	if err != nil {
		s.writeServiceError(w, err)
		return
	}
	if err := s.record(r, "transit.key.create", "transit/keys/"+req.Name, "success", "", req.Type); err != nil {
		writeError(w, http.StatusInternalServerError, CodeInternal, "internal error")
		return
	}
	writeJSON(w, http.StatusCreated, transitMeta(m))
}

func (s *Server) handleTransitList(w http.ResponseWriter, r *http.Request) {
	// Instance-scoped read: a viewer with instance transit:read passes.
	if err := s.can(r, authz.TransitRead, authz.Resource{}); err != nil {
		s.writeAuthzError(w, err)
		return
	}
	ms, err := s.transit.List(r.Context())
	if err != nil {
		s.writeServiceError(w, err)
		return
	}
	out := make([]map[string]any, 0, len(ms))
	for _, m := range ms {
		out = append(out, transitMeta(m))
	}
	writeJSON(w, http.StatusOK, map[string]any{"keys": out})
}

func (s *Server) handleTransitGet(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")
	if err := s.can(r, authz.TransitRead, authz.Resource{TransitKey: name}); err != nil {
		s.writeAuthzError(w, err)
		return
	}
	m, err := s.transit.Get(r.Context(), name)
	if err != nil {
		s.writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, transitMeta(m))
}

func (s *Server) handleTransitRotate(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")
	res := authz.Resource{TransitKey: name}
	if !s.authorize(w, r, authz.TransitManage, res, "transit.key.rotate", "transit/keys/"+name) {
		return
	}
	m, err := s.transit.Rotate(r.Context(), name)
	if err != nil {
		s.writeServiceError(w, err)
		return
	}
	if err := s.record(r, "transit.key.rotate", "transit/keys/"+name, "success", "", ""); err != nil {
		writeError(w, http.StatusInternalServerError, CodeInternal, "internal error")
		return
	}
	writeJSON(w, http.StatusOK, transitMeta(m))
}

func (s *Server) handleTransitConfig(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")
	res := authz.Resource{TransitKey: name}
	if !s.authorize(w, r, authz.TransitManage, res, "transit.key.config", "transit/keys/"+name) {
		return
	}
	var req struct {
		MinDecryptionVersion *int  `json:"min_decryption_version"`
		DeletionAllowed      *bool `json:"deletion_allowed"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, CodeValidation, "invalid body")
		return
	}
	if err := s.transit.UpdateConfig(r.Context(), name, req.MinDecryptionVersion, req.DeletionAllowed); err != nil {
		s.writeServiceError(w, err)
		return
	}
	if err := s.record(r, "transit.key.config", "transit/keys/"+name, "success", "", ""); err != nil {
		writeError(w, http.StatusInternalServerError, CodeInternal, "internal error")
		return
	}
	m, err := s.transit.Get(r.Context(), name)
	if err != nil {
		s.writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, transitMeta(m))
}

func (s *Server) handleTransitTrim(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")
	res := authz.Resource{TransitKey: name}
	if !s.authorize(w, r, authz.TransitManage, res, "transit.key.trim", "transit/keys/"+name) {
		return
	}
	var req struct {
		MinAvailableVersion int `json:"min_available_version"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, CodeValidation, "invalid body")
		return
	}
	if err := s.transit.Trim(r.Context(), name, req.MinAvailableVersion); err != nil {
		s.writeServiceError(w, err)
		return
	}
	if err := s.record(r, "transit.key.trim", "transit/keys/"+name, "success", "", ""); err != nil {
		writeError(w, http.StatusInternalServerError, CodeInternal, "internal error")
		return
	}
	m, err := s.transit.Get(r.Context(), name)
	if err != nil {
		s.writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, transitMeta(m))
}

func (s *Server) handleTransitDelete(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")
	res := authz.Resource{TransitKey: name}
	if !s.authorize(w, r, authz.TransitManage, res, "transit.key.delete", "transit/keys/"+name) {
		return
	}
	if err := s.transit.Delete(r.Context(), name); err != nil {
		s.writeServiceError(w, err)
		return
	}
	if err := s.record(r, "transit.key.delete", "transit/keys/"+name, "success", "", ""); err != nil {
		writeError(w, http.StatusInternalServerError, CodeInternal, "internal error")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
