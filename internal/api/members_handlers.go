package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/steveokay/janus-secrets/internal/authz"
	"github.com/steveokay/janus-secrets/internal/store"
)

// isLastInstanceOwner reports whether userID is currently the sole instance
// owner — the subject of the never-lock-out guards (remove/disable/demote). It
// fails closed: any store error is returned so callers reject the mutation
// rather than proceed on incomplete information.
func (s *Server) isLastInstanceOwner(ctx context.Context, userID string) (bool, error) {
	n, err := s.authz.CountInstanceOwners(ctx)
	if err != nil {
		return false, err
	}
	if n > 1 {
		return false, nil
	}
	members, err := s.authz.ListMembers(ctx, "instance", "")
	if err != nil {
		return false, err
	}
	for _, m := range members {
		if m.UserID == userID && m.Role == string(authz.RoleOwner) {
			return true, nil
		}
	}
	return false, nil
}

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

// envScope resolves the environment-members scope from the target env id's real
// parent chain (via resolveScopeResource), never from the path pid, so a caller
// cannot manage members of another project's environment by putting its id under
// a pid they control. Returns store.ErrNotFound if the environment is missing.
func (s *Server) envScope(r *http.Request) (scopeSpec, error) {
	eid := chi.URLParam(r, "eid")
	res, err := s.resolveScopeResource(r.Context(), "environment", eid)
	if err != nil {
		return scopeSpec{}, err
	}
	return scopeSpec{level: "environment", envID: &eid, resource: res}, nil
}

func (s *Server) membersList(w http.ResponseWriter, r *http.Request, spec scopeSpec) {
	if err := s.can(r, authz.MemberRead, spec.resource); err != nil {
		s.writeAuthzError(w, err)
		return
	}
	pp, err := parsePageParams(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, CodeValidation, err.Error())
		return
	}
	scopeID := ""
	if spec.projectID != nil {
		scopeID = *spec.projectID
	}
	if spec.envID != nil {
		scopeID = *spec.envID
	}
	members, next, err := s.authz.ListMembersPage(r.Context(), spec.level, scopeID, pp.limit, pp.after)
	if err != nil {
		writeError(w, http.StatusInternalServerError, CodeInternal, "internal error")
		return
	}
	var nextTok *string
	if next != nil {
		t := encodeCursor(next.CreatedAt, next.ID)
		nextTok = &t
	}
	writeJSON(w, http.StatusOK, map[string]any{"members": members, "next_cursor": nextTok})
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
	if !s.authorize(w, r, authz.MemberManage, spec.resource, "member.grant", memberResource(spec, userID)) {
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
	// Never-lock-out: an in-place upsert must not demote the last instance owner
	// to a lesser role (the DELETE path is guarded the same way). Without this an
	// admin could demote the sole owner and — since reconcileInstanceOwner
	// regrants owner to the oldest user on the next boot — escalate to owner.
	if spec.level == "instance" && req.Role != string(authz.RoleOwner) {
		last, err := s.isLastInstanceOwner(r.Context(), userID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, CodeInternal, "internal error")
			return
		}
		if last {
			writeError(w, http.StatusConflict, CodeValidation, "cannot demote the last instance owner")
			return
		}
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
	if err := s.record(r, "member.grant", memberResource(spec, userID), "success", "", "role="+req.Role); err != nil {
		writeError(w, http.StatusInternalServerError, CodeInternal, "internal error")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) memberDelete(w http.ResponseWriter, r *http.Request, spec scopeSpec, userID string) {
	if !s.authorize(w, r, authz.MemberManage, spec.resource, "member.revoke", memberResource(spec, userID)) {
		return
	}
	// Never-lock-out: refuse to remove the last instance owner (fail closed).
	if spec.level == "instance" {
		last, err := s.isLastInstanceOwner(r.Context(), userID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, CodeInternal, "internal error")
			return
		}
		if last {
			writeError(w, http.StatusConflict, CodeValidation, "cannot remove the last instance owner")
			return
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
	if err := s.record(r, "member.revoke", memberResource(spec, userID), "success", "", ""); err != nil {
		writeError(w, http.StatusInternalServerError, CodeInternal, "internal error")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// memberResource renders a scope-qualified member path for audit resources,
// e.g. "instance/members/<uid>", "project/<pid>/members/<uid>".
func memberResource(spec scopeSpec, userID string) string {
	switch spec.level {
	case "project":
		return "project/" + deref(spec.projectID) + "/members/" + userID
	case "environment":
		return "environment/" + deref(spec.envID) + "/members/" + userID
	default:
		return "instance/members/" + userID
	}
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
