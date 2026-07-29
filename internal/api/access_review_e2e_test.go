package api

import (
	"context"
	"testing"
)

type accessScopeJSON struct {
	Key             string `json:"key"`
	Level           string `json:"scope_level"`
	ProjectID       string `json:"project_id"`
	ProjectName     string `json:"project_name"`
	EnvironmentID   string `json:"environment_id"`
	EnvironmentSlug string `json:"environment_slug"`
}

type accessSourceJSON struct {
	Kind         string `json:"kind"`
	Level        string `json:"scope_level"`
	Role         string `json:"role"`
	ViaGroupID   string `json:"via_group_id"`
	ViaGroupName string `json:"via_group_name"`
}

type accessCellJSON struct {
	UserID  string             `json:"user_id"`
	Scope   string             `json:"scope"`
	Role    string             `json:"role"`
	Sources []accessSourceJSON `json:"sources"`
}

type accessTruncationJSON struct {
	Projects     bool `json:"projects"`
	Environments bool `json:"environments"`
	Bindings     bool `json:"bindings"`
	Users        bool `json:"users"`
	Cells        bool `json:"cells"`
}

type matrixJSON struct {
	Scopes          []accessScopeJSON    `json:"scopes"`
	UserIDs         []string             `json:"user_ids"`
	Cells           []accessCellJSON     `json:"cells"`
	InstanceVisible bool                 `json:"instance_visible"`
	Scoped          bool                 `json:"scoped"`
	ScopeProjects   int                  `json:"scope_projects"`
	Truncated       accessTruncationJSON `json:"truncated"`
	Complete        bool                 `json:"complete"`
}

type accessGrantJSON struct {
	Level         string `json:"scope_level"`
	ProjectID     string `json:"project_id"`
	ProjectName   string `json:"project_name"`
	EnvironmentID string `json:"environment_id"`
	Role          string `json:"role"`
	Source        string `json:"source"`
	ViaGroupName  string `json:"via_group_name"`
	Reason        string `json:"reason"`
}

type userAccessJSON struct {
	UserID     string            `json:"user_id"`
	Grants     []accessGrantJSON `json:"grants"`
	Reaches    []accessCellJSON  `json:"reaches"`
	BreakGlass []struct {
		Level string `json:"scope_level"`
		Role  string `json:"role"`
	} `json:"break_glass"`
	InstanceVisible bool `json:"instance_visible"`
	Scoped          bool `json:"scoped"`
	Complete        bool `json:"complete"`
}

type revokeAllJSON struct {
	UserID    string            `json:"user_id"`
	Revoked   []accessGrantJSON `json:"revoked"`
	Skipped   []accessGrantJSON `json:"skipped"`
	Remaining struct {
		GroupBindings    []accessGrantJSON `json:"group_bindings"`
		BreakGlassGrants int               `json:"break_glass_grants"`
	} `json:"remaining"`
	InstanceVisible bool `json:"instance_visible"`
	Scoped          bool `json:"scoped"`
	Complete        bool `json:"complete"`
}

func cellFor(m matrixJSON, uid, scope string) *accessCellJSON {
	for i := range m.Cells {
		if m.Cells[i].UserID == uid && m.Cells[i].Scope == scope {
			return &m.Cells[i]
		}
	}
	return nil
}

