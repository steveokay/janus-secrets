package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/steveokay/janus-secrets/internal/audit"
	"github.com/steveokay/janus-secrets/internal/auth"
	"github.com/steveokay/janus-secrets/internal/authz"
	"github.com/steveokay/janus-secrets/internal/store"
)

// maxGroupNameLen / maxGroupDescLen bound operator-supplied text at the API
// boundary. Group metadata is display material, never secret material.
const (
	maxGroupNameLen  = 128
	maxGroupDescLen  = 512
	maxClaimValueLen = 256
)

type groupView struct {
	ID           string  `json:"id"`
	Name         string  `json:"name"`
	Kind         string  `json:"kind"`
	ClaimValue   *string `json:"claim_value"`
	Description  string  `json:"description"`
	MemberCount  int     `json:"member_count"`
	BindingCount int     `json:"binding_count"`
	CreatedAt    string  `json:"created_at"`
}

func toGroupView(g *store.Group) groupView {
	return groupView{
		ID: g.ID, Name: g.Name, Kind: g.Kind, ClaimValue: g.ClaimValue,
		Description: g.Description, MemberCount: g.MemberCount,
		BindingCount: g.BindingCount, CreatedAt: g.CreatedAt.UTC().Format(time.RFC3339),
	}
}

// groupsRepo / groupBindingsRepo are built per request like the other handlers'
// repositories (the Server holds the pool, not the repos).
func (s *Server) groupsRepo() *store.GroupRepo { return store.NewGroupRepo(s.st) }

func (s *Server) groupBindingsRepo() *store.GroupBindingRepo {
	return store.NewGroupBindingRepo(s.st)
}

// handleGroupList: authz enforced by requireInstance. Read — not audited.
func (s *Server) handleGroupList(w http.ResponseWriter, r *http.Request) {
	pp, err := parsePageParams(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, CodeValidation, err.Error())
		return
	}
	gs, err := s.groupsRepo().List(r.Context(), pp.limit, pp.after)
	if err != nil {
		writeError(w, http.StatusInternalServerError, CodeInternal, "internal error")
		return
	}
	out := make([]groupView, 0, len(gs))
	for _, g := range gs {
		out = append(out, toGroupView(g))
	}
	var nextTok *string
	if pp.limit > 0 && len(gs) == pp.limit {
		last := gs[len(gs)-1]
		t := encodeCursor(last.CreatedAt, last.ID)
		nextTok = &t
	}
	writeJSON(w, http.StatusOK, map[string]any{"groups": out, "next_cursor": nextTok})
}

type createGroupRequest struct {
	Name        string `json:"name"`
	Kind        string `json:"kind"`
	ClaimValue  string `json:"claim_value"`
	Description string `json:"description"`
}

// handleGroupCreate: authz enforced by requireInstance (group:manage).
func (s *Server) handleGroupCreate(w http.ResponseWriter, r *http.Request) {
	var req createGroupRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, CodeValidation, "invalid body")
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	req.ClaimValue = strings.TrimSpace(req.ClaimValue)
	if req.Name == "" || len(req.Name) > maxGroupNameLen || len(req.Description) > maxGroupDescLen {
		writeError(w, http.StatusBadRequest, CodeValidation, "a name of 1-128 characters is required")
		return
	}
	in := store.GroupInput{Name: req.Name, Kind: req.Kind, Description: req.Description}
	switch req.Kind {
	case store.GroupKindLocal:
		// A local group has no claim value: membership is the explicit list.
		if req.ClaimValue != "" {
			writeError(w, http.StatusBadRequest, CodeValidation, "a local group cannot have a claim value")
			return
		}
	case store.GroupKindOIDC:
		// An empty claim value would match a group nothing can ever assert, so
		// require it rather than storing a group that silently never applies.
		if req.ClaimValue == "" || len(req.ClaimValue) > maxClaimValueLen {
			writeError(w, http.StatusBadRequest, CodeValidation, "an oidc group requires a claim value")
			return
		}
		in.ClaimValue = &req.ClaimValue
	default:
		writeError(w, http.StatusBadRequest, CodeValidation, "kind must be 'oidc' or 'local'")
		return
	}
	p, _ := PrincipalFrom(r.Context())
	in.CreatedBy = &p.ID

	g, err := s.groupsRepo().Create(r.Context(), in)
	if err != nil {
		if errors.Is(err, store.ErrAlreadyExists) {
			writeError(w, http.StatusConflict, CodeValidation, "a group with that name or claim value already exists")
			return
		}
		writeError(w, http.StatusInternalServerError, CodeInternal, "internal error")
		return
	}
	if err := s.record(r, "group.create", "groups/"+g.ID, "success", "",
		"name="+g.Name+" kind="+g.Kind); err != nil {
		writeError(w, http.StatusInternalServerError, CodeInternal, "internal error")
		return
	}
	writeJSON(w, http.StatusCreated, toGroupView(g))
}

