package api

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/steveokay/janus-secrets/internal/auth"
	"github.com/steveokay/janus-secrets/internal/authz"
	"github.com/steveokay/janus-secrets/internal/store"
)

// promoteActorUser returns the acting user's id for the config_locked_keys
// created_by FK, or "" for a service token (whose id is not a users.id and
// must not be written to the FK; the store maps "" -> NULL).
func promoteActorUser(r *http.Request) string {
	p, _ := PrincipalFrom(r.Context())
	if p.Kind == auth.KindUser {
		return p.ID
	}
	return ""
}

func (s *Server) handlePipelineGet(w http.ResponseWriter, r *http.Request) {
	pid := chi.URLParam(r, "pid")
	if err := s.can(r, authz.ProjectRead, authz.Resource{ProjectID: pid}); err != nil {
		s.writeAuthzError(w, err)
		return
	}
	steps, err := store.NewPipelineRepo(s.st).Get(r.Context(), pid)
	if err != nil {
		s.writeServiceError(w, err)
		return
	}
	ids := make([]string, 0, len(steps))
	for _, step := range steps {
		ids = append(ids, step.EnvironmentID)
	}
	writeJSON(w, http.StatusOK, map[string]any{"environment_ids": ids})
}

func (s *Server) handlePipelinePut(w http.ResponseWriter, r *http.Request) {
	pid := chi.URLParam(r, "pid")
	if !s.authorize(w, r, authz.PromotionManage, authz.Resource{ProjectID: pid}, "promotion.pipeline.set", "projects/"+pid+"/pipeline") {
		return
	}
	var body struct {
		EnvironmentIDs []string `json:"environment_ids"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, CodeValidation, "invalid body")
		return
	}
	if err := store.NewPipelineRepo(s.st).Set(r.Context(), pid, body.EnvironmentIDs); err != nil {
		s.writeServiceError(w, err)
		return
	}
	if err := s.record(r, "promotion.pipeline.set", "projects/"+pid+"/pipeline", "success", "", ""); err != nil {
		writeError(w, http.StatusInternalServerError, CodeInternal, "internal error")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"environment_ids": body.EnvironmentIDs})
}

func (s *Server) handleLockedKeysList(w http.ResponseWriter, r *http.Request) {
	res, cid, err := s.configResource(r)
	if err != nil {
		s.writeServiceError(w, err)
		return
	}
	if err := s.can(r, authz.ConfigRead, res); err != nil {
		s.writeAuthzError(w, err)
		return
	}
	keys, err := store.NewLockedKeyRepo(s.st).List(r.Context(), cid)
	if err != nil {
		s.writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"keys": keys})
}

func (s *Server) handleLockedKeyCreate(w http.ResponseWriter, r *http.Request) {
	res, cid, err := s.configResource(r)
	if err != nil {
		s.writeServiceError(w, err)
		return
	}
	if !s.authorize(w, r, authz.PromotionManage, res, "promotion.key.lock", "configs/"+cid+"/locked-keys") {
		return
	}
	var body struct {
		Key string `json:"key"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Key == "" {
		writeError(w, http.StatusBadRequest, CodeValidation, "key is required")
		return
	}
	if err := store.NewLockedKeyRepo(s.st).Lock(r.Context(), cid, body.Key, promoteActorUser(r)); err != nil {
		s.writeServiceError(w, err)
		return
	}
	if err := s.record(r, "promotion.key.lock", "configs/"+cid+"/locked-keys/"+body.Key, "success", "", ""); err != nil {
		writeError(w, http.StatusInternalServerError, CodeInternal, "internal error")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"key": body.Key, "locked": true})
}

func (s *Server) handleLockedKeyDelete(w http.ResponseWriter, r *http.Request) {
	res, cid, err := s.configResource(r)
	if err != nil {
		s.writeServiceError(w, err)
		return
	}
	key := chi.URLParam(r, "key")
	if !s.authorize(w, r, authz.PromotionManage, res, "promotion.key.unlock", "configs/"+cid+"/locked-keys/"+key) {
		return
	}
	if err := store.NewLockedKeyRepo(s.st).Unlock(r.Context(), cid, key); err != nil {
		s.writeServiceError(w, err)
		return
	}
	if err := s.record(r, "promotion.key.unlock", "configs/"+cid+"/locked-keys/"+key, "success", "", ""); err != nil {
		writeError(w, http.StatusInternalServerError, CodeInternal, "internal error")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"key": key, "locked": false})
}
