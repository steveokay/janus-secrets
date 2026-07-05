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

type createEnvRequest struct {
	Slug string `json:"slug"`
	Name string `json:"name"`
}

type envResponse struct {
	ID        string `json:"id"`
	ProjectID string `json:"project_id"`
	Slug      string `json:"slug"`
	Name      string `json:"name"`
	CreatedAt string `json:"created_at"`
}

func envView(e *store.Environment) envResponse {
	return envResponse{ID: e.ID, ProjectID: e.ProjectID, Slug: e.Slug, Name: e.Name,
		CreatedAt: e.CreatedAt.UTC().Format(time.RFC3339)}
}

func (s *Server) handleEnvCreate(w http.ResponseWriter, r *http.Request) {
	pid := chi.URLParam(r, "pid")
	if !s.authorize(w, r, authz.EnvCreate, authz.Resource{ProjectID: pid}, "env.create", "projects/"+pid+"/environments") {
		return
	}
	var req createEnvRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Slug == "" {
		writeError(w, http.StatusBadRequest, CodeValidation, "slug is required")
		return
	}
	e, err := s.service.CreateEnvironment(r.Context(), pid, req.Slug, req.Name)
	if err != nil {
		s.writeServiceError(w, err)
		return
	}
	if err := s.record(r, "env.create", "environments/"+e.ID, "success", "", ""); err != nil {
		writeError(w, http.StatusInternalServerError, CodeInternal, "internal error")
		return
	}
	writeJSON(w, http.StatusCreated, envView(e))
}

func (s *Server) handleEnvList(w http.ResponseWriter, r *http.Request) {
	pid := chi.URLParam(r, "pid")
	if err := s.can(r, authz.ProjectRead, authz.Resource{ProjectID: pid}); err != nil {
		s.writeAuthzError(w, err)
		return
	}
	envs, err := store.NewEnvironmentRepo(s.st).ListByProject(r.Context(), pid)
	if err != nil {
		s.writeServiceError(w, err)
		return
	}
	out := make([]envResponse, 0, len(envs))
	for _, e := range envs {
		out = append(out, envView(e))
	}
	writeJSON(w, http.StatusOK, map[string]any{"environments": out})
}

func (s *Server) handleEnvGet(w http.ResponseWriter, r *http.Request) {
	eid := chi.URLParam(r, "eid")
	res, err := s.resolveScopeResource(r.Context(), "environment", eid)
	if err != nil {
		s.writeServiceError(w, err)
		return
	}
	if err := s.can(r, authz.ProjectRead, res); err != nil {
		s.writeAuthzError(w, err)
		return
	}
	e, err := store.NewEnvironmentRepo(s.st).Get(r.Context(), eid)
	if err != nil {
		s.writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, envView(e))
}

func (s *Server) handleEnvDelete(w http.ResponseWriter, r *http.Request) {
	eid := chi.URLParam(r, "eid")
	destroy := r.URL.Query().Get("destroy") == "true"
	detail := "soft"
	if destroy {
		detail = "destroy"
	}
	res, err := s.resolveScopeResource(r.Context(), "environment", eid)
	if err != nil {
		s.writeServiceError(w, err)
		return
	}
	if !s.authorize(w, r, authz.EnvDelete, res, "env.delete", "environments/"+eid) {
		return
	}
	repo := store.NewEnvironmentRepo(s.st)
	if destroy {
		err = repo.Destroy(r.Context(), eid)
	} else {
		err = repo.SoftDelete(r.Context(), eid)
	}
	if err != nil {
		s.writeServiceError(w, err)
		return
	}
	if err := s.record(r, "env.delete", "environments/"+eid, "success", "", detail); err != nil {
		writeError(w, http.StatusInternalServerError, CodeInternal, "internal error")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleEnvRestore(w http.ResponseWriter, r *http.Request) {
	// A soft-deleted environment is invisible to resolveScopeResource (live rows
	// only), so resolve its scope via a deleted-inclusive read to authorize
	// before undeleting.
	eid := chi.URLParam(r, "eid")
	res, err := s.resolveEnvScopeIncludingDeleted(r.Context(), eid)
	if err != nil {
		s.writeServiceError(w, err)
		return
	}
	if !s.authorize(w, r, authz.EnvDelete, res, "env.restore", "environments/"+eid) {
		return
	}
	repo := store.NewEnvironmentRepo(s.st)
	if err := repo.Undelete(r.Context(), eid); err != nil {
		s.writeServiceError(w, err)
		return
	}
	if err := s.record(r, "env.restore", "environments/"+eid, "success", "", ""); err != nil {
		writeError(w, http.StatusInternalServerError, CodeInternal, "internal error")
		return
	}
	e, err := repo.Get(r.Context(), eid)
	if err != nil {
		s.writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, envView(e))
}

// resolveEnvScopeIncludingDeleted builds the project→env resource for an
// environment that may be soft-deleted (needed to authorize restore). It reads
// the environment row deleted-inclusively so a caller cannot reach another
// project's environment by putting its id under a pid they control. Returns
// store.ErrNotFound if the environment row does not exist at all.
func (s *Server) resolveEnvScopeIncludingDeleted(ctx context.Context, eid string) (authz.Resource, error) {
	env, err := store.NewEnvironmentRepo(s.st).GetIncludingDeleted(ctx, eid)
	if err != nil {
		return authz.Resource{}, err
	}
	return authz.Resource{ProjectID: env.ProjectID, EnvID: eid}, nil
}
