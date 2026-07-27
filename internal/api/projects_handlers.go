package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/steveokay/janus-secrets/internal/auth"
	"github.com/steveokay/janus-secrets/internal/authz"
	"github.com/steveokay/janus-secrets/internal/store"
)

type createProjectRequest struct {
	Slug string `json:"slug"`
	Name string `json:"name"`
	// OwnerGroupID names the group that will own the project. Required for a
	// delegated creator (a member of a group with can_create_projects) unless
	// they belong to exactly one such group; optional for an instance admin,
	// who may use it to hand a new project straight to a team.
	OwnerGroupID string `json:"owner_group_id"`
}

type renameRequest struct {
	Name string `json:"name"`
}

type projectResponse struct {
	ID             string  `json:"id"`
	Slug           string  `json:"slug"`
	Name           string  `json:"name"`
	CreatedAt      string  `json:"created_at"`
	LastActivityAt *string `json:"last_activity_at"`
}

func projectView(p *store.Project) projectResponse {
	return projectResponse{ID: p.ID, Slug: p.Slug, Name: p.Name,
		CreatedAt: p.CreatedAt.UTC().Format(time.RFC3339)}
}

// handleProjectCreate creates a project, by either of two routes.
//
// The historical route is instance-scoped project:create (admin+). The problem
// with it being the ONLY route is that every role carrying project:create at
// instance scope also carries project:read there — roles are cumulative bundles
// — so delegating creation meant revealing every project in the organisation.
// "Teams self-serve" and "teams cannot see each other" were mutually exclusive.
//
// The second route closes that: a member of a group marked can_create_projects
// may create a project owned by that group. The new project is bound to the
// group at ADMIN so the team can work immediately, and to the creator at OWNER
// so it always has someone who can administer and delete it. Neither grant is
// an escalation — the project is brand new and empty, and nothing outside it
// becomes reachable.
//
// The capability is checked here rather than in internal/authz on purpose:
// creation is the one operation with no existing resource to authorize against,
// and folding it into the role ladder would reintroduce the instance-wide read.
// The engine stays a pure decision function over roles.
func (s *Server) handleProjectCreate(w http.ResponseWriter, r *http.Request) {
	var req createProjectRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Slug == "" {
		writeError(w, http.StatusBadRequest, CodeValidation, "slug is required")
		return
	}

	p, _ := PrincipalFrom(r.Context())
	instanceAllowed := s.can(r, authz.ProjectCreate, authz.Instance()) == nil

	// Resolve the owning group when one was named (or is required).
	var owner *store.Group
	if req.OwnerGroupID != "" || !instanceAllowed {
		g, err := s.resolveCreatorGroup(r, req.OwnerGroupID)
		if err != nil {
			// Deny-by-default and indistinguishable: an unknown group, a group
			// the caller is not in, and a group without the capability are the
			// same 403, so this is not a probe for which groups exist.
			if !instanceAllowed {
				if aerr := s.record(r, "project.create", "projects", "denied", CodeForbidden, ""); aerr != nil {
					writeError(w, http.StatusInternalServerError, CodeInternal, "internal error")
					return
				}
				writeError(w, http.StatusForbidden, CodeForbidden, "access denied")
				return
			}
			writeError(w, http.StatusBadRequest, CodeValidation, "unknown owner group")
			return
		}
		owner = g
	}

	proj, err := s.service.CreateProject(r.Context(), req.Slug, req.Name)
	if err != nil {
		s.writeServiceError(w, err)
		return
	}

	// Seed access. A delegated creator would otherwise be locked out of the
	// project they just made, since they hold no instance binding.
	detail := ""
	if owner != nil {
		if err := s.seedProjectOwnership(r, proj.ID, owner.ID, p.ID); err != nil {
			writeError(w, http.StatusInternalServerError, CodeInternal, "internal error")
			return
		}
		detail = "owner_group=" + owner.Name
	}
	if err := s.record(r, "project.create", "projects/"+proj.ID, "success", "", detail); err != nil {
		writeError(w, http.StatusInternalServerError, CodeInternal, "internal error")
		return
	}
	writeJSON(w, http.StatusCreated, projectView(proj))
}

// resolveCreatorGroup returns the group that will own a new project, verifying
// the caller is a member of it AND that it may create projects. When no id is
// supplied and the caller has exactly one such group, that group is used; with
// several, the caller must name one rather than have us guess which team owns
// the project.
func (s *Server) resolveCreatorGroup(r *http.Request, groupID string) (*store.Group, error) {
	p, _ := PrincipalFrom(r.Context())
	if p.Kind != auth.KindUser {
		return nil, errNoCreatorGroup // service tokens never carry group membership
	}
	groups, err := s.groupsRepo().CreatorGroupsForUser(r.Context(), p.ID)
	if err != nil {
		return nil, err
	}
	if groupID == "" {
		if len(groups) == 1 {
			return groups[0], nil
		}
		return nil, errNoCreatorGroup
	}
	for _, g := range groups {
		if g.ID == groupID {
			return g, nil
		}
	}
	return nil, errNoCreatorGroup
}

var errNoCreatorGroup = errors.New("api: no creator group")

// seedProjectOwnership binds the owning group at admin and the creator at
// owner. The group binding is what makes the project the TEAM's rather than one
// person's; the direct owner binding guarantees the project is never orphaned
// (a group binding can never be owner, by design).
func (s *Server) seedProjectOwnership(r *http.Request, projectID, groupID, userID string) error {
	pid := projectID
	if _, err := s.groupBindingsRepo().Create(r.Context(), store.GroupRoleBindingInput{
		GroupID:    groupID,
		ScopeLevel: "project",
		ProjectID:  &pid,
		Role:       string(authz.RoleAdmin),
		CreatedBy:  &userID,
	}); err != nil {
		return err
	}
	return s.authz.Grant(r.Context(), store.RoleBindingInput{
		SubjectUserID: userID,
		ScopeLevel:    "project",
		ProjectID:     &pid,
		Role:          string(authz.RoleOwner),
		CreatedBy:     &userID,
	})
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
	ids := make([]string, len(out))
	for i := range out {
		ids[i] = out[i].ID
	}
	act, err := store.NewProjectRepo(s.st).LastActivity(r.Context(), ids)
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

func (s *Server) handleProjectRename(w http.ResponseWriter, r *http.Request) {
	pid := chi.URLParam(r, "pid")
	if !s.authorize(w, r, authz.ProjectUpdate, authz.Resource{ProjectID: pid}, "project.update", "projects/"+pid) {
		return
	}
	var req renameRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || strings.TrimSpace(req.Name) == "" {
		writeError(w, http.StatusBadRequest, CodeValidation, "name is required")
		return
	}
	repo := store.NewProjectRepo(s.st)
	if err := repo.UpdateName(r.Context(), pid, req.Name); err != nil {
		s.writeServiceError(w, err)
		return
	}
	if err := s.record(r, "project.update", "projects/"+pid, "success", "", ""); err != nil {
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
