package api

import (
	"errors"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/steveokay/janus-secrets/internal/authz"
	"github.com/steveokay/janus-secrets/internal/store"
)

// Bounds on one access review. Every one of them is REPORTED when it bites (see
// accessTruncation): a review that quietly dropped rows would understate who
// has access, and an incomplete access review that reads as complete is worse
// than no review at all — it gets signed off.
const (
	maxAccessProjects = 200
	maxAccessEnvs     = 1000
	maxAccessBindings = 5000
	maxAccessUsers    = 500
	maxAccessCells    = 20000
)

// accessTruncation says, structurally, which parts of the answer were cut. The
// codebase is consistent about this (`derived_truncated`, `values_compared`,
// `scoped`/`scope_projects`); this is the same contract for a bigger answer.
type accessTruncation struct {
	Projects     bool `json:"projects"`
	Environments bool `json:"environments"`
	Bindings     bool `json:"bindings"`
	Users        bool `json:"users"`
	Cells        bool `json:"cells"`
}

// Any reports whether anything at all was cut.
func (t accessTruncation) Any() bool {
	return t.Projects || t.Environments || t.Bindings || t.Users || t.Cells
}

// accessView is the set of scopes ONE caller may look at, resolved once per
// request. It is the authorization boundary for everything in this file: the
// store filter is built from it and never widened, so a principal with no
// bindings sees nothing — the same deny-by-default handleProjectList gets from
// its per-item check, but computed in a fixed number of queries.
type accessView struct {
	// instance is true when the caller holds the action against the
	// instance-scoped resource. When false, instance-level bindings are left
	// OUT of the answer entirely — they are a scope the caller cannot see, and
	// showing them would leak the shape of instance membership to a project
	// admin. The response says so (`instance_visible`) rather than pretending
	// the picture is complete.
	instance bool
	projects []*store.Project
	envs     []*store.Environment
	trunc    accessTruncation
}

func (v *accessView) projectIDs() []string {
	out := make([]string, 0, len(v.projects))
	for _, p := range v.projects {
		out = append(out, p.ID)
	}
	return out
}

func (v *accessView) envIDs() []string {
	out := make([]string, 0, len(v.envs))
	for _, e := range v.envs {
		out = append(out, e.ID)
	}
	return out
}

// filter renders the view as the store's scope filter (optionally narrowed to
// one subject).
func (v *accessView) filter(userID string) store.AccessScopeFilter {
	return store.AccessScopeFilter{
		SubjectUserID: userID,
		Instance:      v.instance,
		ProjectIDs:    v.projectIDs(),
		EnvIDs:        v.envIDs(),
	}
}

