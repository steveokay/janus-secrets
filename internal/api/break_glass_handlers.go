package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/steveokay/janus-secrets/internal/authz"
	"github.com/steveokay/janus-secrets/internal/store"
)

// maxBreakGlassReasonLen bounds the operator-entered justification. The reason
// is non-secret text; the bound keeps a single audit/notification payload sane.
const maxBreakGlassReasonLen = 1000

type breakGlassActivateReq struct {
	ScopeLevel    string `json:"scope_level"`    // instance | project | environment
	ProjectID     string `json:"project_id"`     // required for scope_level=project
	EnvironmentID string `json:"environment_id"` // required for scope_level=environment
	Role          string `json:"role"`           // target elevated role (must exceed held role, ≤ owner)
	Reason        string `json:"reason"`         // mandatory, non-empty
	TTL           string `json:"ttl"`            // Go duration (e.g. "30m"); clamped to the server max
}

// breakGlassGrantView is the value-safe JSON rendering of a grant.
type breakGlassGrantView struct {
	ID            string     `json:"id"`
	UserID        string     `json:"user_id"`
	ScopeLevel    string     `json:"scope_level"`
	ProjectID     *string    `json:"project_id,omitempty"`
	EnvironmentID *string    `json:"environment_id,omitempty"`
	ElevatedRole  string     `json:"elevated_role"`
	Reason        string     `json:"reason"`
	ActivatedAt   time.Time  `json:"activated_at"`
	ExpiresAt     time.Time  `json:"expires_at"`
	RevokedAt     *time.Time `json:"revoked_at,omitempty"`
}

func breakGlassView(g *store.BreakGlassGrant) breakGlassGrantView {
	return breakGlassGrantView{
		ID: g.ID, UserID: g.UserID, ScopeLevel: g.ScopeLevel,
		ProjectID: g.ProjectID, EnvironmentID: g.EnvironmentID,
		ElevatedRole: g.ElevatedRole, Reason: g.Reason,
		ActivatedAt: g.ActivatedAt, ExpiresAt: g.ExpiresAt, RevokedAt: g.RevokedAt,
	}
}

// breakGlassScope resolves the request's scope into the authz Resource (full
// parent chain) plus the store scope columns. Instance → empty resource; project
// / environment resolve (and thereby validate the existence of) the target so a
// grant can never be minted against a non-existent or mis-parented scope.
func (s *Server) breakGlassScope(ctx context.Context, req breakGlassActivateReq) (authz.Resource, *string, *string, error) {
	switch req.ScopeLevel {
	case "instance":
		return authz.Instance(), nil, nil, nil
	case "project":
		if req.ProjectID == "" {
			return authz.Resource{}, nil, nil, errBreakGlassValidation
		}
		if _, err := store.NewProjectRepo(s.st).Get(ctx, req.ProjectID); err != nil {
			return authz.Resource{}, nil, nil, err
		}
		pid := req.ProjectID
		return authz.Resource{ProjectID: pid}, &pid, nil, nil
	case "environment":
		if req.EnvironmentID == "" {
			return authz.Resource{}, nil, nil, errBreakGlassValidation
		}
		res, err := s.resolveScopeResource(ctx, "environment", req.EnvironmentID)
		if err != nil {
			return authz.Resource{}, nil, nil, err
		}
		eid := req.EnvironmentID
		return res, nil, &eid, nil
	default:
		return authz.Resource{}, nil, nil, errBreakGlassValidation
	}
}

var errBreakGlassValidation = errors.New("api: break-glass validation")