// The gap this closes: permissions are a UNION of every applicable binding with
// no deny rules, and looking at one scope at a time hides that completely. An
// environment with no binding of its own still has people who can write it —
// through the project, through the instance, through a group — and "who can
// write prod?" was unanswerable without walking every scope by hand.
func TestAccessMatrixMakesTheUnionVisibleE2E(t *testing.T) {
	ts, srv, email, password, _ := authStackFull(t)
	owner := login(t, ts.URL, email, password)
	ctx := context.Background()

	proj, err := srv.service.CreateProject(ctx, "atlas", "Atlas")
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	prod, err := srv.service.CreateEnvironment(ctx, proj.ID, "prod", "Prod")
	if err != nil {
		t.Fatalf("create env: %v", err)
	}

	var dev, lead struct{ ID, Password string }
	if code := doAuthed(t, "POST", ts.URL+"/v1/users", owner, "", `{"email":"dev@corp.io"}`, &dev); code != 200 {
		t.Fatalf("create dev: %d", code)
	}
	if code := doAuthed(t, "POST", ts.URL+"/v1/users", owner, "", `{"email":"lead@corp.io"}`, &lead); code != 200 {
		t.Fatalf("create lead: %d", code)
	}
	// lead: a PROJECT binding, which reaches prod without any prod binding.
	if code := doAuthed(t, "PUT", ts.URL+"/v1/projects/"+proj.ID+"/members/"+lead.ID, owner, "", `{"role":"admin"}`, nil); code != 204 {
		t.Fatalf("bind lead: %d", code)
	}
	// dev: nothing direct at all — everything comes through a group.
	g := createGroup(t, ts.URL, owner, `{"name":"payments","kind":"local"}`)
	if code := doAuthed(t, "PUT", ts.URL+"/v1/groups/"+g.ID+"/members/"+dev.ID, owner, "", "", nil); code != 204 {
		t.Fatalf("add group member: %d", code)
	}
	if code := doAuthed(t, "PUT", ts.URL+"/v1/projects/"+proj.ID+"/group-members/"+g.ID, owner, "", `{"role":"developer"}`, nil); code != 204 {
		t.Fatalf("bind group: %d", code)
	}

	var m matrixJSON
	if code := doAuthed(t, "GET", ts.URL+"/v1/access/matrix", owner, "", "", &m); code != 200 {
		t.Fatalf("matrix: %d", code)
	}
	if !m.InstanceVisible || m.Scoped || !m.Complete {
		t.Fatalf("an instance owner's review must be complete and unscoped: %+v", m)
	}
	prodScope := "env:" + prod.ID
	projScope := "project:" + proj.ID

	// The point of the grid: prod has NO binding of its own, yet three people
	// reach it, each for a different reason.
	if c := cellFor(m, lead.ID, prodScope); c == nil || c.Role != "admin" {
		t.Fatalf("lead should reach prod as admin via the project binding: %+v", c)
	} else if c.Sources[0].Level != "project" || c.Sources[0].Kind != "direct" {
		t.Fatalf("lead's prod access should be sourced to the project binding: %+v", c.Sources)
	}
	if c := cellFor(m, dev.ID, prodScope); c == nil || c.Role != "developer" {
		t.Fatalf("dev should reach prod as developer via the group: %+v", c)
	} else if c.Sources[0].Kind != "group" || c.Sources[0].ViaGroupName != "payments" {
		t.Fatalf("dev's prod access should be sourced to the group: %+v", c.Sources)
	}
	if c := cellFor(m, lead.ID, projScope); c == nil || c.Role != "admin" {
		t.Fatalf("lead should hold admin on the project itself: %+v", c)
	}

	// The instance owner reaches every scope, which is exactly the fact a
	// per-scope members list cannot show.
	var ownerID string
	for _, uid := range m.UserIDs {
		if uid != dev.ID && uid != lead.ID {
			ownerID = uid
		}
	}
	if ownerID == "" {
		t.Fatal("the bootstrap owner should appear in the review")
	}
	if c := cellFor(m, ownerID, prodScope); c == nil || c.Role != "owner" {
		t.Fatalf("the instance owner must show as owner on prod: %+v", c)
	}
}

// Deny-by-default, from the outside: a fresh account with no bindings must not
// learn that any scope exists.
func TestAccessReviewDeniesUnboundAccountE2E(t *testing.T) {
	ts, srv, email, password, _ := authStackFull(t)
	owner := login(t, ts.URL, email, password)
	if _, err := srv.service.CreateProject(context.Background(), "atlas", "Atlas"); err != nil {
		t.Fatalf("create project: %v", err)
	}

	var nobody struct{ ID, Password string }
	if code := doAuthed(t, "POST", ts.URL+"/v1/users", owner, "", `{"email":"nobody@corp.io"}`, &nobody); code != 200 {
		t.Fatalf("create user: %d", code)
	}
	session := login(t, ts.URL, "nobody@corp.io", nobody.Password)

	for _, path := range []string{
		"/v1/access/matrix",
		"/v1/access/users/" + nobody.ID,
	} {
		if code := doAuthed(t, "GET", ts.URL+path, session, "", "", nil); code != 403 {
			t.Fatalf("%s for an unbound account: got %d, want 403", path, code)
		}
	}
	if code := doAuthed(t, "POST", ts.URL+"/v1/access/users/"+nobody.ID+"/revoke-all", session, "", "", nil); code != 403 {
		t.Fatalf("revoke-all for an unbound account: got %d, want 403", code)
	}
}