// resolveAccessView computes the scopes a caller may review under `action`,
// optionally narrowed to one project by the `project` query parameter.
//
// The batch shape is the whole point, and it is copied from authorizeAuditRead:
// ONE binding+group+grant load for the whole set via AllowedProjects, rather
// than s.can per project — which re-queries direct bindings, group bindings and
// break-glass on every iteration and turns one page into thousands of queries
// on a large instance.
//
// Returns (view, true) or writes the response and returns (nil, false). Denials
// are audited like any other.
func (s *Server) resolveAccessView(w http.ResponseWriter, r *http.Request, action authz.Action, auditAction string) (*accessView, bool) {
	v := &accessView{instance: s.can(r, action, authz.Instance()) == nil}

	all, err := store.NewProjectRepo(s.st).ListPage(r.Context(), 0, nil)
	if err != nil {
		writeError(w, http.StatusInternalServerError, CodeInternal, "internal error")
		return nil, false
	}
	byID := make(map[string]*store.Project, len(all))
	ids := make([]string, 0, len(all))
	for _, p := range all {
		byID[p.ID] = p
		ids = append(ids, p.ID)
	}

	var allowed []string
	if v.instance {
		// An instance binding applies to every resource (bindingApplies returns
		// true for it), so there is nothing to filter and nothing to re-check.
		allowed = ids
	} else {
		principal, _ := PrincipalFrom(r.Context())
		allowed, err = s.authz.AllowedProjects(r.Context(), principal, action, ids)
		if err != nil {
			writeError(w, http.StatusInternalServerError, CodeInternal, "internal error")
			return nil, false
		}
	}

	// An explicit project narrows the review. A project the caller may not
	// review is refused identically to one that does not exist, so the endpoint
	// is not an existence oracle — and the refusal is the same 403 whether the
	// caller is instance-wide or not, rather than silently degrading to an
	// instance-only view that looks like an answer.
	filtered := false
	if want := strings.TrimSpace(r.URL.Query().Get("project")); want != "" {
		filtered = true
		narrowed := []string(nil)
		for _, id := range allowed {
			if id == want {
				narrowed = []string{id}
				break
			}
		}
		allowed = narrowed
	}

	if len(allowed) == 0 && (!v.instance || filtered) {
		// Deny-by-default, audited like any other denial (fail closed).
		if aerr := s.record(r, auditAction, "access", "denied", CodeForbidden, ""); aerr != nil {
			writeError(w, http.StatusInternalServerError, CodeInternal, "internal error")
			return nil, false
		}
		writeError(w, http.StatusForbidden, CodeForbidden, "access denied")
		return nil, false
	}

	if len(allowed) > maxAccessProjects {
		allowed = allowed[:maxAccessProjects]
		v.trunc.Projects = true
	}
	for _, id := range allowed {
		v.projects = append(v.projects, byID[id])
	}

	envs, err := store.NewEnvironmentRepo(s.st).ListForProjects(r.Context(), allowed, maxAccessEnvs+1)
	if err != nil {
		writeError(w, http.StatusInternalServerError, CodeInternal, "internal error")
		return nil, false
	}
	if len(envs) > maxAccessEnvs {
		envs = envs[:maxAccessEnvs]
		v.trunc.Environments = true
	}
	v.envs = envs
	return v, true
}

// loadAccessBindings fetches every reason anyone reaches a scope in view, in
// exactly TWO queries regardless of how many users, projects or environments
// are involved: direct bindings and group-derived ones, unioned into a single
// slice exactly as the engine unions them.
func (s *Server) loadAccessBindings(r *http.Request, v *accessView, userID string) ([]*store.RoleBinding, bool, error) {
	f := v.filter(userID)
	direct, err := store.NewRoleBindingRepo(s.st).ListForScopes(r.Context(), f, maxAccessBindings+1)
	if err != nil {
		return nil, false, err
	}
	truncated := len(direct) > maxAccessBindings
	if truncated {
		direct = direct[:maxAccessBindings]
	}
	derived, err := s.groupBindingsRepo().DerivedForScopes(r.Context(), f, maxAccessBindings+1)
	if err != nil {
		return nil, false, err
	}
	if len(derived) > maxAccessBindings {
		derived = derived[:maxAccessBindings]
		truncated = true
	}
	return append(direct, derived...), truncated, nil
}

// ---- scope description ----

type accessScopeView struct {
	// Key is the stable client-side identity of a column ("instance",
	// "project:<id>", "env:<id>").
	Key             string `json:"key"`
	Level           string `json:"scope_level"`
	ProjectID       string `json:"project_id,omitempty"`
	ProjectName     string `json:"project_name,omitempty"`
	EnvironmentID   string `json:"environment_id,omitempty"`
	EnvironmentSlug string `json:"environment_slug,omitempty"`
}

