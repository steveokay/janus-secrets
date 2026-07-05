package api

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/steveokay/janus-secrets/internal/authz"
	"github.com/steveokay/janus-secrets/internal/store"
)

type createConfigRequest struct {
	Name         string  `json:"name"`
	InheritsFrom *string `json:"inherits_from"`
}

type configResponse struct {
	ID            string  `json:"id"`
	EnvironmentID string  `json:"environment_id"`
	Name          string  `json:"name"`
	InheritsFrom  *string `json:"inherits_from"`
	CreatedAt     string  `json:"created_at"`
}

func configView(c *store.Config) configResponse {
	return configResponse{ID: c.ID, EnvironmentID: c.EnvironmentID, Name: c.Name,
		InheritsFrom: c.InheritsFrom, CreatedAt: c.CreatedAt.UTC().Format(time.RFC3339)}
}

func (s *Server) handleConfigCreate(w http.ResponseWriter, r *http.Request) {
	pid := chi.URLParam(r, "pid")
	eid := chi.URLParam(r, "eid")
	if !s.authorize(w, r, authz.ConfigCreate, authz.Resource{ProjectID: pid, EnvID: eid}, "config.create", "environments/"+eid+"/configs") {
		return
	}
	var req createConfigRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Name == "" {
		writeError(w, http.StatusBadRequest, CodeValidation, "name is required")
		return
	}
	c, err := s.service.CreateConfig(r.Context(), eid, req.Name, req.InheritsFrom)
	if err != nil {
		s.writeServiceError(w, err)
		return
	}
	if err := s.record(r, "config.create", "configs/"+c.ID, "success", "", ""); err != nil {
		writeError(w, http.StatusInternalServerError, CodeInternal, "internal error")
		return
	}
	writeJSON(w, http.StatusCreated, configView(c))
}

func (s *Server) handleConfigList(w http.ResponseWriter, r *http.Request) {
	pid := chi.URLParam(r, "pid")
	eid := chi.URLParam(r, "eid")
	if err := s.can(r, authz.ConfigRead, authz.Resource{ProjectID: pid, EnvID: eid}); err != nil {
		s.writeAuthzError(w, err)
		return
	}
	cfgs, err := store.NewConfigRepo(s.st).ListByEnvironment(r.Context(), eid)
	if err != nil {
		s.writeServiceError(w, err)
		return
	}
	out := make([]configResponse, 0, len(cfgs))
	for _, c := range cfgs {
		out = append(out, configView(c))
	}
	writeJSON(w, http.StatusOK, map[string]any{"configs": out})
}

// configResource resolves the full project→env→config chain for a {cid} route.
func (s *Server) configResource(r *http.Request) (authz.Resource, string, error) {
	cid := chi.URLParam(r, "cid")
	res, err := s.resolveScopeResource(r.Context(), "config", cid)
	return res, cid, err
}

func (s *Server) handleConfigGet(w http.ResponseWriter, r *http.Request) {
	res, cid, err := s.configResource(r)
	if err != nil {
		s.writeServiceError(w, err)
		return
	}
	if err := s.can(r, authz.ConfigRead, res); err != nil {
		s.writeAuthzError(w, err)
		return
	}
	c, err := store.NewConfigRepo(s.st).Get(r.Context(), cid)
	if err != nil {
		s.writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, configView(c))
}

func (s *Server) handleConfigDelete(w http.ResponseWriter, r *http.Request) {
	res, cid, err := s.configResource(r)
	if err != nil {
		s.writeServiceError(w, err)
		return
	}
	destroy := r.URL.Query().Get("destroy") == "true"
	detail := "soft"
	if destroy {
		detail = "destroy"
	}
	if !s.authorize(w, r, authz.ConfigDelete, res, "config.delete", "configs/"+cid) {
		return
	}
	repo := store.NewConfigRepo(s.st)
	if destroy {
		err = repo.Destroy(r.Context(), cid)
	} else {
		err = repo.SoftDelete(r.Context(), cid)
	}
	if err != nil {
		s.writeServiceError(w, err)
		return
	}
	if err := s.record(r, "config.delete", "configs/"+cid, "success", "", detail); err != nil {
		writeError(w, http.StatusInternalServerError, CodeInternal, "internal error")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleConfigRestore(w http.ResponseWriter, r *http.Request) {
	// A soft-deleted config is invisible to resolveScopeResource (live rows
	// only), so resolve its scope via a deleted-inclusive read to authorize
	// before undeleting.
	cid := chi.URLParam(r, "cid")
	repo := store.NewConfigRepo(s.st)
	res, err := s.resolveConfigScopeIncludingDeleted(r.Context(), cid)
	if err != nil {
		s.writeServiceError(w, err)
		return
	}
	if !s.authorize(w, r, authz.ConfigDelete, res, "config.restore", "configs/"+cid) {
		return
	}
	if err := repo.Undelete(r.Context(), cid); err != nil {
		s.writeServiceError(w, err)
		return
	}
	if err := s.record(r, "config.restore", "configs/"+cid, "success", "", ""); err != nil {
		writeError(w, http.StatusInternalServerError, CodeInternal, "internal error")
		return
	}
	c, err := repo.Get(r.Context(), cid)
	if err != nil {
		s.writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, configView(c))
}

// resolveConfigScopeIncludingDeleted builds the project→env→config resource for
// a config that may be soft-deleted (needed to authorize restore). It reads the
// config row deleted-inclusively, then reuses the live environment→project
// chain. Returns store.ErrNotFound if the config row does not exist at all.
func (s *Server) resolveConfigScopeIncludingDeleted(ctx context.Context, cid string) (authz.Resource, error) {
	c, err := store.NewConfigRepo(s.st).GetIncludingDeleted(ctx, cid)
	if err != nil {
		return authz.Resource{}, err
	}
	env, err := store.NewEnvironmentRepo(s.st).Get(ctx, c.EnvironmentID)
	if err != nil {
		return authz.Resource{}, err
	}
	return authz.Resource{ProjectID: env.ProjectID, EnvID: env.ID, ConfigID: cid}, nil
}