// A project admin gets a REAL but PARTIAL answer, and the response says so:
// their own project's scopes only, with instance-level bindings left out
// entirely — those are a scope they cannot see, and showing them would leak the
// shape of instance membership.
func TestAccessMatrixIsScopedAndSaysSoE2E(t *testing.T) {
	ts, srv, email, password, _ := authStackFull(t)
	owner := login(t, ts.URL, email, password)
	ctx := context.Background()

	mine, err := srv.service.CreateProject(ctx, "mine", "Mine")
	if err != nil {
		t.Fatalf("create mine: %v", err)
	}
	theirs, err := srv.service.CreateProject(ctx, "theirs", "Theirs")
	if err != nil {
		t.Fatalf("create theirs: %v", err)
	}

	var lead struct{ ID, Password string }
	if code := doAuthed(t, "POST", ts.URL+"/v1/users", owner, "", `{"email":"lead@corp.io"}`, &lead); code != 200 {
		t.Fatalf("create lead: %d", code)
	}
	if code := doAuthed(t, "PUT", ts.URL+"/v1/projects/"+mine.ID+"/members/"+lead.ID, owner, "", `{"role":"admin"}`, nil); code != 204 {
		t.Fatalf("bind lead: %d", code)
	}
	session := login(t, ts.URL, "lead@corp.io", lead.Password)

	var m matrixJSON
	if code := doAuthed(t, "GET", ts.URL+"/v1/access/matrix", session, "", "", &m); code != 200 {
		t.Fatalf("matrix: %d", code)
	}
	if m.InstanceVisible || !m.Scoped || m.Complete {
		t.Fatalf("a project admin's review is partial and must say so: %+v", m)
	}
	if m.ScopeProjects != 1 {
		t.Fatalf("scope_projects = %d, want 1", m.ScopeProjects)
	}
	for _, sc := range m.Scopes {
		if sc.Level == "instance" {
			t.Fatal("instance scope must not appear for a caller who cannot read it")
		}
		if sc.ProjectID == theirs.ID {
			t.Fatalf("another team's project leaked into the review: %+v", sc)
		}
	}
	for _, c := range m.Cells {
		for _, src := range c.Sources {
			if src.Level == "instance" {
				t.Fatalf("an instance-level binding leaked into a scoped review: %+v", c)
			}
		}
	}
	// The bootstrap owner genuinely can act on this project, but only through an
	// instance binding — which is invisible here. That is the partial answer the
	// `instance_visible: false` flag exists to declare.
	if cellFor(m, lead.ID, "project:"+mine.ID) == nil {
		t.Fatal("the caller's own binding should be visible")
	}

	// Narrowing to a project they cannot review is refused, not silently
	// widened or answered — and refused the same way a nonexistent id is, so
	// the parameter is not an existence oracle.
	if code := doAuthed(t, "GET", ts.URL+"/v1/access/matrix?project="+theirs.ID, session, "", "", nil); code != 403 {
		t.Fatalf("filtering to another team's project: got %d, want 403", code)
	}
	if code := doAuthed(t, "GET", ts.URL+"/v1/access/matrix?project="+mine.ID, session, "", "", &m); code != 200 {
		t.Fatalf("filtering to their own project: %d", code)
	}
	if m.ScopeProjects != 1 {
		t.Fatalf("scope_projects = %d under a filter, want 1", m.ScopeProjects)
	}

	// And an instance-wide caller gets the same refusal for an id that does not
	// exist, rather than an instance-only view that reads like an answer.
	if code := doAuthed(t, "GET", ts.URL+"/v1/access/matrix?project=1cbb0f39-0000-4000-8000-000000000000",
		owner, "", "", nil); code != 403 {
		t.Fatalf("filtering to a nonexistent project: got %d, want 403", code)
	}
}