// scopeColumns renders the view's scopes, in review order: instance first, then
// each project immediately followed by its own environments — so a grid reads
// top-down as the inheritance chain it is.
func (v *accessView) scopeColumns() ([]accessScopeView, []authz.Resource) {
	var out []accessScopeView
	var res []authz.Resource
	if v.instance {
		out = append(out, accessScopeView{Key: "instance", Level: "instance"})
		res = append(res, authz.Instance())
	}
	byProject := map[string][]*store.Environment{}
	for _, e := range v.envs {
		byProject[e.ProjectID] = append(byProject[e.ProjectID], e)
	}
	for _, p := range v.projects {
		out = append(out, accessScopeView{
			Key: "project:" + p.ID, Level: "project", ProjectID: p.ID, ProjectName: p.Name,
		})
		res = append(res, authz.Resource{ProjectID: p.ID})
		for _, e := range byProject[p.ID] {
			out = append(out, accessScopeView{
				Key: "env:" + e.ID, Level: "environment",
				ProjectID: p.ID, ProjectName: p.Name,
				EnvironmentID: e.ID, EnvironmentSlug: e.Slug,
			})
			// The project id is carried on the resource so a PROJECT binding is
			// correctly seen to apply to the environment — top-down inheritance
			// is the union rule, not a separate one.
			res = append(res, authz.Resource{ProjectID: p.ID, EnvID: e.ID})
		}
	}
	return out, res
}

// accessSourceView is one reason a role holds at a scope.
type accessSourceView struct {
	Kind         string `json:"kind"` // "direct" | "group"
	Level        string `json:"scope_level"`
	Role         string `json:"role"`
	ViaGroupID   string `json:"via_group_id,omitempty"`
	ViaGroupName string `json:"via_group_name,omitempty"`
}

func sourceOf(b *store.RoleBinding) accessSourceView {
	sv := accessSourceView{Kind: "direct", Level: b.ScopeLevel, Role: b.Role}
	if b.ViaGroupID != nil {
		sv.Kind = "group"
		sv.ViaGroupID = *b.ViaGroupID
		sv.ViaGroupName = b.ViaGroupName
	}
	return sv
}

type accessCellView struct {
	UserID  string             `json:"user_id"`
	Scope   string             `json:"scope"`
	Role    string             `json:"role"`
	Sources []accessSourceView `json:"sources"`
}

// ---- GET /v1/access/matrix ----

// handleAccessMatrix answers "who can act where?" across every scope the caller
// may review — the cross-scope half of the members picture.
//
// Permissions are a union of all applicable bindings with no deny rules, which
// is invisible when you can only look at one scope at a time: an instance
// binding and a group binding on a sibling project both land in the same cell,
// and until you can see the grid you cannot tell that "nobody is bound to prod"
// does not mean "nobody can write prod".
//
// A read, so it is not audited (denials are). Cells are SPARSE — only where
// access actually holds — because a dense users x scopes grid is quadratic and
// almost entirely empty.
func (s *Server) handleAccessMatrix(w http.ResponseWriter, r *http.Request) {
	v, ok := s.resolveAccessView(w, r, authz.MemberRead, "access.review")
	if !ok {
		return
	}
	bindings, bTrunc, err := s.loadAccessBindings(r, v, "")
	if err != nil {
		writeError(w, http.StatusInternalServerError, CodeInternal, "internal error")
		return
	}
	v.trunc.Bindings = bTrunc

	byUser := map[string][]*store.RoleBinding{}
	for _, b := range bindings {
		byUser[b.SubjectUserID] = append(byUser[b.SubjectUserID], b)
	}
	users := make([]string, 0, len(byUser))
	for uid := range byUser {
		users = append(users, uid)
	}
	sort.Strings(users) // deterministic, so a truncated grid is stable
	if len(users) > maxAccessUsers {
		users = users[:maxAccessUsers]
		v.trunc.Users = true
	}

	scopes, resources := v.scopeColumns()
	cells := make([]accessCellView, 0, len(users))
	for _, uid := range users {
		held := byUser[uid]
		for i, res := range resources {
			// Same predicate the engine uses, over an already-loaded slice — a
			// review that invented its own scope-matching could report access
			// the server does not grant, and would still look authoritative.
			applicable := authz.ApplicableBindings(held, res)
			if len(applicable) == 0 {
				continue
			}
			if len(cells) >= maxAccessCells {
				v.trunc.Cells = true
				break
			}
			sources := make([]accessSourceView, 0, len(applicable))
			for _, b := range applicable {
				sources = append(sources, sourceOf(b))
			}
			cells = append(cells, accessCellView{
				UserID: uid, Scope: scopes[i].Key,
				Role:    string(authz.RoleFromBindings(applicable, res)),
				Sources: sources,
			})
		}
		if v.trunc.Cells {
			break
		}
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"scopes":           scopes,
		"user_ids":         users,
		"cells":            cells,
		"instance_visible": v.instance,
		"scoped":           !v.instance,
		"scope_projects":   len(v.projects),
		"truncated":        v.trunc,
		"complete":         v.instance && !v.trunc.Any(),
	})
}

