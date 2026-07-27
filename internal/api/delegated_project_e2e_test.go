package api

import (
	"testing"
)

// The gap this closes: handleProjectCreate authorized against the INSTANCE
// scope, so the only way to let a team create projects was instance admin —
// which carries project:read everywhere. "Teams self-serve" and "teams cannot
// see each other" were mutually exclusive, and an org picks self-serve.
//
// This is the whole point of the feature in one test: a delegated creator makes
// a project, can work in it immediately, and STILL cannot see anyone else's.
func TestDelegatedProjectCreationKeepsIsolationE2E(t *testing.T) {
	ts, srv, email, password, _ := authStackFull(t)
	owner := login(t, ts.URL, email, password)

	// Someone else's project, which the delegated creator must never see.
	if _, err := srv.service.CreateProject(t.Context(), "other-team", "Other"); err != nil {
		t.Fatalf("create foreign project: %v", err)
	}

	var dev struct{ ID, Password string }
	if code := doAuthed(t, "POST", ts.URL+"/v1/users", owner, "", `{"email":"dev@corp.io"}`, &dev); code != 200 {
		t.Fatalf("create user: %d", code)
	}
	g := createGroup(t, ts.URL, owner, `{"name":"payments","kind":"local"}`)
	if code := doAuthed(t, "PUT", ts.URL+"/v1/groups/"+g.ID+"/members/"+dev.ID, owner, "", "", nil); code != 204 {
		t.Fatalf("add member: %d", code)
	}

	devSession := login(t, ts.URL, "dev@corp.io", dev.Password)

	// Before the capability is granted, creation is denied — deny by default.
	if code := doAuthed(t, "POST", ts.URL+"/v1/projects", devSession, "", `{"slug":"nope","name":"Nope"}`, nil); code != 403 {
		t.Fatalf("create without the capability: got %d, want 403", code)
	}

	// An admin delegates creation to the group.
	if code := doAuthed(t, "PUT", ts.URL+"/v1/groups/"+g.ID+"/capabilities", owner, "",
		`{"can_create_projects":true}`, nil); code != 200 {
		t.Fatalf("grant capability: %d", code)
	}

	var created struct{ ID, Slug string }
	if code := doAuthed(t, "POST", ts.URL+"/v1/projects", devSession, "",
		`{"slug":"payments-api","name":"Payments API"}`, &created); code != 201 {
		t.Fatalf("delegated create: got %d, want 201", code)
	}
	if created.Slug != "payments-api" {
		t.Fatalf("created = %+v", created)
	}

	// They can work in it immediately — no separate binding step.
	if code := doAuthed(t, "POST", ts.URL+"/v1/projects/"+created.ID+"/environments",
		devSession, "", `{"slug":"dev","name":"Dev"}`, nil); code != 201 {
		t.Fatalf("delegated creator making an environment: got %d, want 201", code)
	}

	// ...and still see ONLY their own project. This is the property that was
	// impossible before: self-serve without org-wide visibility.
	var list struct {
		Projects []struct{ ID, Slug string } `json:"projects"`
	}
	if code := doAuthed(t, "GET", ts.URL+"/v1/projects", devSession, "", "", &list); code != 200 {
		t.Fatalf("list projects: %d", code)
	}
	if len(list.Projects) != 1 || list.Projects[0].Slug != "payments-api" {
		t.Fatalf("delegated creator sees %+v — want only their own project", list.Projects)
	}
}

