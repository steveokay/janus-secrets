package api

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/steveokay/janus-secrets/internal/authz"
	"github.com/steveokay/janus-secrets/internal/store"
)

type createProjectRequest struct {
	Slug string `json:"slug"`
	Name string `json:"name"`
}

type projectResponse struct {
	ID        string `json:"id"`
	Slug      string `json:"slug"`
	Name      string `json:"name"`
	CreatedAt string `json:"created_at"`
}

func projectView(p *store.Project) projectResponse {
	return projectResponse{ID: p.ID, Slug: p.Slug, Name: p.Name,
		CreatedAt: p.CreatedAt.UTC().Format(time.RFC3339)}
}

func (s *Server) handleProjectCreate(w http.ResponseWriter, r *http.Request) {
	if !s.authorize(w, r, authz.ProjectCreate, authz.Instance(), "project.create", "projects") {
		return
	}
	var req createProjectRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Slug == "" {
		writeError(w, http.StatusBadRequest, CodeValidation, "slug is required")
		return
	}
	p, err := s.service.CreateProject(r.Context(), req.Slug, req.Name)
	if err != nil {
		s.writeServiceError(w, err)
		return
	}
	if err := s.record(r, "project.create", "projects/"+p.ID, "success", "", ""); err != nil {
		writeError(w, http.StatusInternalServerError, CodeInternal, "internal error")
		return
	}
	writeJSON(w, http.StatusCreated, projectView(p))
}

func (s *Server) handleProjectList(w http.ResponseWriter, r *http.Request) {
	pp, err := parsePageParams(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, CodeValidation, err.Error())
		return
	}
	ps, err := store.NewProjectRepo(s.st).ListPage(r.Context(), pp.limit, pp.after)
	if err != nil {
		writeError(w, http.StatusInternalServerError, CodeInternal, "internal error")
		return
	}
	out := make([]projectResponse, 0, len(ps))
	for _, p := range ps {
		if s.can(r, authz.ProjectRead, authz.Resource{ProjectID: p.ID}) == nil {
			out = append(out, projectView(p))
		}
	}
	var next *string
	if len(ps) > 0 {
		last := ps[len(ps)-1]
		next = nextCursor(pp.limit, len(ps), last.CreatedAt, last.ID)
	}
	writeJSON(w, http.StatusOK, map[string]any{"projects": out, "next_cursor": next})
}

func (s *Server) handleProjectGet(w http.ResponseWriter, r *http.Request) {
	pid := chi.URLParam(r, "pid")
	if err := s.can(r, authz.ProjectRead, authz.Resource{ProjectID: pid}); err != nil {
		s.writeAuthzError(w, err)
		return
	}
	p, err := store.NewProjectRepo(s.st).Get(r.Context(), pid)
	if err != nil {
		s.writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, projectView(p))
}

func (s *Server) handleProjectDelete(w http.ResponseWriter, r *http.Request) {
	pid := chi.URLParam(r, "pid")
	destroy := r.URL.Query().Get("destroy") == "true"
	detail := "soft"
	if destroy {
		detail = "destroy"
	}
	if !s.authorize(w, r, authz.ProjectDelete, authz.Resource{ProjectID: pid}, "project.delete", "projects/"+pid) {
		return
	}
	repo := store.NewProjectRepo(s.st)
	var err error
	if destroy {
		err = repo.Destroy(r.Context(), pid)
	} else {
		err = repo.SoftDelete(r.Context(), pid)
	}
	if err != nil {
		s.writeServiceError(w, err)
		return
	}
	if err := s.record(r, "project.delete", "projects/"+pid, "success", "", detail); err != nil {
		writeError(w, http.StatusInternalServerError, CodeInternal, "internal error")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleProjectRestore(w http.ResponseWriter, r *http.Request) {
	pid := chi.URLParam(r, "pid")
	if !s.authorize(w, r, authz.ProjectDelete, authz.Resource{ProjectID: pid}, "project.restore", "projects/"+pid) {
		return
	}
	repo := store.NewProjectRepo(s.st)
	if err := repo.Undelete(r.Context(), pid); err != nil {
		s.writeServiceError(w, err)
		return
	}
	if err := s.record(r, "project.restore", "projects/"+pid, "success", "", ""); err != nil {
		writeError(w, http.StatusInternalServerError, CodeInternal, "internal error")
		return
	}
	p, err := repo.Get(r.Context(), pid)
	if err != nil {
		s.writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, projectView(p))
}