// ---- GET /v1/access/users/{uid} ----

type accessGrantView struct {
	Level           string `json:"scope_level"`
	ProjectID       string `json:"project_id,omitempty"`
	ProjectName     string `json:"project_name,omitempty"`
	EnvironmentID   string `json:"environment_id,omitempty"`
	EnvironmentSlug string `json:"environment_slug,omitempty"`
	Role            string `json:"role"`
	Source          string `json:"source"` // "direct" | "group"
	ViaGroupID      string `json:"via_group_id,omitempty"`
	ViaGroupName    string `json:"via_group_name,omitempty"`
	CreatedAt       string `json:"created_at,omitempty"`
}

type accessBreakGlassView struct {
	Level           string `json:"scope_level"`
	ProjectID       string `json:"project_id,omitempty"`
	ProjectName     string `json:"project_name,omitempty"`
	EnvironmentID   string `json:"environment_id,omitempty"`
	EnvironmentSlug string `json:"environment_slug,omitempty"`
	Role            string `json:"role"`
	ExpiresAt       string `json:"expires_at"`
}

// handleAccessForUser answers "what can this person reach?" in one place — the
// question an offboarding has to answer before it can be called done.
//
// Three things are reported separately because they are removed in three
// different places: direct bindings (removable here), group-derived access
// (lives on the group, or in the IdP) and active break-glass grants (their own
// revoke, their own TTL). Collapsing them into one list would make revoke-all
// look like it did more than it can.
func (s *Server) handleAccessForUser(w http.ResponseWriter, r *http.Request) {
	uid := chi.URLParam(r, "uid")
	v, ok := s.resolveAccessView(w, r, authz.MemberRead, "access.review")
	if !ok {
		return
	}
	bindings, bTrunc, err := s.loadAccessBindings(r, v, uid)
	if err != nil {
		writeError(w, http.StatusInternalServerError, CodeInternal, "internal error")
		return
	}
	v.trunc.Bindings = bTrunc

	names := v.projectNames()
	envs := v.envIndex()
	grants := make([]accessGrantView, 0, len(bindings))
	for _, b := range bindings {
		g := accessGrantView{Level: b.ScopeLevel, Role: b.Role, Source: "direct",
			CreatedAt: b.CreatedAt.UTC().Format(time.RFC3339)}
		if b.ViaGroupID != nil {
			g.Source = "group"
			g.ViaGroupID = *b.ViaGroupID
			g.ViaGroupName = b.ViaGroupName
		}
		v.describeScope(&g.ProjectID, &g.ProjectName, &g.EnvironmentID, &g.EnvironmentSlug,
			b.ProjectID, b.EnvironmentID, names, envs)
		grants = append(grants, g)
	}

	// Break-glass is part of what this person can do RIGHT NOW, and revoke-all
	// cannot touch it — so an offboarding that ignored it would be wrong twice.
	// Only grants on scopes in view are reported, same boundary as everything
	// else here.
	var bg []accessBreakGlassView
	if s.breakGlass != nil {
		gs, err := s.breakGlass.ListActiveForUserOrdered(r.Context(), uid, time.Now())
		if err != nil {
			writeError(w, http.StatusInternalServerError, CodeInternal, "internal error")
			return
		}
		for _, g := range gs {
			if !v.covers(g.ScopeLevel, g.ProjectID, g.EnvironmentID) {
				continue
			}
			e := accessBreakGlassView{Level: g.ScopeLevel, Role: g.ElevatedRole,
				ExpiresAt: g.ExpiresAt.UTC().Format(time.RFC3339)}
			v.describeScope(&e.ProjectID, &e.ProjectName, &e.EnvironmentID, &e.EnvironmentSlug,
				g.ProjectID, g.EnvironmentID, names, envs)
			bg = append(bg, e)
		}
	}
	if bg == nil {
		bg = []accessBreakGlassView{}
	}

	// "Reaches" is the same union the matrix shows, for this one user: the
	// effective role at every visible scope, which is what an offboarder
	// actually has to clear.
	scopes, resources := v.scopeColumns()
	reaches := make([]accessCellView, 0)
	for i, res := range resources {
		applicable := authz.ApplicableBindings(bindings, res)
		if len(applicable) == 0 {
			continue
		}
		sources := make([]accessSourceView, 0, len(applicable))
		for _, b := range applicable {
			sources = append(sources, sourceOf(b))
		}
		reaches = append(reaches, accessCellView{
			UserID: uid, Scope: scopes[i].Key,
			Role:    string(authz.RoleFromBindings(applicable, res)),
			Sources: sources,
		})
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"user_id":          uid,
		"scopes":           scopes,
		"grants":           grants,
		"reaches":          reaches,
		"break_glass":      bg,
		"instance_visible": v.instance,
		"scoped":           !v.instance,
		"scope_projects":   len(v.projects),
		"truncated":        v.trunc,
		"complete":         v.instance && !v.trunc.Any(),
	})
}

