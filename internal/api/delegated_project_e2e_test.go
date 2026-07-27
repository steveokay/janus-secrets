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
