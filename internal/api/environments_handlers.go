package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
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
	ID             string  `json:"id"`
	ProjectID      string  `json:"project_id"`
	Slug           string  `json:"slug"`
	Name           string  `json:"name"`
	CreatedAt      string  `json:"created_at"`
	LastActivityAt *string `json:"last_activity_at"`
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

type cloneEnvRequest struct {
	Slug string `json:"slug"`
	Name string `json:"name"`
}

// handleEnvClone deep-copies a source environment's config tree and each config's
// own latest secrets into a new environment in the same project (admin+, via
// env:create). The audit event is value-free: it carries only the new/source env
// ids ("from:<srcEnvID>"), never the slug/name or any secret value — the clone's
// decrypt->re-encrypt happens entirely inside the service, which logs/audits
// nothing.
func (s *Server) handleEnvClone(w http.ResponseWriter, r *http.Request) {
	pid := chi.URLParam(r, "pid")
	eid := chi.URLParam(r, "eid")
	if !s.authorize(w, r, authz.EnvCreate, authz.Resource{ProjectID: pid}, "env.clone", "environments/"+eid) {
		return
	}
	var req cloneEnvRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Slug == "" {
		writeError(w, http.StatusBadRequest, CodeValidation, "slug is required")
		return
	}
	newEnv, err := s.service.CloneEnvironment(r.Context(), pid, eid, req.Slug, req.Name, actorOf(r))
	if err != nil {
		s.writeServiceError(w, err)
		return
	}
	if err := s.record(r, "env.clone", "environments/"+newEnv.ID, "success", "", "from:"+eid); err != nil {
		writeError(w, http.StatusInternalServerError, CodeInternal, "internal error")
		return
	}
	writeJSON(w, http.StatusCreated, envView(newEnv))
}

func (s *Server) handleEnvList(w http.ResponseWriter, r *http.Request) {
	pid := chi.URLParam(r, "pid")
	if err := s.can(r, authz.ProjectRead, authz.Resource{ProjectID: pid}); err != nil {
		s.writeAuthzError(w, err)
		return
	}
	pp, err := parsePageParams(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, CodeValidation, err.Error())
		return
	}
	envs, err := store.NewEnvironmentRepo(s.st).ListByProjectPage(r.Context(), pid, pp.limit, pp.after)
	if err != nil {
		s.writeServiceError(w, err)
		return
	}
	out := make([]envResponse, 0, len(envs))
	for _, e := range envs {
		out = append(out, envView(e))
	}
	ids := make([]string, len(out))
	for i := range out {
		ids[i] = out[i].ID
	}
	act, err := store.NewEnvironmentRepo(s.st).LastActivity(r.Context(), ids)
	if err != nil {
		writeError(w, http.StatusInternalServerError, CodeInternal, "internal error")
		return
	}
	for i := range out {
		if ts, ok := act[out[i].ID]; ok {
			v := ts.UTC().Format(time.RFC3339)
			out[i].LastActivityAt = &v
		}
	}
	var next *string
	if len(envs) > 0 {
		last := envs[len(envs)-1]
		next = nextCursor(pp.limit, len(envs), last.CreatedAt, last.ID)
	}
	writeJSON(w, http.StatusOK, map[string]any{"environments": out, "next_cursor": next})
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

func (s *Server) handleEnvRename(w http.ResponseWriter, r *http.Request) {
	eid := chi.URLParam(r, "eid")
	res, err := s.resolveScopeResource(r.Context(), "environment", eid)
	if err != nil {
		s.writeServiceError(w, err)
		return
	}
	if !s.authorize(w, r, authz.EnvUpdate, res, "env.update", "environments/"+eid) {
		return
	}
	var req renameRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || strings.TrimSpace(req.Name) == "" {
		writeError(w, http.StatusBadRequest, CodeValidation, "name is required")
		return
	}
	repo := store.NewEnvironmentRepo(s.st)
	if err := repo.UpdateName(r.Context(), eid, req.Name); err != nil {
		s.writeServiceError(w, err)
		return
	}
	if err := s.record(r, "env.update", "environments/"+eid, "success", "", ""); err != nil {
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