// ---- POST /v1/access/users/{uid}/revoke-all ----

type revokedScopeView struct {
	Level           string `json:"scope_level"`
	ProjectID       string `json:"project_id,omitempty"`
	ProjectName     string `json:"project_name,omitempty"`
	EnvironmentID   string `json:"environment_id,omitempty"`
	EnvironmentSlug string `json:"environment_slug,omitempty"`
	Role            string `json:"role"`
	// Reason is set only on skipped entries.
	Reason string `json:"reason,omitempty"`
}

// handleAccessRevokeAll removes every DIRECT role binding the target holds at
// scopes the caller may manage — offboarding as one action instead of a hunt
// across instance, project and environment rows.
//
// What it deliberately does NOT do, all of it reported back rather than implied:
//
//   - group-derived access. The grant lives on the group binding (or, for an
//     `oidc` group, in the identity provider). Deleting the user's row here
//     would not touch it, and silently reporting success would certify an
//     offboarding that had not happened.
//   - active break-glass grants. They are time-boxed, revoked through their own
//     endpoint under their own authority, and folding them in here would let a
//     scope admin end an incident elevation granted at another scope.
//   - the account itself. Disabling is `POST /v1/users/{id}/disable`, a
//     separate authority (instance `user:manage`) with its own never-lock-out
//     guard.
//
// Two guards mirror the single-scope handlers. The last instance owner can
// never be removed — and here the whole request is refused rather than
// partially applied, because a bulk offboarding that half-succeeded is exactly
// the state nobody can reason about. And the delegation cap is measured against
// the caller's DURABLE bound role (M-1), never their effective one, so a
// break-glass elevation cannot be spent sweeping away bindings above the role
// the caller actually holds.
func (s *Server) handleAccessRevokeAll(w http.ResponseWriter, r *http.Request) {
	uid := chi.URLParam(r, "uid")
	v, ok := s.resolveAccessView(w, r, authz.MemberManage, "member.revoke_all")
	if !ok {
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

	bindings, bTrunc, err := s.loadAccessBindings(r, v, uid)
	if err != nil {
		writeError(w, http.StatusInternalServerError, CodeInternal, "internal error")
		return
	}
	v.trunc.Bindings = bTrunc

	var direct, viaGroup []*store.RoleBinding
	for _, b := range bindings {
		if b.ViaGroupID != nil {
			viaGroup = append(viaGroup, b)
			continue
		}
		direct = append(direct, b)
	}

	// Never-lock-out, checked BEFORE anything is removed.
	for _, b := range direct {
		if b.ScopeLevel != "instance" || b.Role != string(authz.RoleOwner) {
			continue
		}
		last, err := s.isLastInstanceOwner(r.Context(), uid)
		if err != nil {
			writeError(w, http.StatusInternalServerError, CodeInternal, "internal error")
			return
		}
		if last {
			writeError(w, http.StatusConflict, CodeValidation,
				"cannot remove the last instance owner; bind another owner first")
			return
		}
	}

	names := v.projectNames()
	envs := v.envIndex()

	// One binding load for the caller's cap across every scope in play, for the
	// same reason AllowedProjects exists: BoundRole per scope would re-query
	// direct and group bindings on every iteration.
	resources := make([]authz.Resource, len(direct))
	for i, b := range direct {
		resources[i] = v.bindingResource(b, envs)
	}
	granter, _ := PrincipalFrom(r.Context())
	caps, err := s.authz.BoundRoles(r.Context(), granter.ID, resources)
	if err != nil {
		writeError(w, http.StatusInternalServerError, CodeInternal, "internal error")
		return
	}

	describe := func(b *store.RoleBinding) revokedScopeView {
		out := revokedScopeView{Level: b.ScopeLevel, Role: b.Role}
		v.describeScope(&out.ProjectID, &out.ProjectName, &out.EnvironmentID, &out.EnvironmentSlug,
			b.ProjectID, b.EnvironmentID, names, envs)
		return out
	}

	revoked := make([]revokedScopeView, 0, len(direct))
	skipped := make([]revokedScopeView, 0)
	for i, b := range direct {
		held := caps[i]
		switch {
		case !authz.RoleAllows(held, authz.MemberManage):
			e := describe(b)
			e.Reason = "not_permitted"
			skipped = append(skipped, e)
			continue
		case !authz.RoleAtLeast(held, authz.Role(b.Role)):
			e := describe(b)
			e.Reason = "above_your_bound_role"
			skipped = append(skipped, e)
			continue
		}
		if err := s.authz.Revoke(r.Context(), uid, b.ScopeLevel, b.ProjectID, b.EnvironmentID); err != nil {
			if errors.Is(err, store.ErrNotFound) {
				// Removed concurrently — the desired state holds either way.
				continue
			}
			writeError(w, http.StatusInternalServerError, CodeInternal, "internal error")
			return
		}
		// Audited as an ordinary member.revoke at the exact scope, so a bulk
		// offboarding is greppable in the ledger exactly like the manual
		// revocations it replaces. Fail closed: s.authorize records DENIALS
		// only, so a successful mutation needs this call, and a failed audit
		// write must fail the request.
		if aerr := s.record(r, "member.revoke", memberResource(bindingScopeSpec(b), uid),
			"success", "", "via=revoke-all role="+b.Role); aerr != nil {
			writeError(w, http.StatusInternalServerError, CodeInternal, "internal error")
			return
		}
		revoked = append(revoked, describe(b))
	}

	remainingGroups := make([]accessGrantView, 0, len(viaGroup))
	for _, b := range viaGroup {
		g := accessGrantView{Level: b.ScopeLevel, Role: b.Role, Source: "group"}
		if b.ViaGroupID != nil {
			g.ViaGroupID = *b.ViaGroupID
		}
		g.ViaGroupName = b.ViaGroupName
		v.describeScope(&g.ProjectID, &g.ProjectName, &g.EnvironmentID, &g.EnvironmentSlug,
			b.ProjectID, b.EnvironmentID, names, envs)
		remainingGroups = append(remainingGroups, g)
	}

	activeGrants := 0
	if s.breakGlass != nil {
		gs, err := s.breakGlass.ListActiveForUserOrdered(r.Context(), uid, time.Now())
		if err != nil {
			writeError(w, http.StatusInternalServerError, CodeInternal, "internal error")
			return
		}
		for _, g := range gs {
			if v.covers(g.ScopeLevel, g.ProjectID, g.EnvironmentID) {
				activeGrants++
			}
		}
	}

	// complete means exactly one thing: after this call, nothing the caller can
	// see still grants this person access. Anything less says so.
	complete := v.instance && !v.trunc.Any() &&
		len(skipped) == 0 && len(remainingGroups) == 0 && activeGrants == 0

	if err := s.record(r, "member.revoke_all", "users/"+uid+"/access", "success", "",
		"revoked="+strconv.Itoa(len(revoked))+
			" skipped="+strconv.Itoa(len(skipped))+
			" group_remaining="+strconv.Itoa(len(remainingGroups))+
			" break_glass_remaining="+strconv.Itoa(activeGrants)+
			" complete="+boolStr(complete)); err != nil {
		writeError(w, http.StatusInternalServerError, CodeInternal, "internal error")
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"user_id": uid,
		"revoked": revoked,
		"skipped": skipped,
		"remaining": map[string]any{
			"group_bindings":     remainingGroups,
			"break_glass_grants": activeGrants,
		},
		"instance_visible": v.instance,
		"scoped":           !v.instance,
		"scope_projects":   len(v.projects),
		"truncated":        v.trunc,
		"complete":         complete,
	})
}