// The offboarding answer, and the honesty requirement on it: revoke-all removes
// direct bindings and CANNOT remove group-derived access. Reporting success
// without saying so would certify an offboarding that had not happened.
func TestAccessRevokeAllRemovesDirectAndReportsWhatItCannotE2E(t *testing.T) {
	ts, srv, email, password, _ := authStackFull(t)
	owner := login(t, ts.URL, email, password)
	ctx := context.Background()

	a, err := srv.service.CreateProject(ctx, "a", "A")
	if err != nil {
		t.Fatalf("create a: %v", err)
	}
	b, err := srv.service.CreateProject(ctx, "b", "B")
	if err != nil {
		t.Fatalf("create b: %v", err)
	}
	env, err := srv.service.CreateEnvironment(ctx, a.ID, "prod", "Prod")
	if err != nil {
		t.Fatalf("create env: %v", err)
	}

	var leaver struct{ ID, Password string }
	if code := doAuthed(t, "POST", ts.URL+"/v1/users", owner, "", `{"email":"leaver@corp.io"}`, &leaver); code != 200 {
		t.Fatalf("create leaver: %d", code)
	}
	for _, put := range []struct{ path, body string }{
		{"/v1/projects/" + a.ID + "/members/" + leaver.ID, `{"role":"developer"}`},
		{"/v1/projects/" + b.ID + "/members/" + leaver.ID, `{"role":"viewer"}`},
		{"/v1/projects/" + a.ID + "/environments/" + env.ID + "/members/" + leaver.ID, `{"role":"admin"}`},
		{"/v1/instance/members/" + leaver.ID, `{"role":"viewer"}`},
	} {
		if code := doAuthed(t, "PUT", ts.URL+put.path, owner, "", put.body, nil); code != 204 {
			t.Fatalf("bind %s: %d", put.path, code)
		}
	}
	g := createGroup(t, ts.URL, owner, `{"name":"payments","kind":"local"}`)
	if code := doAuthed(t, "PUT", ts.URL+"/v1/groups/"+g.ID+"/members/"+leaver.ID, owner, "", "", nil); code != 204 {
		t.Fatalf("add group member: %d", code)
	}
	if code := doAuthed(t, "PUT", ts.URL+"/v1/projects/"+b.ID+"/group-members/"+g.ID, owner, "", `{"role":"developer"}`, nil); code != 204 {
		t.Fatalf("bind group: %d", code)
	}

	// Before: four scopes, and the group on top.
	var before userAccessJSON
	if code := doAuthed(t, "GET", ts.URL+"/v1/access/users/"+leaver.ID, owner, "", "", &before); code != 200 {
		t.Fatalf("user access: %d", code)
	}
	directBefore := 0
	for _, gr := range before.Grants {
		if gr.Source == "direct" {
			directBefore++
		}
	}
	if directBefore != 4 {
		t.Fatalf("direct grants before = %d, want 4: %+v", directBefore, before.Grants)
	}

	var res revokeAllJSON
	if code := doAuthed(t, "POST", ts.URL+"/v1/access/users/"+leaver.ID+"/revoke-all", owner, "", "", &res); code != 200 {
		t.Fatalf("revoke-all: %d", code)
	}
	if len(res.Revoked) != 4 {
		t.Fatalf("revoked = %+v, want 4 scopes", res.Revoked)
	}
	if len(res.Skipped) != 0 {
		t.Fatalf("an instance owner should skip nothing: %+v", res.Skipped)
	}
	if len(res.Remaining.GroupBindings) != 1 || res.Remaining.GroupBindings[0].ViaGroupName != "payments" {
		t.Fatalf("the group-derived grant must be reported as remaining: %+v", res.Remaining.GroupBindings)
	}
	if res.Complete {
		t.Fatal("revoke-all MUST NOT report complete while group-derived access remains")
	}

	// After: every direct binding is gone, the group one is untouched.
	var after userAccessJSON
	if code := doAuthed(t, "GET", ts.URL+"/v1/access/users/"+leaver.ID, owner, "", "", &after); code != 200 {
		t.Fatalf("re-read: %d", code)
	}
	for _, gr := range after.Grants {
		if gr.Source == "direct" {
			t.Fatalf("a direct binding survived revoke-all: %+v", gr)
		}
	}
	if len(after.Grants) != 1 || after.Grants[0].Source != "group" {
		t.Fatalf("grants after = %+v, want exactly the group one", after.Grants)
	}

	// The mutation is audited — s.authorize records DENIALS only, so a
	// successful bulk revoke needs its own events or the ledger loses it.
	var events struct {
		Events []struct {
			Action   string `json:"action"`
			Resource string `json:"resource"`
			Result   string `json:"result"`
		} `json:"events"`
	}
	if code := doAuthed(t, "GET", ts.URL+"/v1/audit/events?limit=200", owner, "", "", &events); code != 200 {
		t.Fatalf("audit: %d", code)
	}
	revokes, summary := 0, 0
	for _, e := range events.Events {
		if e.Result != "success" {
			continue
		}
		switch e.Action {
		case "member.revoke":
			revokes++
		case "member.revoke_all":
			summary++
		}
	}
	if revokes != 4 {
		t.Fatalf("audit recorded %d member.revoke events, want one per revoked scope (4)", revokes)
	}
	if summary != 1 {
		t.Fatalf("audit recorded %d member.revoke_all summaries, want 1", summary)
	}
}