// The project belongs to the TEAM, not just the person who typed the command —
// otherwise groups would not have solved offboarding for projects either.
func TestDelegatedProjectIsOwnedByTheGroupE2E(t *testing.T) {
	ts, _, email, password, _ := authStackFull(t)
	owner := login(t, ts.URL, email, password)

	var creator, teammate struct{ ID, Password string }
	if code := doAuthed(t, "POST", ts.URL+"/v1/users", owner, "", `{"email":"creator@corp.io"}`, &creator); code != 200 {
		t.Fatalf("create creator: %d", code)
	}
	if code := doAuthed(t, "POST", ts.URL+"/v1/users", owner, "", `{"email":"mate@corp.io"}`, &teammate); code != 200 {
		t.Fatalf("create teammate: %d", code)
	}
	g := createGroup(t, ts.URL, owner, `{"name":"payments","kind":"local"}`)
	for _, uid := range []string{creator.ID, teammate.ID} {
		if code := doAuthed(t, "PUT", ts.URL+"/v1/groups/"+g.ID+"/members/"+uid, owner, "", "", nil); code != 204 {
			t.Fatalf("add member: %d", code)
		}
	}
	if code := doAuthed(t, "PUT", ts.URL+"/v1/groups/"+g.ID+"/capabilities", owner, "",
		`{"can_create_projects":true}`, nil); code != 200 {
		t.Fatalf("grant capability: %d", code)
	}

	creatorSession := login(t, ts.URL, "creator@corp.io", creator.Password)
	var created struct{ ID string }
	if code := doAuthed(t, "POST", ts.URL+"/v1/projects", creatorSession, "",
		`{"slug":"payments-api","name":"Payments API"}`, &created); code != 201 {
		t.Fatalf("create: %d", code)
	}

	// The teammate never touched the project, but reaches it through the group.
	mateSession := login(t, ts.URL, "mate@corp.io", teammate.Password)
	var list struct {
		Projects []struct{ Slug string } `json:"projects"`
	}
	if code := doAuthed(t, "GET", ts.URL+"/v1/projects", mateSession, "", "", &list); code != 200 {
		t.Fatalf("teammate list: %d", code)
	}
	if len(list.Projects) != 1 || list.Projects[0].Slug != "payments-api" {
		t.Fatalf("teammate sees %+v — the project should be the TEAM's", list.Projects)
	}

	// The group holds admin; the creator holds owner directly, so the project
	// always has someone who can delete it (a group binding can never be owner).
	var bindings struct {
		Bindings []struct {
			GroupName string `json:"group_name"`
			Role      string `json:"role"`
		} `json:"bindings"`
	}
	if code := doAuthed(t, "GET", ts.URL+"/v1/projects/"+created.ID+"/group-members", creatorSession, "", "", &bindings); code != 200 {
		t.Fatalf("list group bindings: %d", code)
	}
	if len(bindings.Bindings) != 1 || bindings.Bindings[0].GroupName != "payments" || bindings.Bindings[0].Role != "admin" {
		t.Fatalf("group bindings = %+v", bindings.Bindings)
	}
	if code := doAuthed(t, "DELETE", ts.URL+"/v1/projects/"+created.ID, creatorSession, "", "", nil); code != 204 {
		t.Fatalf("creator deleting their own project: got %d, want 204", code)
	}
}

// A member of a creating group must not be able to hand the project to a group
// they are not in — that would be a way to plant access into another team.
func TestDelegatedCreationCannotNameAForeignGroupE2E(t *testing.T) {
	ts, _, email, password, _ := authStackFull(t)
	owner := login(t, ts.URL, email, password)

	var dev struct{ ID, Password string }
	if code := doAuthed(t, "POST", ts.URL+"/v1/users", owner, "", `{"email":"dev@corp.io"}`, &dev); code != 200 {
		t.Fatalf("create user: %d", code)
	}
	mine := createGroup(t, ts.URL, owner, `{"name":"mine","kind":"local"}`)
	theirs := createGroup(t, ts.URL, owner, `{"name":"theirs","kind":"local"}`)
	if code := doAuthed(t, "PUT", ts.URL+"/v1/groups/"+mine.ID+"/members/"+dev.ID, owner, "", "", nil); code != 204 {
		t.Fatalf("add member: %d", code)
	}
	for _, gid := range []string{mine.ID, theirs.ID} {
		if code := doAuthed(t, "PUT", ts.URL+"/v1/groups/"+gid+"/capabilities", owner, "",
			`{"can_create_projects":true}`, nil); code != 200 {
			t.Fatalf("grant capability: %d", code)
		}
	}

	devSession := login(t, ts.URL, "dev@corp.io", dev.Password)
	if code := doAuthed(t, "POST", ts.URL+"/v1/projects", devSession, "",
		`{"slug":"planted","name":"Planted","owner_group_id":"`+theirs.ID+`"}`, nil); code != 403 {
		t.Fatalf("naming a group the caller is not in: got %d, want 403", code)
	}
	// Their own group still works, so the refusal is about membership.
	if code := doAuthed(t, "POST", ts.URL+"/v1/projects", devSession, "",
		`{"slug":"ok","name":"OK","owner_group_id":"`+mine.ID+`"}`, nil); code != 201 {
		t.Fatalf("naming their own group: %d", code)
	}
}