// ---- shared scope description helpers ----

func (v *accessView) projectNames() map[string]string {
	out := make(map[string]string, len(v.projects))
	for _, p := range v.projects {
		out[p.ID] = p.Name
	}
	return out
}

func (v *accessView) envIndex() map[string]*store.Environment {
	out := make(map[string]*store.Environment, len(v.envs))
	for _, e := range v.envs {
		out[e.ID] = e
	}
	return out
}

// covers reports whether a scope (as carried by a binding or a break-glass
// grant) is inside the view. It is the read-side twin of AccessScopeFilter: the
// filter keeps out-of-view rows out of the query, and this keeps them out of
// anything fetched by a different path.
func (v *accessView) covers(level string, projectID, envID *string) bool {
	switch level {
	case "instance":
		return v.instance
	case "project":
		if projectID == nil {
			return false
		}
		for _, p := range v.projects {
			if p.ID == *projectID {
				return true
			}
		}
	case "environment":
		if envID == nil {
			return false
		}
		for _, e := range v.envs {
			if e.ID == *envID {
				return true
			}
		}
	}
	return false
}

// describeScope fills the human-readable scope columns of a view row from a
// binding's raw scope keys.
func (v *accessView) describeScope(pid, pname, eid, eslug *string,
	projectID, envID *string, names map[string]string, envs map[string]*store.Environment) {
	if projectID != nil {
		*pid = *projectID
		*pname = names[*projectID]
	}
	if envID != nil {
		*eid = *envID
		if e, ok := envs[*envID]; ok {
			*eslug = e.Slug
			*pid = e.ProjectID
			*pname = names[e.ProjectID]
		}
	}
}