// handleGroupGet returns a group with the scopes it grants access at — the
// question an admin needs answered before deleting one.
func (s *Server) handleGroupGet(w http.ResponseWriter, r *http.Request) {
	gid := chi.URLParam(r, "gid")
	g, err := s.groupsRepo().Get(r.Context(), gid)
	if err != nil {
		s.writeGroupStoreError(w, err)
		return
	}
	bs, err := s.groupBindingsRepo().ListForGroup(r.Context(), gid)
	if err != nil {
		writeError(w, http.StatusInternalServerError, CodeInternal, "internal error")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"group":    toGroupView(g),
		"bindings": toGroupBindingViews(bs),
	})
}

// handleGroupDelete removes a group; membership and bindings cascade, so every
// access it conferred is gone on the next request.
func (s *Server) handleGroupDelete(w http.ResponseWriter, r *http.Request) {
	gid := chi.URLParam(r, "gid")
	if err := s.groupsRepo().Delete(r.Context(), gid); err != nil {
		s.writeGroupStoreError(w, err)
		return
	}
	if err := s.record(r, "group.delete", "groups/"+gid, "success", "", ""); err != nil {
		writeError(w, http.StatusInternalServerError, CodeInternal, "internal error")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleGroupMemberList lists a group's members. For an OIDC group this is the
// snapshot from logins, so it only ever covers users who have signed in.
func (s *Server) handleGroupMemberList(w http.ResponseWriter, r *http.Request) {
	gid := chi.URLParam(r, "gid")
	pp, err := parsePageParams(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, CodeValidation, err.Error())
		return
	}
	ms, err := s.groupsRepo().ListMembers(r.Context(), gid, pp.limit, pp.after)
	if err != nil {
		writeError(w, http.StatusInternalServerError, CodeInternal, "internal error")
		return
	}
	out := make([]map[string]any, 0, len(ms))
	for _, m := range ms {
		out = append(out, map[string]any{
			"user_id":    m.UserID,
			"created_at": m.CreatedAt.UTC().Format(time.RFC3339),
		})
	}
	var nextTok *string
	if pp.limit > 0 && len(ms) == pp.limit {
		last := ms[len(ms)-1]
		t := encodeCursor(last.CreatedAt, last.UserID)
		nextTok = &t
	}
	writeJSON(w, http.StatusOK, map[string]any{"members": out, "next_cursor": nextTok})
}

// handleGroupMemberPut adds a user to a LOCAL group.
//
// The OIDC rejection here is a clear error message, not the security control:
// the schema's composite FK makes a hand-added member of an IdP-fed group
// unrepresentable, which is what preserves "access granted via an IdP group is
// fully described by the IdP".
func (s *Server) handleGroupMemberPut(w http.ResponseWriter, r *http.Request) {
	gid, uid := chi.URLParam(r, "gid"), chi.URLParam(r, "uid")
	g, err := s.groupsRepo().Get(r.Context(), gid)
	if err != nil {
		s.writeGroupStoreError(w, err)
		return
	}
	if g.Kind != store.GroupKindLocal {
		writeError(w, http.StatusConflict, CodeValidation,
			"membership of an oidc group comes from the identity provider")
		return
	}
	if _, err := store.NewUserRepo(s.st).Get(r.Context(), uid); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "not_found", "user not found")
			return
		}
		writeError(w, http.StatusInternalServerError, CodeInternal, "internal error")
		return
	}
	p, _ := PrincipalFrom(r.Context())
	if err := s.groupsRepo().AddMember(r.Context(), gid, g.Kind, uid, &p.ID); err != nil {
		writeError(w, http.StatusInternalServerError, CodeInternal, "internal error")
		return
	}
	if err := s.record(r, "group.member.add", "groups/"+gid+"/members/"+uid, "success", "",
		"group="+g.Name); err != nil {
		writeError(w, http.StatusInternalServerError, CodeInternal, "internal error")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleGroupMemberDelete removes a user from a local group.
func (s *Server) handleGroupMemberDelete(w http.ResponseWriter, r *http.Request) {
	gid, uid := chi.URLParam(r, "gid"), chi.URLParam(r, "uid")
	g, err := s.groupsRepo().Get(r.Context(), gid)
	if err != nil {
		s.writeGroupStoreError(w, err)
		return
	}
	if g.Kind != store.GroupKindLocal {
		writeError(w, http.StatusConflict, CodeValidation,
			"membership of an oidc group comes from the identity provider")
		return
	}
	if err := s.groupsRepo().RemoveMember(r.Context(), gid, uid); err != nil {
		s.writeGroupStoreError(w, err)
		return
	}
	if err := s.record(r, "group.member.remove", "groups/"+gid+"/members/"+uid, "success", "",
		"group="+g.Name); err != nil {
		writeError(w, http.StatusInternalServerError, CodeInternal, "internal error")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// writeGroupStoreError maps a store error for the group routes.
func (s *Server) writeGroupStoreError(w http.ResponseWriter, err error) {
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "not_found", "not found")
		return
	}
	writeError(w, http.StatusInternalServerError, CodeInternal, "internal error")
}

// recordGroupSync writes the value-free group.sync audit event for an OIDC
// login. Best-effort by design: the login has already succeeded and its own
// auth.login event is fail-closed, so failing the request here would log a user
// out over a bookkeeping write. Nothing is emitted for an unchanged membership,
// which keeps routine logins out of the ledger.
func (s *Server) recordGroupSync(r *http.Request, p auth.Principal, outcome *auth.OIDCGroupOutcome) {
	if outcome == nil {
		return
	}
	actor := audit.Actor{Kind: string(auth.KindUser), ID: p.ID, Name: p.Name}
	if outcome.Overflow {
		// Loud on purpose: membership silently stopped tracking the IdP and is
		// now stale without bound until the operator fixes the IdP-side group
		// filter.
		_ = s.recordActor(r, actor, "group.sync", "auth/oidc/groups", "success",
			"", "status=overage")
		return
	}
	if outcome.Synced == nil {
		return
	}
	detail := "added=" + strings.Join(outcome.Synced.Added, ",") +
		" removed=" + strings.Join(outcome.Synced.Removed, ",")
	_ = s.recordActor(r, actor, "group.sync", "auth/oidc/groups", "success", "", detail)
}

// ---- group role bindings (scope-side) ----

type groupBindingView struct {
	GroupID   string `json:"group_id"`
	GroupName string `json:"group_name,omitempty"`
	GroupKind string `json:"group_kind,omitempty"`
	// ScopeLevel plus the scope key. The ids are carried so the group detail
	// view can name WHICH project or environment a grant reaches — without them
	// a group bound on three projects renders as three identical rows.
	ScopeLevel    string  `json:"scope_level"`
	ProjectID     *string `json:"project_id,omitempty"`
	EnvironmentID *string `json:"environment_id,omitempty"`
	Role          string  `json:"role"`
	CreatedAt     string  `json:"created_at"`
}

func toGroupBindingViews(bs []*store.GroupRoleBinding) []groupBindingView {
	out := make([]groupBindingView, 0, len(bs))
	for _, b := range bs {
		out = append(out, groupBindingView{
			GroupID: b.GroupID, GroupName: b.GroupName, GroupKind: b.GroupKind,
			ScopeLevel: b.ScopeLevel, ProjectID: b.ProjectID, EnvironmentID: b.EnvironmentID,
			Role:      b.Role,
			CreatedAt: b.CreatedAt.UTC().Format(time.RFC3339),
		})
	}
	return out
}

// groupMembersList returns the group bindings at a scope (the Groups section of
// a members screen). Gated on member:read at the scope, like the user list.
func (s *Server) groupMembersList(w http.ResponseWriter, r *http.Request, spec scopeSpec) {
	if err := s.can(r, authz.MemberRead, spec.resource); err != nil {
		s.writeAuthzError(w, err)
		return
	}
	pp, err := parsePageParams(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, CodeValidation, err.Error())
		return
	}
	scopeID := scopeIDOf(spec)
	bs, err := s.groupBindingsRepo().ListForScopePage(r.Context(), spec.level, scopeID, pp.limit, pp.after)
	if err != nil {
		writeError(w, http.StatusInternalServerError, CodeInternal, "internal error")
		return
	}
	var nextTok *string
	if pp.limit > 0 && len(bs) == pp.limit {
		last := bs[len(bs)-1]
		t := encodeCursor(last.CreatedAt, last.ID)
		nextTok = &t
	}
	// Resolve who actually reaches this scope through those groups. This rides
	// member:read at the scope on purpose: a project admin may see who has
	// access to their project, but listing a group's members is instance
	// `group:manage`, so without this they cannot answer "who can write here?"
	// at all. Fetch one extra row to detect truncation honestly rather than
	// silently returning a partial answer that reads as complete.
	derived, err := s.groupBindingsRepo().DerivedMembersForScope(r.Context(), spec.level, scopeID, maxDerivedMembers+1)
	if err != nil {
		writeError(w, http.StatusInternalServerError, CodeInternal, "internal error")
		return
	}
	truncated := len(derived) > maxDerivedMembers
	if truncated {
		derived = derived[:maxDerivedMembers]
	}
	out := make([]derivedMemberView, 0, len(derived))
	for _, d := range derived {
		out = append(out, derivedMemberView{
			UserID: d.UserID, Role: d.Role, ViaGroupID: d.GroupID, ViaGroupName: d.GroupName,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"bindings":          toGroupBindingViews(bs),
		"derived_members":   out,
		"derived_truncated": truncated,
		"next_cursor":       nextTok,
	})
}

// maxDerivedMembers bounds the resolved (user, group) pairs one scope listing
// returns. Truncation is reported, never silent — a members screen that quietly
// dropped rows would understate who has access, which is the exact failure this
// endpoint exists to prevent.
const maxDerivedMembers = 2000

type derivedMemberView struct {
	UserID       string `json:"user_id"`
	Role         string `json:"role"`
	ViaGroupID   string `json:"via_group_id"`
	ViaGroupName string `json:"via_group_name"`
}

// groupMemberPut binds a group to a scope at a role.
//
// Two guards mirror memberPut exactly, and for the same reasons:
//   - the delegation cap uses BoundRole, NOT EffectiveRole, so a break-glass
//     elevation cannot be laundered into a durable grant (M-1). Without this,
//     group bindings would be a way around that fix.
//   - the role is capped at admin. Owner rotates the master key, prunes the
//     audit chain and destroys secret history; it must be a deliberate act
//     recorded here, not a group membership an external directory controls.
func (s *Server) groupMemberPut(w http.ResponseWriter, r *http.Request, spec scopeSpec, groupID string) {
	var req putMemberRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || !authz.ValidRole(req.Role) {
		writeError(w, http.StatusBadRequest, CodeValidation, "a valid role is required")
		return
	}
	if req.Role == string(authz.RoleOwner) {
		writeError(w, http.StatusBadRequest, CodeValidation,
			"a group cannot grant owner; bind an owner directly")
		return
	}
	if !s.authorize(w, r, authz.MemberManage, spec.resource, "group.binding.grant", groupBindingResource(spec, groupID)) {
		return
	}
	granter, _ := PrincipalFrom(r.Context())
	gRole, err := s.authz.BoundRole(r.Context(), granter.ID, spec.resource)
	if err != nil {
		writeError(w, http.StatusInternalServerError, CodeInternal, "internal error")
		return
	}
	if !authz.RoleAtLeast(gRole, authz.Role(req.Role)) {
		writeError(w, http.StatusForbidden, CodeForbidden, "cannot grant a role above your own")
		return
	}
	// Subject must exist (the binding FK would otherwise 500).
	g, err := s.groupsRepo().Get(r.Context(), groupID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "not_found", "group not found")
			return
		}
		writeError(w, http.StatusInternalServerError, CodeInternal, "internal error")
		return
	}
	if _, err := s.groupBindingsRepo().Create(r.Context(), store.GroupRoleBindingInput{
		GroupID:       groupID,
		ScopeLevel:    spec.level,
		ProjectID:     spec.projectID,
		EnvironmentID: spec.envID,
		Role:          req.Role,
		CreatedBy:     &granter.ID,
	}); err != nil {
		writeError(w, http.StatusInternalServerError, CodeInternal, "internal error")
		return
	}
	if err := s.record(r, "group.binding.grant", groupBindingResource(spec, groupID), "success", "",
		"group="+g.Name+" role="+req.Role); err != nil {
		writeError(w, http.StatusInternalServerError, CodeInternal, "internal error")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// groupMemberDelete unbinds a group from a scope. There is no last-owner guard
// to run: a group binding can never be owner, so it can never be the binding
// that keeps the instance reachable.
func (s *Server) groupMemberDelete(w http.ResponseWriter, r *http.Request, spec scopeSpec, groupID string) {
	if !s.authorize(w, r, authz.MemberManage, spec.resource, "group.binding.revoke", groupBindingResource(spec, groupID)) {
		return
	}
	if err := s.groupBindingsRepo().DeleteForGroupScope(r.Context(), groupID, spec.level, spec.projectID, spec.envID); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "not_found", "binding not found")
			return
		}
		writeError(w, http.StatusInternalServerError, CodeInternal, "internal error")
		return
	}
	if err := s.record(r, "group.binding.revoke", groupBindingResource(spec, groupID), "success", "", ""); err != nil {
		writeError(w, http.StatusInternalServerError, CodeInternal, "internal error")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// scopeIDOf returns the project or environment id a scope keys on ("" at
// instance level).
func scopeIDOf(spec scopeSpec) string {
	if spec.projectID != nil {
		return *spec.projectID
	}
	if spec.envID != nil {
		return *spec.envID
	}
	return ""
}

// groupBindingResource renders a scope-qualified audit path, mirroring
// memberResource.
func groupBindingResource(spec scopeSpec, groupID string) string {
	switch spec.level {
	case "project":
		return "project/" + deref(spec.projectID) + "/group-members/" + groupID
	case "environment":
		return "environment/" + deref(spec.envID) + "/group-members/" + groupID
	default:
		return "instance/group-members/" + groupID
	}
}