// Instance admins keep the historical behaviour: create without naming a group,
// and no group binding is invented for them.
func TestInstanceAdminCreationUnchangedE2E(t *testing.T) {
	ts, _, email, password, _ := authStackFull(t)
	owner := login(t, ts.URL, email, password)

	var created struct{ ID string }
	if code := doAuthed(t, "POST", ts.URL+"/v1/projects", owner, "", `{"slug":"admin-made","name":"Admin"}`, &created); code != 201 {
		t.Fatalf("admin create: %d", code)
	}
	var bindings struct {
		Bindings []struct{ GroupName string } `json:"bindings"`
	}
	if code := doAuthed(t, "GET", ts.URL+"/v1/projects/"+created.ID+"/group-members", owner, "", "", &bindings); code != 200 {
		t.Fatalf("list group bindings: %d", code)
	}
	if len(bindings.Bindings) != 0 {
		t.Fatalf("admin-created project got an unexpected group binding: %+v", bindings.Bindings)
	}
}

// Regression: authorization must be decided BEFORE the body is read. An earlier
// revision decoded first so it could see owner_group_id, which let an
// unauthorized caller distinguish a malformed body (400) from a denial (403)
// and skipped the denied audit event on that path.
func TestProjectCreateAuthorizesBeforeParsingBodyE2E(t *testing.T) {
	ts, _, email, password, _ := authStackFull(t)
	owner := login(t, ts.URL, email, password)

	var nobody struct{ ID, Password string }
	if code := doAuthed(t, "POST", ts.URL+"/v1/users", owner, "", `{"email":"nobody@corp.io"}`, &nobody); code != 200 {
		t.Fatalf("create user: %d", code)
	}
	session := login(t, ts.URL, "nobody@corp.io", nobody.Password)

	// Every shape of unusable body must still answer 403, never 400 — otherwise
	// the status code is an oracle for "the endpoint exists, your body is wrong".
	for _, body := range []string{``, `{`, `{}`, `{"slug":""}`, `{"name":"no slug"}`, `[]`} {
		if code := doAuthed(t, "POST", ts.URL+"/v1/projects", session, "", body, nil); code != 403 {
			t.Fatalf("unauthorized create with body %q: got %d, want 403", body, code)
		}
	}

	// And the denial is audited — a probing account must leave a trail.
	var events struct {
		Events []struct {
			Action string `json:"action"`
			Result string `json:"result"`
		} `json:"events"`
	}
	if code := doAuthed(t, "GET", ts.URL+"/v1/audit/events?limit=100", owner, "", "", &events); code != 200 {
		t.Fatalf("read audit: %d", code)
	}
	denied := 0
	for _, e := range events.Events {
		if e.Action == "project.create" && e.Result == "denied" {
			denied++
		}
	}
	if denied == 0 {
		t.Fatal("no project.create denied event recorded for the refused attempts")
	}
}

// An instance admin may hand a new project to a team they are not a member of.
// Requiring membership was a defect: they already hold member:manage everywhere
// and could bind that group a moment later, so it grants no new authority.
func TestInstanceAdminCanHandProjectToAnyGroupE2E(t *testing.T) {
	ts, _, email, password, _ := authStackFull(t)
	owner := login(t, ts.URL, email, password)

	// A plain group the admin is NOT in, and which cannot create projects.
	g := createGroup(t, ts.URL, owner, `{"name":"payments","kind":"local"}`)

	var created struct{ ID string }
	if code := doAuthed(t, "POST", ts.URL+"/v1/projects", owner, "",
		`{"slug":"handed","name":"Handed","owner_group_id":"`+g.ID+`"}`, &created); code != 201 {
		t.Fatalf("admin handing a project to a team: got %d, want 201", code)
	}
	var bindings struct {
		Bindings []struct {
			GroupName string `json:"group_name"`
			Role      string `json:"role"`
		} `json:"bindings"`
	}
	if code := doAuthed(t, "GET", ts.URL+"/v1/projects/"+created.ID+"/group-members", owner, "", "", &bindings); code != 200 {
		t.Fatalf("list bindings: %d", code)
	}
	if len(bindings.Bindings) != 1 || bindings.Bindings[0].GroupName != "payments" || bindings.Bindings[0].Role != "admin" {
		t.Fatalf("bindings = %+v", bindings.Bindings)
	}

	// A group that does not exist is a validation error for an admin, not a 403.
	if code := doAuthed(t, "POST", ts.URL+"/v1/projects", owner, "",
		`{"slug":"nope","name":"Nope","owner_group_id":"00000000-0000-0000-0000-000000000000"}`, nil); code != 400 {
		t.Fatalf("admin naming an unknown group: got %d, want 400", code)
	}
}

