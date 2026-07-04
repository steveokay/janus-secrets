package api

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/steveokay/janus-secrets/internal/authz"
	"github.com/steveokay/janus-secrets/internal/store"
)

// scopeSpec identifies a role-binding scope drawn from the request path. level
// is "instance" | "project" | "environment"; projectID/envID are the binding's
// scope key (nil at instance); resource is the authz Resource (with the full
// chain filled in) used for inheritance-aware checks.
type scopeSpec struct {
	level     string
	projectID *string
	envID     *string
	resource  authz.Resource
}

func (s *Server) instanceScope() scopeSpec {
	return scopeSpec{level: "instance", resource: authz.Instance()}
}

func (s *Server) projectScope(r *http.Request) scopeSpec {
	pid := chi.URLParam(r, "pid")
	return scopeSpec{level: "project", projectID: &pid, resource: authz.Resource{ProjectID: pid}}
}

func (s *Server) envScope(r *http.Request) scopeSpec {
	pid := chi.URLParam(r, "pid")
	eid := chi.URLParam(r, "eid")
	return scopeSpec{level: "environment", envID: &eid, resource: authz.Resource{ProjectID: pid, EnvID: eid}}
}

func (s *Server) membersList(w http.ResponseWriter, r *http.Request, spec scopeSpec) {
	if err := s.can(r, authz.MemberRead, spec.resource); err != nil {
		s.writeAuthzError(w, err)
		return
	}
	scopeID := ""
	if spec.projectID != nil {
		scopeID = *spec.projectID
	}
	if spec.envID != nil {
		scopeID = *spec.envID
	}
	members, err := s.authz.ListMembers(r.Context(), spec.level, scopeID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, CodeInternal, "internal error")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"members": members})
}

type putMemberRequest struct {
	Role string `json:"role"`
}

func (s *Server) memberPut(w http.ResponseWriter, r *http.Request, spec scopeSpec, userID string) {
	var req putMemberRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || !authz.ValidRole(req.Role) {
		writeError(w, http.StatusBadRequest, CodeValidation, "a valid role is required")
		return
	}
	if err := s.can(r, authz.MemberManage, spec.resource); err != nil {
		s.writeAuthzError(w, err)
		return
	}
	// Delegation: you cannot grant a role above your own effective role here.
	granter, _ := PrincipalFrom(r.Context())
	gRole, err := s.authz.EffectiveRole(r.Context(), granter.ID, spec.resource)
	if err != nil {
		writeError(w, http.StatusInternalServerError, CodeInternal, "internal error")
		return
	}
	if !authz.RoleAtLeast(gRole, authz.Role(req.Role)) {
		writeError(w, http.StatusForbidden, CodeForbidden, "cannot grant a role above your own")
		return
	}
	// Subject must exist (the binding FK would otherwise 500).
	if _, err := store.NewUserRepo(s.st).Get(r.Context(), userID); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "not_found", "user not found")
			return
		}
		writeError(w, http.StatusInternalServerError, CodeInternal, "internal error")
		return
	}
	if err := s.authz.Grant(r.Context(), store.RoleBindingInput{
		SubjectUserID: userID,
		ScopeLevel:    spec.level,
		ProjectID:     spec.projectID,
		EnvironmentID: spec.envID,
		Role:          req.Role,
		CreatedBy:     &granter.ID,
	}); err != nil {
		writeError(w, http.StatusInternalServerError, CodeInternal, "internal error")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) memberDelete(w http.ResponseWriter, r *http.Request, spec scopeSpec, userID string) {
	if err := s.can(r, authz.MemberManage, spec.resource); err != nil {
		s.writeAuthzError(w, err)
		return
	}
	// Never-lock-out: refuse to remove the last instance owner.
	if spec.level == "instance" {
		if n, err := s.authz.CountInstanceOwners(r.Context()); err == nil && n <= 1 {
			members, _ := s.authz.ListMembers(r.Context(), "instance", "")
			for _, m := range members {
				if m.UserID == userID && m.Role == string(authz.RoleOwner) {
					writeError(w, http.StatusConflict, CodeValidation, "cannot remove the last instance owner")
					return
				}
			}
		}
	}
	if err := s.authz.Revoke(r.Context(), userID, spec.level, spec.projectID, spec.envID); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "not_found", "binding not found")
			return
		}
		writeError(w, http.StatusInternalServerError, CodeInternal, "internal error")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