func (s *Server) handleBreakGlassActivate(w http.ResponseWriter, r *http.Request) {
	p, _ := PrincipalFrom(r.Context())
	// Only human users can hold a role binding to elevate from; service tokens
	// have no bound role and must not activate break-glass.
	if p.Kind != "user" {
		if aerr := s.record(r, "breakglass.activate", "break-glass", "denied", CodeForbidden, ""); aerr != nil {
			writeError(w, http.StatusInternalServerError, CodeInternal, "internal error")
			return
		}
		writeError(w, http.StatusForbidden, CodeForbidden, "break-glass is available to user accounts only")
		return
	}

	var req breakGlassActivateReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, CodeValidation, "invalid request body")
		return
	}
	req.Reason = strings.TrimSpace(req.Reason)
	if req.Reason == "" {
		writeError(w, http.StatusBadRequest, CodeValidation, "reason is required")
		return
	}
	if len(req.Reason) > maxBreakGlassReasonLen {
		writeError(w, http.StatusBadRequest, CodeValidation, "reason is too long")
		return
	}
	target := authz.Role(req.Role)
	if !authz.ValidRole(req.Role) {
		writeError(w, http.StatusBadRequest, CodeValidation, "role must be one of viewer, developer, admin, owner")
		return
	}
	// Target can never exceed owner (ValidRole already caps this, but be explicit).
	if authz.RoleStrictlyAbove(target, authz.RoleOwner) {
		writeError(w, http.StatusBadRequest, CodeValidation, "role may not exceed owner")
		return
	}

	// Clamp the TTL. An absent/invalid/non-positive TTL uses the server max.
	ttl := s.breakGlassMaxTTL
	if req.TTL != "" {
		d, err := time.ParseDuration(req.TTL)
		if err != nil || d <= 0 {
			writeError(w, http.StatusBadRequest, CodeValidation, "ttl must be a positive Go duration like 30m")
			return
		}
		if d < ttl {
			ttl = d
		}
	}

	res, projectID, envID, err := s.breakGlassScope(r.Context(), req)
	if err != nil {
		if errors.Is(err, errBreakGlassValidation) {
			writeError(w, http.StatusBadRequest, CodeValidation, "invalid scope")
			return
		}
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "not_found", "scope not found")
			return
		}
		s.writeServiceError(w, err)
		return
	}

	// GUARD (deny-by-default): the user must ALREADY hold a role on this exact
	// scope (bound role, excluding any existing grant), and the requested role
	// must be STRICTLY higher than the held role. No base binding → 403, so
	// nobody elevates into a scope they cannot already see.
	held, err := s.authz.BoundRole(r.Context(), p.ID, res)
	if err != nil {
		s.writeAuthzError(w, err)
		return
	}
	if held == "" {
		if aerr := s.record(r, "breakglass.activate", breakGlassAuditResource(req), "denied", CodeForbidden, ""); aerr != nil {
			writeError(w, http.StatusInternalServerError, CodeInternal, "internal error")
			return
		}
		writeError(w, http.StatusForbidden, CodeForbidden, "you must already hold a role on this scope to activate break-glass")
		return
	}
	if !authz.RoleStrictlyAbove(target, held) {
		writeError(w, http.StatusBadRequest, CodeValidation,
			fmt.Sprintf("break-glass must raise your role above %q on this scope", held))
		return
	}

	grant, err := s.breakGlass.Create(r.Context(), store.BreakGlassGrantInput{
		UserID:        p.ID,
		ScopeLevel:    req.ScopeLevel,
		ProjectID:     projectID,
		EnvironmentID: envID,
		ElevatedRole:  req.Role,
		Reason:        req.Reason,
		ExpiresAt:     time.Now().Add(ttl),
	})
	if err != nil {
		s.writeServiceError(w, err)
		return
	}

	// LOUD, fail-closed audit: if the audit write fails, revoke the just-created
	// grant so no unaudited elevation persists, then 500.
	detail := fmt.Sprintf("role=%s expires_at=%s reason=%s",
		grant.ElevatedRole, grant.ExpiresAt.UTC().Format(time.RFC3339), grant.Reason)
	if aerr := s.record(r, "breakglass.activate", breakGlassAuditResource(req), "success", "", detail); aerr != nil {
		_ = s.breakGlass.Revoke(r.Context(), grant.ID, time.Now())
		writeError(w, http.StatusInternalServerError, CodeInternal, "internal error")
		return
	}

	writeJSON(w, http.StatusCreated, breakGlassView(grant))
}

// breakGlassAuditResource renders a stable, value-free resource path for the
// audit event, e.g. "break-glass/project/<pid>".
func breakGlassAuditResource(req breakGlassActivateReq) string {
	switch req.ScopeLevel {
	case "project":
		return "break-glass/project/" + req.ProjectID
	case "environment":
		return "break-glass/environment/" + req.EnvironmentID
	default:
		return "break-glass/instance"
	}
}

func (s *Server) handleBreakGlassList(w http.ResponseWriter, r *http.Request) {
	p, _ := PrincipalFrom(r.Context())
	now := time.Now()

	// Admins (instance member:manage) see every active grant; everyone else sees
	// only their own. This is a value-safe metadata listing.
	var grants []*store.BreakGlassGrant
	var err error
	if s.can(r, authz.MemberManage, authz.Instance()) == nil {
		grants, err = s.breakGlass.ListActive(r.Context(), now)
	} else {
		grants, err = s.breakGlass.ListActiveForUserOrdered(r.Context(), p.ID, now)
	}
	if err != nil {
		s.writeServiceError(w, err)
		return
	}
	views := make([]breakGlassGrantView, 0, len(grants))
	for _, g := range grants {
		views = append(views, breakGlassView(g))
	}
	writeJSON(w, http.StatusOK, map[string]any{"grants": views})
}

func (s *Server) handleBreakGlassRevoke(w http.ResponseWriter, r *http.Request) {
	p, _ := PrincipalFrom(r.Context())
	id := chi.URLParam(r, "id")

	grant, err := s.breakGlass.Get(r.Context(), id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "not_found", "break-glass grant not found")
			return
		}
		s.writeServiceError(w, err)
		return
	}
	// Self OR an instance admin (member:manage) may end a grant early.
	isAdmin := s.can(r, authz.MemberManage, authz.Instance()) == nil
	if grant.UserID != p.ID && !isAdmin {
		if aerr := s.record(r, "breakglass.revoke", "break-glass/"+id, "denied", CodeForbidden, ""); aerr != nil {
			writeError(w, http.StatusInternalServerError, CodeInternal, "internal error")
			return
		}
		writeError(w, http.StatusForbidden, CodeForbidden, "access denied")
		return
	}

	if err := s.breakGlass.Revoke(r.Context(), id, time.Now()); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			// Already revoked/expired — nothing live to end.
			writeError(w, http.StatusConflict, "conflict", "grant is not active")
			return
		}
		s.writeServiceError(w, err)
		return
	}
	if aerr := s.record(r, "breakglass.revoke", "break-glass/"+id, "success", "", "role="+grant.ElevatedRole); aerr != nil {
		writeError(w, http.StatusInternalServerError, CodeInternal, "internal error")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