// bindingResource is the authz Resource a binding's own scope names, with the
// PARENT PROJECT filled in for an environment. That chain is load-bearing: it
// is what lets a project admin's binding be seen to apply to an environment
// inside their project, exactly as envScope resolves the real parent chain
// rather than trusting a path. Without it the delegation cap would refuse every
// environment-scoped revocation to anyone but an instance admin.
func (v *accessView) bindingResource(b *store.RoleBinding, envs map[string]*store.Environment) authz.Resource {
	switch b.ScopeLevel {
	case "project":
		if b.ProjectID != nil {
			return authz.Resource{ProjectID: *b.ProjectID}
		}
	case "environment":
		if b.EnvironmentID != nil {
			res := authz.Resource{EnvID: *b.EnvironmentID}
			if e, ok := envs[*b.EnvironmentID]; ok {
				res.ProjectID = e.ProjectID
			}
			return res
		}
	}
	return authz.Instance()
}

// bindingScopeSpec renders a binding as the scopeSpec the member audit paths
// use, so a bulk revocation writes the SAME resource string a single-scope
// revocation would.
func bindingScopeSpec(b *store.RoleBinding) scopeSpec {
	return scopeSpec{level: b.ScopeLevel, projectID: b.ProjectID, envID: b.EnvironmentID}
}