// Never-lock-out, on the bulk path: the whole request is refused rather than
// partially applied, because a half-finished offboarding is the state nobody
// can reason about.
func TestAccessRevokeAllRefusesLastInstanceOwnerE2E(t *testing.T) {
	ts, _, email, password, _ := authStackFull(t)
	owner := login(t, ts.URL, email, password)

	var me meResult
	if code := doAuthed(t, "GET", ts.URL+"/v1/auth/me", owner, "", "", &me); code != 200 {
		t.Fatalf("me: %d", code)
	}
	var env errEnvelope
	if code := doAuthed(t, "POST", ts.URL+"/v1/access/users/"+me.ID+"/revoke-all", owner, "", "", &env); code != 409 {
		t.Fatalf("revoke-all on the last instance owner: got %d, want 409", code)
	}

	// And nothing was removed on the way to the refusal.
	var after userAccessJSON
	if code := doAuthed(t, "GET", ts.URL+"/v1/access/users/"+me.ID, owner, "", "", &after); code != 200 {
		t.Fatalf("re-read: %d", code)
	}
	if len(after.Grants) == 0 {
		t.Fatal("the owner's binding must survive a refused revoke-all")
	}
}

// The delegation cap, on the bulk path. It is measured against the caller's
// DURABLE bound role (M-1), and a scope the caller cannot manage is reported as
// skipped rather than silently dropped.
func TestAccessRevokeAllRespectsDelegationCapE2E(t *testing.T) {
	ts, srv, email, password, _ := authStackFull(t)
	owner := login(t, ts.URL, email, password)
	ctx := context.Background()

	proj, err := srv.service.CreateProject(ctx, "atlas", "Atlas")
	if err != nil {
		t.Fatalf("create project: %v", err)
	}

	var lead, target struct{ ID, Password string }
	if code := doAuthed(t, "POST", ts.URL+"/v1/users", owner, "", `{"email":"lead@corp.io"}`, &lead); code != 200 {
		t.Fatalf("create lead: %d", code)
	}
	if code := doAuthed(t, "POST", ts.URL+"/v1/users", owner, "", `{"email":"target@corp.io"}`, &target); code != 200 {
		t.Fatalf("create target: %d", code)
	}
	if code := doAuthed(t, "PUT", ts.URL+"/v1/projects/"+proj.ID+"/members/"+lead.ID, owner, "", `{"role":"admin"}`, nil); code != 204 {
		t.Fatalf("bind lead: %d", code)
	}
	// The target outranks the project admin ON THAT PROJECT.
	if code := doAuthed(t, "PUT", ts.URL+"/v1/projects/"+proj.ID+"/members/"+target.ID, owner, "", `{"role":"owner"}`, nil); code != 204 {
		t.Fatalf("bind target: %d", code)
	}

	session := login(t, ts.URL, "lead@corp.io", lead.Password)
	var res revokeAllJSON
	if code := doAuthed(t, "POST", ts.URL+"/v1/access/users/"+target.ID+"/revoke-all", session, "", "", &res); code != 200 {
		t.Fatalf("revoke-all: %d", code)
	}
	if len(res.Revoked) != 0 {
		t.Fatalf("a project admin must not revoke a project owner: %+v", res.Revoked)
	}
	if len(res.Skipped) != 1 || res.Skipped[0].Reason != "above_your_bound_role" {
		t.Fatalf("skipped = %+v, want one above_your_bound_role entry", res.Skipped)
	}
	if res.Complete {
		t.Fatal("a revoke-all that skipped a binding is not complete")
	}
	// The binding really is still there.
	var after userAccessJSON
	if code := doAuthed(t, "GET", ts.URL+"/v1/access/users/"+target.ID, owner, "", "", &after); code != 200 {
		t.Fatalf("re-read: %d", code)
	}
	if len(after.Grants) != 1 || after.Grants[0].Role != "owner" {
		t.Fatalf("grants after = %+v, want the owner binding intact", after.Grants)
	}
}

// A service token is not a member and holds no bindings, so the review has
// nothing to say to it — and must not fall back to "everything".
func TestAccessReviewRefusesServiceTokenE2E(t *testing.T) {
	ts, _, email, password, configID := authStackFull(t)
	owner := login(t, ts.URL, email, password)

	var minted struct{ Token string }
	body := `{"name":"probe","scope":{"kind":"config","id":"` + configID + `"},"access":"readwrite"}`
	if code := doAuthed(t, "POST", ts.URL+"/v1/tokens", owner, "", body, &minted); code != 200 {
		t.Fatalf("mint: %d", code)
	}
	if code := doAuthed(t, "GET", ts.URL+"/v1/access/matrix", "", minted.Token, "", nil); code != 403 {
		t.Fatalf("service token matrix: got %d, want 403", code)
	}
}
