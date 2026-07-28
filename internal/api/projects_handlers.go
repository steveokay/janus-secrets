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
	// Owner is optional: omitted leaves it unchanged, "" clears it. Advisory
	// display metadata, guarded by the same project:update as the name.
	Owner *string `json:"owner"`
}

type projectResponse struct {
	ID   string `json:"id"`
	Slug string `json:"slug"`
	Name string `json:"name"`
	// Owner is an ADVISORY display label ("who do I ask about this service").
	// It grants nothing and is never consulted in an authorization decision —
	// real ownership is a role binding. Moved here from per-key annotations in
	// migration 000049.
	Owner          *string `json:"owner"`
	CreatedAt      string  `json:"created_at"`
	LastActivityAt *string `json:"last_activity_at"`
}

func projectView(p *store.Project) projectResponse {
	return projectResponse{ID: p.ID, Slug: p.Slug, Name: p.Name, Owner: p.Owner,
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
	p, _ := PrincipalFrom(r.Context())
	instanceAllowed := s.can(r, authz.ProjectCreate, authz.Instance()) == nil

	// AUTHORIZE BEFORE READING THE BODY. "May you create projects at all?" does
	// not depend on request content — only "which group owns this one" does. An
	// earlier revision decoded first so it could read owner_group_id, which let
	// an unauthorized caller tell a malformed body (400) from a denial (403)
	// and, worse, skipped the denied audit event on that path.
	var creatorGroups []*store.Group
	if p.Kind == auth.KindUser {
		gs, err := s.groupsRepo().CreatorGroupsForUser(r.Context(), p.ID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, CodeInternal, "internal error")
			return
		}
		creatorGroups = gs
	}
	if !instanceAllowed && len(creatorGroups) == 0 {
		if aerr := s.record(r, "project.create", "projects", "denied", CodeForbidden, ""); aerr != nil {
			writeError(w, http.StatusInternalServerError, CodeInternal, "internal error")
			return
		}
		writeError(w, http.StatusForbidden, CodeForbidden, "access denied")
		return
	}

	var req createProjectRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Slug == "" {
		writeError(w, http.StatusBadRequest, CodeValidation, "slug is required")
		return
	}

	owner, err := s.resolveOwnerGroup(r, req.OwnerGroupID, instanceAllowed, creatorGroups)
	if err != nil {
		switch {
		case errors.Is(err, errAmbiguousCreatorGroup):
			// The caller IS authorized; they just belong to several creating
			// groups and must say which team owns this project. A 403 here
			// would misreport an authorization failure.
			writeError(w, http.StatusBadRequest, CodeValidation,
				"you belong to more than one group that can create projects — name one in owner_group_id")
			return
		case instanceAllowed:
			writeError(w, http.StatusBadRequest, CodeValidation, "unknown owner group")
			return
		default:
			// Deny-by-default and indistinguishable: an unknown group, a group
			// the caller is not in, and a group without the capability are the
			// same 403, so this is not a probe for which groups exist.
			if aerr := s.record(r, "project.create", "projects", "denied", CodeForbidden, ""); aerr != nil {
				writeError(w, http.StatusInternalServerError, CodeInternal, "internal error")
				return
			}
			writeError(w, http.StatusForbidden, CodeForbidden, "access denied")
			return
		}
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

// resolveOwnerGroup returns the group that will own a new project, or nil when
// none applies. creatorGroups is the caller's already-resolved set of groups
// that may create projects, so this makes no further membership query.
//
// Two callers, two rules:
//
//   - A DELEGATED creator must be a member of the named group and it must carry
//     the capability. That membership check is what stops someone planting
//     access into another team's group.
//   - An INSTANCE ADMIN may name ANY existing group. They already hold
//     member:manage everywhere and could bind that group to the project a
//     moment later, so this grants no authority they lack — and requiring them
//     to be a member of a team just to hand it a project was a defect.
//
// With no id supplied: an instance admin gets no owner group (the historical
// behaviour — no bindings are seeded); a delegated creator with exactly one
// creating group gets that one, and with several must name it rather than have
// us guess which team owns the project.
func (s *Server) resolveOwnerGroup(r *http.Request, groupID string, instanceAllowed bool, creatorGroups []*store.Group) (*store.Group, error) {
	if groupID == "" {
		if instanceAllowed {
			return nil, nil
		}
		switch len(creatorGroups) {
		case 1:
			return creatorGroups[0], nil
		case 0:
			return nil, errNoCreatorGroup // unreachable: the gate above rejects this
		default:
			return nil, errAmbiguousCreatorGroup
		}
	}
	for _, g := range creatorGroups {
		if g.ID == groupID {
			return g, nil
		}
	}
	if !instanceAllowed {
		// Not one of theirs. Indistinguishable from "no such group" on purpose.
		return nil, errNoCreatorGroup
	}
	g, err := s.groupsRepo().Get(r.Context(), groupID)
	if err != nil {
		return nil, errNoCreatorGroup
	}
	return g, nil
}

var (
	errNoCreatorGroup        = errors.New("api: no creator group")
	errAmbiguousCreatorGroup = errors.New("api: ambiguous creator group")
)

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
	if req.Owner != nil {
		if _, err := s.service.SetProjectOwner(r.Context(), pid, req.Owner); err != nil {
			s.writeServiceError(w, err)
			return
		}
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