// Belonging to several creating groups is not an authorization failure — the
// caller must simply say which team owns the project.
func TestAmbiguousCreatorGroupIsAValidationErrorE2E(t *testing.T) {
	ts, _, email, password, _ := authStackFull(t)
	owner := login(t, ts.URL, email, password)

	var dev struct{ ID, Password string }
	if code := doAuthed(t, "POST", ts.URL+"/v1/users", owner, "", `{"email":"dev@corp.io"}`, &dev); code != 200 {
		t.Fatalf("create user: %d", code)
	}
	var ids []string
	for _, name := range []string{"alpha", "beta"} {
		g := createGroup(t, ts.URL, owner, `{"name":"`+name+`","kind":"local"}`)
		ids = append(ids, g.ID)
		if code := doAuthed(t, "PUT", ts.URL+"/v1/groups/"+g.ID+"/members/"+dev.ID, owner, "", "", nil); code != 204 {
			t.Fatalf("add member: %d", code)
		}
		if code := doAuthed(t, "PUT", ts.URL+"/v1/groups/"+g.ID+"/capabilities", owner, "",
			`{"can_create_projects":true}`, nil); code != 200 {
			t.Fatalf("grant capability: %d", code)
		}
	}

	session := login(t, ts.URL, "dev@corp.io", dev.Password)
	if code := doAuthed(t, "POST", ts.URL+"/v1/projects", session, "", `{"slug":"which","name":"Which"}`, nil); code != 400 {
		t.Fatalf("ambiguous owner: got %d, want 400", code)
	}
	// Naming one resolves it.
	if code := doAuthed(t, "POST", ts.URL+"/v1/projects", session, "",
		`{"slug":"which","name":"Which","owner_group_id":"`+ids[0]+`"}`, nil); code != 201 {
		t.Fatalf("naming a group: %d", code)
	}
}

// The owning-team picker was unusable by the very people it is for: listing the
// group catalog needs instance group:manage, so a delegated creator saw no
// picker — and with membership of two creating groups, every create failed as
// ambiguous with no way in the UI to resolve it.
func TestCallerCanReadTheirOwnGroupsE2E(t *testing.T) {
	ts, _, email, password, _ := authStackFull(t)
	owner := login(t, ts.URL, email, password)

	var dev struct{ ID, Password string }
	if code := doAuthed(t, "POST", ts.URL+"/v1/users", owner, "", `{"email":"dev@corp.io"}`, &dev); code != 200 {
		t.Fatalf("create user: %d", code)
	}
	mine := createGroup(t, ts.URL, owner, `{"name":"mine","kind":"local"}`)
	createGroup(t, ts.URL, owner, `{"name":"someone-elses","kind":"local"}`)
	if code := doAuthed(t, "PUT", ts.URL+"/v1/groups/"+mine.ID+"/members/"+dev.ID, owner, "", "", nil); code != 204 {
		t.Fatalf("add member: %d", code)
	}
	if code := doAuthed(t, "PUT", ts.URL+"/v1/groups/"+mine.ID+"/capabilities", owner, "",
		`{"can_create_projects":true}`, nil); code != 200 {
		t.Fatalf("grant capability: %d", code)
	}

	session := login(t, ts.URL, "dev@corp.io", dev.Password)
	// Premise: the catalog is still closed to them.
	if code := doAuthed(t, "GET", ts.URL+"/v1/groups", session, "", "", nil); code != 403 {
		t.Fatalf("non-admin listing the catalog: got %d, want 403", code)
	}

	var mineResp struct {
		Groups []struct {
			ID                string `json:"id"`
			Name              string `json:"name"`
			CanCreateProjects bool   `json:"can_create_projects"`
		} `json:"groups"`
	}
	if code := doAuthed(t, "GET", ts.URL+"/v1/auth/me/groups", session, "", "", &mineResp); code != 200 {
		t.Fatalf("read own groups: %d", code)
	}
	if len(mineResp.Groups) != 1 {
		t.Fatalf("own groups = %+v — must list ONLY the caller's own", mineResp.Groups)
	}
	if mineResp.Groups[0].Name != "mine" || !mineResp.Groups[0].CanCreateProjects {
		t.Fatalf("own group = %+v", mineResp.Groups[0])
	}
}
